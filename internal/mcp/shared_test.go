// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMetaMarshal(t *testing.T) {
	// Verify that Meta values round-trip.
	for _, meta := range []Meta{
		{Data: nil, ProgressToken: nil},
		{Data: nil, ProgressToken: "p"},
		{Data: map[string]any{"d": true}, ProgressToken: nil},
		{Data: map[string]any{"d": true}, ProgressToken: "p"},
	} {
		got := roundTrip(t, meta)
		if !cmp.Equal(got, meta) {
			t.Errorf("\ngot  %#v\nwant %#v", got, meta)
		}
	}

	// Check errors.
	for _, tt := range []struct {
		meta Meta
		want string
	}{
		{
			Meta{Data: map[string]any{"progressToken": "p"}, ProgressToken: 1},
			"duplicate",
		},
		{
			Meta{ProgressToken: true},
			"bad type",
		},
	} {
		_, err := json.Marshal(tt.meta)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%+v: got %v, want error containing %q", tt.meta, err, tt.want)
		}
	}

	// Accept progressToken in map if the field is nil.
	// It will unmarshal by populating ProgressToken.
	meta := Meta{Data: map[string]any{"progressToken": "p"}}
	got := roundTrip(t, meta)
	want := Meta{ProgressToken: "p"}
	if !cmp.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func roundTrip[T any](t *testing.T, v T) T {
	t.Helper()
	bytes, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var res T
	if err := json.Unmarshal(bytes, &res); err != nil {
		t.Fatal(err)
	}
	return res
}
