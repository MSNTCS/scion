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
package trc_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/scionproto/scion/go/lib/scrypto"
	trc "github.com/scionproto/scion/go/lib/scrypto/trc/v2"
	"github.com/scionproto/scion/go/lib/xtest"
)

func TestPrimaryASUnmarshalJSON(t *testing.T) {
	tests := map[string]struct {
		Input          string
		Primary        trc.PrimaryAS
		ExpectedErrMsg string
	}{
		"Valid": {
			Input: `
			{
				"Attributes": ["Issuing", "Core"],
				"Keys": {
					"Issuing": {
						"KeyVersion": 1,
    					"Algorithm": "ed25519",
    					"Key": "YW5hcGF5YSDinaQgIHNjaW9u"
					}
				}
			}`,
			Primary: trc.PrimaryAS{
				Attributes: trc.Attributes{"Issuing", "Core"},
				Keys: map[trc.KeyType]trc.KeyMeta{
					trc.IssuingKey: {
						KeyVersion: 1,
						Algorithm:  scrypto.Ed25519,
						Key: xtest.MustParseHexString("616e617061796120e2" +
							"9da420207363696f6e"),
					},
				},
			},
		},
		"Attributes not set": {
			Input: `
			{
				"Keys": {
					"Issuing": {
						"KeyVersion": 1,
    					"Algorithm": "ed25519",
    					"Key": "YW5hcGF5YSDinaQgIHNjaW9u"
					}
				}
			}`,
			ExpectedErrMsg: trc.ErrAttributesNotSet.Error(),
		},
		"Keys not set": {
			Input: `
			{
				"Attributes": ["Issuing", "Core"]
			}`,
			ExpectedErrMsg: trc.ErrKeysNotSet.Error(),
		},
		"Invalid key meta": {
			Input: `
			{
				"Attributes": ["Issuing", "Core"],
				"Keys": {
					"Issuing": {
    					"Algorithm": "ed25519",
    					"Key": "YW5hcGF5YSDinaQgIHNjaW9u"
					}
				}
			}`,
			ExpectedErrMsg: trc.ErrKeyVersionNotSet.Error(),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var primary trc.PrimaryAS
			err := json.Unmarshal([]byte(test.Input), &primary)
			if test.ExpectedErrMsg == "" {
				require.NoError(t, err)
				assert.Equal(t, test.Primary, primary)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.ExpectedErrMsg)
			}
		})
	}
}

func TestKeyMetaUnmarshalJSON(t *testing.T) {
	tests := map[string]struct {
		Input          string
		Meta           trc.KeyMeta
		ExpectedErrMsg string
	}{
		"Valid": {
			Input: `
			{
				"KeyVersion": 1,
				"Algorithm": "ed25519",
				"Key": "YW5hcGF5YSDinaQgIHNjaW9u"
			}`,
			Meta: trc.KeyMeta{
				KeyVersion: 1,
				Algorithm:  scrypto.Ed25519,
				Key:        xtest.MustParseHexString("616e617061796120e29da420207363696f6e"),
			},
		},
		"KeyVersion not set": {
			Input: `
			{
				"Algorithm": "ed25519",
				"Key": "YW5hcGF5YSDinaQgIHNjaW9u"
			}`,
			ExpectedErrMsg: trc.ErrKeyVersionNotSet.Error(),
		},
		"Algorithm not set": {
			Input: `
			{
				"KeyVersion": 1,
				"Key": "YW5hcGF5YSDinaQgIHNjaW9u"
			}`,
			ExpectedErrMsg: trc.ErrAlgorithmNotSet.Error(),
		},
		"Key not set": {
			Input: `
			{
				"KeyVersion": 1,
				"Algorithm": "ed25519"
			}`,
			ExpectedErrMsg: trc.ErrKeyNotSet.Error(),
		},
		"Unknown field": {
			Input: `
			{
				"UnknownField": "UNKNOWN"
			}`,
			ExpectedErrMsg: `json: unknown field "UnknownField"`,
		},
		"invalid json": {
			Input: `
			{
				"KeyVersion": 1,
				"Algorithm": "ed25519"
			`,
			ExpectedErrMsg: "unexpected end of JSON input",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var meta trc.KeyMeta
			err := json.Unmarshal([]byte(test.Input), &meta)
			if test.ExpectedErrMsg == "" {
				require.NoError(t, err)
				assert.Equal(t, test.Meta, meta)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.ExpectedErrMsg)
			}
		})
	}
}

func TestAttributesUnmarshalJSON(t *testing.T) {
	tests := map[string]struct {
		Input     string
		Assertion assert.ErrorAssertionFunc
		Expected  trc.Attributes
	}{
		"Garbage": {
			Input:     `"test"`,
			Assertion: assert.Error,
		},
		"Empty": {
			Input:     `[]`,
			Assertion: assert.Error,
		},
		"Non-Core and Authoritative": {
			Input:     `["Authoritative"]`,
			Assertion: assert.Error,
		},
		"Duplication": {
			Input:     `["Core", "Core"]`,
			Assertion: assert.Error,
		},
		"Authoritative, Core, Issuing and Voting": {
			Input:     `["Authoritative", "Issuing", "Core", "Voting"]`,
			Assertion: assert.NoError,
			Expected:  trc.Attributes{trc.Authoritative, trc.Issuing, trc.Voting, trc.Core},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var attrs trc.Attributes
			test.Assertion(t, json.Unmarshal([]byte(test.Input), &attrs))
			if test.Expected != nil {
				assert.ElementsMatch(t, test.Expected, attrs)
			}
		})
	}
}

func TestAttributesMarshalJSON(t *testing.T) {
	type mockPrimaryAS struct {
		Attributes trc.Attributes
	}
	tests := map[string]struct {
		Attrs     trc.Attributes
		Expected  []byte
		Assertion assert.ErrorAssertionFunc
	}{
		"Duplication": {
			Attrs:     trc.Attributes{trc.Core, trc.Core},
			Assertion: assert.Error,
		},
		"Non-Core and Authoritative": {
			Attrs:     trc.Attributes{trc.Authoritative},
			Assertion: assert.Error,
		},
		"Core and Voting": {
			Attrs:     trc.Attributes{trc.Voting, trc.Core},
			Expected:  []byte(`{"Attributes":["Voting","Core"]}`),
			Assertion: assert.NoError,
		},
		"Authoritative, Core, Voting and Issuing": {
			Attrs:     trc.Attributes{trc.Authoritative, trc.Issuing, trc.Voting, trc.Core},
			Expected:  []byte(`{"Attributes":["Authoritative","Issuing","Voting","Core"]}`),
			Assertion: assert.NoError,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(mockPrimaryAS{Attributes: test.Attrs})
			test.Assertion(t, err)
			assert.Equal(t, test.Expected, b)
		})
	}
}

func TestAttributeUnmarshalJSON(t *testing.T) {
	tests := map[string]struct {
		Input     string
		Assertion assert.ErrorAssertionFunc
		Expected  trc.Attribute
	}{
		"Garbage": {
			Input:     `"test"`,
			Assertion: assert.Error,
		},
		"Integer": {
			Input:     `42`,
			Assertion: assert.Error,
		},
		"Wrong case": {
			Input:     `"voting"`,
			Assertion: assert.Error,
		},
		"Authoritative": {
			Input:     `"Authoritative"`,
			Assertion: assert.NoError,
			Expected:  trc.Authoritative,
		},
		"Core": {
			Input:     `"Core"`,
			Assertion: assert.NoError,
			Expected:  trc.Core,
		},
		"Issuing": {
			Input:     `"Issuing"`,
			Assertion: assert.NoError,
			Expected:  trc.Issuing,
		},
		"Voting": {
			Input:     `"Voting"`,
			Assertion: assert.NoError,
			Expected:  trc.Voting,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var attr trc.Attribute
			test.Assertion(t, json.Unmarshal([]byte(test.Input), &attr))
			assert.Equal(t, test.Expected, attr)
		})
	}
}

func TestKeyTypeUnmarshalJSON(t *testing.T) {
	tests := map[string]struct {
		Input     string
		Assertion assert.ErrorAssertionFunc
		Expected  trc.KeyType
	}{
		"Garbage": {
			Input:     `"test"`,
			Assertion: assert.Error,
		},
		"Integer": {
			Input:     `42`,
			Assertion: assert.Error,
		},
		"Wrong case": {
			Input:     `"offline"`,
			Assertion: assert.Error,
		},
		"OfflineKey": {
			Input:     `"Offline"`,
			Assertion: assert.NoError,
			Expected:  trc.OfflineKey,
		},
		"OnlineKey": {
			Input:     `"Online"`,
			Assertion: assert.NoError,
			Expected:  trc.OnlineKey,
		},
		"IssuingKey": {
			Input:     `"Issuing"`,
			Assertion: assert.NoError,
			Expected:  trc.IssuingKey,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var attr trc.KeyType
			test.Assertion(t, json.Unmarshal([]byte(test.Input), &attr))
			assert.Equal(t, test.Expected, attr)
		})
	}
}
