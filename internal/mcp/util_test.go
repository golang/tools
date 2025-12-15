// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestMarshalStructWithMap(t *testing.T) {
	type S struct {
		A int
		B string `json:"b,omitempty"`
		u bool
		M map[string]any `json:",omitempty"`
	}
	t.Run("basic", func(t *testing.T) {
		s := S{A: 1, B: "two", M: map[string]any{"!@#": true}}
		got, err := marshalStructWithMap(&s, "M")
		if err != nil {
			t.Fatal(err)
		}
		want := `{"A":1,"b":"two","!@#":true}`
		if g := string(got); g != want {
			t.Errorf("\ngot  %s\nwant %s", g, want)
		}

		var un S
		if err := unmarshalStructWithMap(got, &un, "M"); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(s, un, cmpopts.IgnoreUnexported(S{})); diff != "" {
			t.Errorf("mismatch (-want, +got):\n%s", diff)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		s := S{A: 1, B: "two", M: map[string]any{"b": "dup"}}
		_, err := marshalStructWithMap(&s, "M")
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("got %v, want error with 'duplicate'", err)
		}
	})
}
