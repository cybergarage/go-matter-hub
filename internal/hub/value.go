// Copyright (C) 2026 The go-matter-hub Authors. All rights reserved.
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
	"encoding/hex"
	"strconv"

	"github.com/cybergarage/go-matter/matter/encoding/tlv"
	"github.com/cybergarage/go-matter/matter/protocol/im"
)

// maxExactInteger is the largest integer a JSON number, read as a float64,
// holds exactly.
const maxExactInteger = 1 << 53

// jsonValue converts the value of an attribute to a value encoding/json can
// marshal:
//
//   - null: nil
//   - boolean: bool
//   - integer: uint64 or int64; one which a JSON number cannot hold exactly
//     (beyond 2^53), such as a node ID, becomes its decimal digits in a string
//   - floating point: float64
//   - UTF-8 string: string
//   - octet string: its bytes in hexadecimal, as a string
//   - structure: map[string]any keyed by the decimal context tag of each field
//   - array and list: []any
//
// The types of the data model (an enumeration, a bitmap) are not applied
// here: a value is what the node sent.
func jsonValue(v *im.AttributeValue) any {
	if v == nil || v.Element == nil {
		return nil
	}
	e := v.Element
	t := e.Type()
	switch {
	case t.IsNull():
		return nil
	case t.IsStructure():
		fields := make(map[string]any, len(v.Children))
		for i, c := range v.Children {
			fields[fieldKey(c, i)] = jsonValue(c)
		}
		return fields
	case t.IsContainer():
		items := make([]any, 0, len(v.Children))
		for _, c := range v.Children {
			items = append(items, jsonValue(c))
		}
		return items
	}
	if b, ok := e.Bool(); ok {
		return b
	}
	if n, ok := e.Unsigned(); ok {
		if n > maxExactInteger {
			return strconv.FormatUint(n, 10)
		}
		return n
	}
	if n, ok := e.Signed(); ok {
		if n > maxExactInteger || n < -maxExactInteger {
			return strconv.FormatInt(n, 10)
		}
		return n
	}
	if f, ok := e.Float(); ok {
		return f
	}
	if s, ok := e.UTF8(); ok {
		return s
	}
	if b, ok := e.Bytes(); ok {
		return hex.EncodeToString(b)
	}
	return nil
}

// fieldKey names a field of a structure by its context tag; a field with
// another kind of tag, which the data model does not use, is named by its
// position.
func fieldKey(c *im.AttributeValue, index int) string {
	if c != nil && c.Element != nil {
		if n, ok := tlv.NewContextNumberFromTag(c.Element.Tag()); ok {
			return strconv.Itoa(int(n))
		}
	}
	return "#" + strconv.Itoa(index)
}
