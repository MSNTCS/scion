// Copyright 2019 Anapaya Systems
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

package beaconing

import (
	"fmt"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/scionproto/scion/go/beacon_srv/internal/beacon"
	"github.com/scionproto/scion/go/beacon_srv/internal/beaconing/mock_beaconing"
	"github.com/scionproto/scion/go/beacon_srv/internal/ifstate"
	"github.com/scionproto/scion/go/beacon_srv/internal/onehop"
	"github.com/scionproto/scion/go/lib/addr"
	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/overlay"
	"github.com/scionproto/scion/go/lib/scrypto"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/snet/mock_snet"
	"github.com/scionproto/scion/go/lib/xtest"
	"github.com/scionproto/scion/go/lib/xtest/graph"
)

func TestPropagatorRun(t *testing.T) {
	macProp, err := scrypto.InitMac(make(common.RawBytes, 16))
	xtest.FailOnErr(t, err)
	macSender, err := scrypto.InitMac(make(common.RawBytes, 16))
	xtest.FailOnErr(t, err)
	pub, priv, err := scrypto.GenKeyPair(scrypto.Ed25519)
	xtest.FailOnErr(t, err)

	type test struct {
		name     string
		inactive map[common.IFIDType]bool
		expected int
		core     bool
	}
	topoFile := map[bool]string{false: topoNonCore, true: topoCore}
	// The beacons to propagate for the non-core and core tests.
	beacons := map[bool][][]common.IFIDType{
		false: {
			{graph.If_120_X_111_B},
			{graph.If_130_B_120_A, graph.If_120_X_111_B},
			{graph.If_130_B_120_A, graph.If_120_X_111_B},
		},
		true: {
			{graph.If_120_A_110_X},
			{graph.If_130_B_120_A, graph.If_120_A_110_X},
		},
	}
	// The interfaces in the non-core and core topologies.
	allIntfs := map[bool]map[common.IFIDType]common.IFIDType{
		false: {
			graph.If_111_A_112_X: graph.If_112_X_111_A,
			graph.If_111_B_120_X: graph.If_120_X_111_B,
			graph.If_111_B_211_A: graph.If_211_A_111_B,
			graph.If_111_C_211_A: graph.If_211_A_111_C,
			graph.If_111_C_121_X: graph.If_121_X_111_C,
		},
		true: {
			graph.If_110_X_120_A: graph.If_120_A_110_X,
			graph.If_110_X_130_A: graph.If_130_A_110_X,
			graph.If_110_X_210_X: graph.If_210_X_110_X,
		},
	}
	tests := []test{
		{
			name:     "Non-core: All interfaces active",
			expected: 3,
		},
		{
			name:     "Non-core: One peer inactive",
			inactive: map[common.IFIDType]bool{graph.If_111_C_121_X: true},
			expected: 3,
		},
		{
			name: "Non-core: All peers inactive",
			inactive: map[common.IFIDType]bool{
				graph.If_111_C_121_X: true,
				graph.If_111_B_211_A: true,
				graph.If_111_C_211_A: true,
			},
			expected: 3,
		},
		{
			name:     "Non-core: Child interface inactive",
			inactive: map[common.IFIDType]bool{graph.If_111_A_112_X: true},
		},
		{
			name:     "Core: All interfaces active",
			expected: 3,
			core:     true,
		},
		{
			name:     "Core: 1-ff00:0:120 inactive",
			inactive: map[common.IFIDType]bool{graph.If_110_X_120_A: true},
			// Should not create beacon if ingress interface is down.
			expected: 0,
			core:     true,
		},
		{
			name:     "Core: 1-ff00:0:130 inactive",
			inactive: map[common.IFIDType]bool{graph.If_110_X_130_A: true},
			expected: 2,
			core:     true,
		},
		{
			name:     "Core: 2-ff00:0:210 inactive",
			inactive: map[common.IFIDType]bool{graph.If_110_X_210_X: true},
			expected: 1,
			core:     true,
		},
		{
			name: "Core: All inactive",
			inactive: map[common.IFIDType]bool{
				graph.If_110_X_120_A: true,
				graph.If_110_X_130_A: true,
				graph.If_110_X_210_X: true,
			},
			core: true,
		},
	}
	for _, test := range tests {
		Convey(test.name, t, func() {
			mctrl := gomock.NewController(t)
			defer mctrl.Finish()
			topoProvider := xtest.TopoProviderFromFile(t, topoFile[test.core])
			provider := mock_beaconing.NewMockBeaconProvider(mctrl)
			conn := mock_snet.NewMockPacketConn(mctrl)
			cfg := PropagatorConf{
				Config: ExtenderConf{
					Signer: testSigner(t, priv, topoProvider.Get().ISD_AS),
					Mac:    macProp,
					Intfs:  ifstate.NewInterfaces(topoProvider.Get().IFInfoMap, ifstate.Config{}),
					MTU:    uint16(topoProvider.Get().MTU),
				},
				BeaconProvider: provider,
				Core:           test.core,
				Sender: &onehop.Sender{
					IA:   topoProvider.Get().ISD_AS,
					Conn: conn,
					Addr: &addr.AppAddr{
						L3: addr.HostFromIPStr("127.0.0.1"),
						L4: addr.NewL4UDPInfo(4242),
					},
					MAC: macSender,
				},
			}
			p, err := cfg.New()
			SoMsg("err", err, ShouldBeNil)
			for ifid, remote := range allIntfs[test.core] {
				if test.inactive[ifid] {
					continue
				}
				cfg.Config.Intfs.Get(ifid).Activate(remote)
			}
			g := graph.NewDefaultGraph(mctrl)
			provider.EXPECT().BeaconsToPropagate(gomock.Any()).MaxTimes(1).DoAndReturn(
				func(_ interface{}) (<-chan beacon.BeaconOrErr, error) {
					res := make(chan beacon.BeaconOrErr, len(beacons[test.core]))
					for _, desc := range beacons[test.core] {
						res <- testBeaconOrErr(g, desc)
					}
					close(res)
					return res, nil
				},
			)
			msgsMtx := sync.Mutex{}
			var msgs []msg
			conn.EXPECT().WriteTo(gomock.Any(), gomock.Any()).Times(test.expected).DoAndReturn(
				func(ipkt, iov interface{}) error {
					msgsMtx.Lock()
					defer msgsMtx.Unlock()
					msgs = append(msgs, msg{
						pkt: ipkt.(*snet.SCIONPacket),
						ov:  iov.(*overlay.OverlayAddr),
					})
					return nil
				},
			)
			p.Run(nil)
			for i, msg := range msgs {
				Convey(fmt.Sprintf("Packet %d is correct", i), func() {
					checkMsg(t, msg, pub, topoProvider.Get().IFInfoMap)
				})
			}
		})
	}
}