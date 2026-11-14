// Copyright (C) 2026 The homewire Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package hub

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/cybergarage/go-matter/matter/encoding/tlv"
	"github.com/cybergarage/go-matter/matter/protocol/im"
)

// elements decodes the elements of an encoding one after another, a
// container as the element which opens it.
func elements(t *testing.T, build func(enc tlv.Encoder)) []tlv.Element {
	t.Helper()
	enc := tlv.NewEncoder()
	build(enc)
	dec := tlv.NewDecoderWithBytes(enc.Bytes())
	var out []tlv.Element
	for dec.Next() {
		out = append(out, dec.Element())
	}
	if len(out) == 0 {
		t.Fatal("nothing decoded")
	}
	return out
}

func scalar(t *testing.T, build func(enc tlv.Encoder)) *im.AttributeValue {
	t.Helper()
	return &im.AttributeValue{Element: elements(t, build)[0]}
}

func TestJSONValueOfScalars(t *testing.T) {
	anon := tlv.NewAnonymousTag()
	tests := []struct {
		name  string
		build func(enc tlv.Encoder)
		want  any
	}{
		{"true", func(enc tlv.Encoder) { enc.PutBool(anon, true) }, true},
		{"false", func(enc tlv.Encoder) { enc.PutBool(anon, false) }, false},
		{"unsigned", func(enc tlv.Encoder) { _ = enc.PutUnsigned(anon, 254) }, uint64(254)},
		{"signed", func(enc tlv.Encoder) { _ = enc.PutSigned(anon, -40) }, int64(-40)},
		{"largest exact unsigned", func(enc tlv.Encoder) { _ = enc.PutUnsigned(anon, 1<<53) }, uint64(1 << 53)},
		{"node ID", func(enc tlv.Encoder) { _ = enc.PutUnsigned(anon, 0xB923078EEB190343) }, "13340514831612576579"},
		{"large negative", func(enc tlv.Encoder) { _ = enc.PutSigned(anon, math.MinInt64) }, "-9223372036854775808"},
		{"string", func(enc tlv.Encoder) { _ = enc.PutUTF8(anon, "lamp") }, "lamp"},
		{"octets", func(enc tlv.Encoder) { _ = enc.PutOctet(anon, []byte{0xDE, 0xAD, 0x00}) }, "dead00"},
		{"null", func(enc tlv.Encoder) { enc.PutNull(anon) }, nil},
	}
	for _, tt := range tests {
		got := jsonValue(scalar(t, tt.build))
		if got != tt.want {
			t.Errorf("%s: %#v, want %#v", tt.name, got, tt.want)
		}
		if _, err := json.Marshal(got); err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
	if jsonValue(nil) != nil || jsonValue(&im.AttributeValue{}) != nil {
		t.Error("a missing value is not nil")
	}
}

func TestJSONValueOfContainers(t *testing.T) {
	// An array of two structures, as a list attribute reports its entries:
	// [{0: 22, 1: "a"}, {0: null, 2: [1, 2]}].
	es := elements(t, func(enc tlv.Encoder) {
		anon := tlv.NewAnonymousTag()
		enc.BeginArray(anon)
		enc.BeginStructure(anon)
		_ = enc.PutUnsigned(tlv.NewContextTag(0), 22)
		_ = enc.PutUTF8(tlv.NewContextTag(1), "a")
		_ = enc.EndContainer()
		enc.BeginStructure(anon)
		enc.PutNull(tlv.NewContextTag(0))
		enc.BeginArray(tlv.NewContextTag(2))
		_ = enc.PutUnsigned(anon, 1)
		_ = enc.PutUnsigned(anon, 2)
		_ = enc.EndContainer()
		_ = enc.EndContainer()
		_ = enc.EndContainer()
	})
	// The decoder yields the elements in order, the ends of containers
	// included; the tree is put together here as go-matter does for a
	// report.
	var values []tlv.Element
	for _, e := range es {
		if e.Type() != tlv.EndOfContainer {
			values = append(values, e)
		}
	}
	if len(values) != 9 {
		t.Fatalf("decoded %d elements", len(values))
	}
	leaf := func(i int) *im.AttributeValue { return &im.AttributeValue{Element: values[i]} }
	tree := &im.AttributeValue{Element: values[0], Children: []*im.AttributeValue{
		{Element: values[1], Children: []*im.AttributeValue{leaf(2), leaf(3)}},
		{Element: values[4], Children: []*im.AttributeValue{
			leaf(5),
			{Element: values[6], Children: []*im.AttributeValue{leaf(7), leaf(8)}},
		}},
	}}
	b, err := json.Marshal(jsonValue(tree))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `[{"0":22,"1":"a"},{"0":null,"2":[1,2]}]`; got != want {
		t.Fatalf("value = %s, want %s", got, want)
	}
}
