// Copyright 2018 Anapaya Systems
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package handlers

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/opentracing/opentracing-go"

	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/ctrl/path_mgmt"
	"github.com/scionproto/scion/go/lib/ctrl/seg"
	"github.com/scionproto/scion/go/lib/infra"
	"github.com/scionproto/scion/go/lib/infra/dedupe"
	"github.com/scionproto/scion/go/lib/infra/messenger"
	"github.com/scionproto/scion/go/lib/log"
	"github.com/scionproto/scion/go/lib/pathdb/query"
	"github.com/scionproto/scion/go/lib/revcache"
	"github.com/scionproto/scion/go/lib/scrypto"
	"github.com/scionproto/scion/go/lib/scrypto/trc"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/path_srv/internal/segutil"
	"github.com/scionproto/scion/go/proto"
)

type segReqHandler struct {
	*baseHandler
	localIA     addr.IA
	segsDeduper dedupe.Deduper
}

// isValidDst returns true if segReq contains a valid destination for segReq handlers,
// false otherwise.
func (h *segReqHandler) isValidDst(segReq *path_mgmt.SegReq) bool {
	// No validation on source here!
	if segReq.DstIA().IsZero() || segReq.DstIA().I == 0 || segReq.DstIA().Equal(h.localIA) {
		logger := log.FromCtx(h.request.Context())
		logger.Warn("[segReqHandler] Drop, invalid dstIA", "dstIA", segReq.DstIA())
		return false
	}
	return true
}

func (h *segReqHandler) isCoreDst(ctx context.Context, segReq *path_mgmt.SegReq,
	resolver func() (net.Addr, error)) (bool, error) {

	if segReq.DstIA().A == 0 {
		return true, nil
	}
	// Try local trust store first.
	if dstTrc, err := h.trustStore.GetValidCachedTRC(ctx, segReq.DstIA().I); err == nil {
		return dstTrc.CoreASes.Contains(segReq.DstIA()), nil
	} else if resolver == nil {
		return false, common.NewBasicError("Destination TRC not found", err,
			"isd", segReq.DstIA().I)
	}
	remote, err := resolver()
	if err != nil {
		return false, common.NewBasicError("Unable to resolve remote", err)
	}
	dstTRC, err := h.trustStore.GetValidTRC(ctx, segReq.DstIA().I, remote)
	if err != nil {
		return false, common.NewBasicError("Failed to get TRC for dst", err)
	}
	return dstTRC.CoreASes.Contains(segReq.DstIA()), nil
}

func (h *segReqHandler) coreASes(ctx context.Context) (trc.CoreASMap, error) {
	srcTRC, err := h.trustStore.GetTRC(ctx, h.localIA.I, scrypto.LatestVer)
	if err != nil {
		return nil, common.NewBasicError("Failed to get TRC for localIA", err)
	}
	return srcTRC.CoreASes, nil
}

func (h *segReqHandler) fetchDownSegs(ctx context.Context, dst addr.IA,
	cPSAddr func() (net.Addr, error), dbOnly bool) (seg.Segments, error) {

	// try local cache first
	q := &query.Params{
		SegTypes: []proto.PathSegType{proto.PathSegType_down},
		EndsAt:   []addr.IA{dst},
	}
	segs, err := h.fetchSegsFromDB(ctx, q)
	if err != nil {
		return nil, err
	}
	if dbOnly || len(segs) > 0 {
		refetch := !dbOnly
		if !dbOnly {
			refetch, err = h.shouldRefetchSegsForDst(ctx, dst, time.Now())
			if err != nil {
				log.FromCtx(ctx).Warn("[segReqHandler] failed to get last query", "err", err)
			}
		}
		if !refetch {
			return segs, nil
		}
	}
	cAddr, err := cPSAddr()
	if err != nil {
		return nil, err
	}
	logger := log.FromCtx(ctx)
	logger.Debug("[segReqHandler] Fetch down segments", "dst", dst, "remote", cAddr)
	if err = h.fetchAndSaveSegs(ctx, addr.IA{}, dst, cAddr); err != nil {
		return nil, err
	}
	return h.fetchSegsFromDB(ctx, q)
}

func (h *segReqHandler) fetchAndSaveSegs(ctx context.Context, src, dst addr.IA,
	cPSAddr net.Addr) error {

	logger := log.FromCtx(ctx)
	r := &path_mgmt.SegReq{RawSrcIA: src.IAInt(), RawDstIA: dst.IAInt()}
	// The logging below is used for acceptance testing do not delete!
	if snetAddr, ok := cPSAddr.(*snet.Addr); ok {
		logger.Trace("[segReqHandler] Sending segment request", "NextHop", snetAddr.NextHop)
	}
	return h.getSegsFromNetwork(ctx, r, cPSAddr, messenger.NextId())
}

func (h *segReqHandler) handleReceivedSegs(ctx context.Context, queryTime time.Time,
	cPSAddr net.Addr, req *path_mgmt.SegReq, segs *path_mgmt.SegReply) {

	logger := log.FromCtx(ctx)
	segs = segs.Sanitize(logger)
	if segs.Recs != nil {
		logSegRecs(logger, "[segReqHandler]", cPSAddr, segs.Recs)
		recs := segs.Recs.Recs
		revInfos, err := revcache.FilterNew(ctx, h.revCache, segs.Recs.SRevInfos)
		if err != nil {
			logger.Error("[segReqHandler] Failed to filter new revocations", "err", err)
			// in case of error we just assume all of them are new and continue.
			revInfos = segs.Recs.SRevInfos
		}
		if err := h.verifyAndStore(ctx, cPSAddr, recs, revInfos); err != nil {
			logger.Error("Failed to verify and store segments", "err", err)
		} else {
			// Only insert next query if we found some results.
			if _, err := h.pathDB.InsertNextQuery(ctx, req.DstIA(),
				queryTime.Add(h.queryInt)); err != nil {
				logger.Warn("Failed to insert last queried", "err", err)
			}
		}
	}
}

func (h *segReqHandler) getSegsFromNetwork(ctx context.Context,
	req *path_mgmt.SegReq, server net.Addr, id uint64) error {

	var span opentracing.Span
	span, ctx = opentracing.StartSpanFromContext(ctx, "getSegsFromNetwork")
	defer span.Finish()
	responseC, cancelF, span := h.segsDeduper.Request(ctx, &segReq{
		segReq:      req,
		server:      server,
		id:          id,
		postprocess: h.handleReceivedSegs,
	})
	defer span.Finish()
	defer cancelF()
	select {
	case response := <-responseC:
		return response.Error
	case <-ctx.Done():
		return common.NewBasicError("Context done while waiting for Segs", ctx.Err())
	}
}

func (h *segReqHandler) sendReply(ctx context.Context, rw infra.ResponseWriter,
	upSegs, coreSegs, downSegs []*seg.PathSegment, segReq *path_mgmt.SegReq) {

	logger := log.FromCtx(ctx)
	revs, err := segutil.RelevantRevInfos(ctx, h.revCache, upSegs, coreSegs, downSegs)
	if err != nil {
		logger.Error("[segReqHandler] Failed to find relevant revocations for reply", "err", err)
		// the client might still be able to use the segments so continue here.
	}
	recs := &path_mgmt.SegRecs{
		Recs:      h.collectSegs(upSegs, coreSegs, downSegs),
		SRevInfos: revs,
	}
	reply := &path_mgmt.SegReply{
		Req:  segReq,
		Recs: recs,
	}
	if err := rw.SendSegReply(ctx, reply); err != nil {
		logger.Error("[segReqHandler] Failed to send reply!", "err", err)
		return
	}
	logger.Debug("[segReqHandler] reply sent", "id", h.request.ID,
		"ups", len(upSegs), "cores", len(coreSegs), "downs", len(downSegs))
}

func (h *segReqHandler) collectSegs(upSegs, coreSegs, downSegs []*seg.PathSegment) []*seg.Meta {
	logger := log.FromCtx(h.request.Context())
	lup, lcore, ldown := limit(len(upSegs), len(coreSegs), len(downSegs), 9)
	recs := make([]*seg.Meta, 0, len(upSegs)+len(coreSegs)+len(downSegs))
	for i := range upSegs {
		if i == lup {
			break
		}
		s := upSegs[i]
		logger.Trace(fmt.Sprintf("[segReqHandler:collectSegs] up %v -> %v",
			s.FirstIA(), s.LastIA()))
		recs = append(recs, seg.NewMeta(s, proto.PathSegType_up))
	}
	for i := range coreSegs {
		if i == lcore {
			break
		}
		s := coreSegs[i]
		logger.Trace(fmt.Sprintf("[segReqHandler:collectSegs] core %v -> %v",
			s.FirstIA(), s.LastIA()))
		recs = append(recs, seg.NewMeta(s, proto.PathSegType_core))
	}
	for i := range downSegs {
		if i == ldown {
			break
		}
		s := downSegs[i]
		logger.Trace(fmt.Sprintf("[segReqHandler:collectSegs] down %v -> %v",
			s.FirstIA(), s.LastIA()))
		recs = append(recs, seg.NewMeta(s, proto.PathSegType_down))
	}
	return recs
}

// XXX(roosd): Dirty hack to avoid exceeding jumbo frames until quic is implemented.
// Revert tainted code after quic is implemented.
func limit(upSegs, coreSegs, downSegs, all int) (int, int, int) {
	for upSegs+coreSegs+downSegs > all {
		switch {
		case upSegs >= coreSegs && upSegs >= downSegs:
			upSegs--
		case coreSegs >= upSegs && coreSegs >= downSegs:
			coreSegs--
		default:
			downSegs--
		}
	}
	return upSegs, coreSegs, downSegs
}

// shouldRefetchSegsForDst returns true if the segments for the given dst
// should be fetched from the remote PS. Returns true on error, so the value can be used anyway.
func (h *segReqHandler) shouldRefetchSegsForDst(ctx context.Context, dst addr.IA,
	now time.Time) (bool, error) {

	nq, err := h.pathDB.GetNextQuery(ctx, dst)
	if err != nil {
		return true, err
	}
	if nq == nil {
		return true, nil
	}
	return now.After(*nq), nil
}

// segsToMap converts the segs slice to a map of IAs to segments.
// The IA (key) is selected using the key function.
func segsToMap(segs []*seg.PathSegment,
	key func(*seg.PathSegment) addr.IA) map[addr.IA]struct{} {

	res := make(map[addr.IA]struct{})
	for _, s := range segs {
		res[key(s)] = struct{}{}
	}
	return res
}
