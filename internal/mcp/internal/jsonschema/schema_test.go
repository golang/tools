// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"encoding/json"
	"testing"
)

func TestMarshal(t *testing.T) {
	for _, s := range []*Schema{
		{Type: "null"},
		{Types: []string{"null", "number"}},
		{Type: "string", MinLength: Ptr(20.0)},
		{Minimum: Ptr(20.0)},
		{Items: &Schema{Type: "integer"}},
		{Const: Ptr(any(0))},
		{Const: Ptr(any(nil))},
		{Const: Ptr(any([]int{}))},
		{Const: Ptr(any(map[string]any{}))},
	} {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("marshal: %s", data)
		var got *Schema
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if !Equal(got, s) {
			t.Errorf("got %+v, want %+v", got, s)
			if got.Const != nil && s.Const != nil {
				t.Logf("Consts: got %#v (%[1]T), want %#v (%[2]T)", *got.Const, *s.Const)
			}
		}
	}
}
