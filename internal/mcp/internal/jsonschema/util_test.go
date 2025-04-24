// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEqual(t *testing.T) {
	for _, tt := range []struct {
		x1, x2 any
		want   bool
	}{
		{0, 1, false},
		{1, 1.0, true},
		{nil, 0, false},
		{"0", 0, false},
		{2.5, 2.5, true},
		{[]int{1, 2}, []float64{1.0, 2.0}, true},
		{[]int(nil), []int{}, false},
		{[]map[string]any(nil), []map[string]any{}, false},
		{
			map[string]any{"a": 1, "b": 2.0},
			map[string]any{"a": 1.0, "b": 2},
			true,
		},
	} {
		check := func(x1, x2 any, want bool) {
			t.Helper()
			if got := Equal(x1, x2); got != want {
				t.Errorf("jsonEqual(%#v, %#v) = %t, want %t", x1, x2, got, want)
			}
		}
		check(tt.x1, tt.x1, true)
		check(tt.x2, tt.x2, true)
		check(tt.x1, tt.x2, tt.want)
		check(tt.x2, tt.x1, tt.want)
	}
}

func TestJSONType(t *testing.T) {
	for _, tt := range []struct {
		val  string
		want string
	}{
		{`null`, "null"},
		{`0`, "integer"},
		{`0.0`, "integer"},
		{`1e2`, "integer"},
		{`0.1`, "number"},
		{`""`, "string"},
		{`true`, "boolean"},
		{`[]`, "array"},
		{`{}`, "object"},
	} {
		var val any
		if err := json.Unmarshal([]byte(tt.val), &val); err != nil {
			t.Fatal(err)
		}
		got, ok := jsonType(reflect.ValueOf(val))
		if !ok {
			t.Fatalf("jsonType failed on %q", tt.val)
		}
		if got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.val, got, tt.want)
		}

	}
}
