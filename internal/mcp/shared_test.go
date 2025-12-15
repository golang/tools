// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
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

// TODO(jba): this shouldn't be in this file, but tool_test.go doesn't have access to unexported symbols.
func TestNewServerToolValidate(t *testing.T) {
	// Check that the tool returned from NewServerTool properly validates its input schema.

	type req struct {
		I int
		B bool
		S string `json:",omitempty"`
		P *int   `json:",omitempty"`
	}

	dummyHandler := func(context.Context, *ServerSession, *CallToolParamsFor[req]) (*CallToolResultFor[any], error) {
		return nil, nil
	}

	tool := NewServerTool("test", "test", dummyHandler)
	// Need to add the tool to a server to get resolved schemas.
	// s := NewServer("", "", nil)

	for _, tt := range []struct {
		desc string
		args map[string]any
		want string // error should contain this string; empty for success
	}{
		{
			"both required",
			map[string]any{"I": 1, "B": true},
			"",
		},
		{
			"optional",
			map[string]any{"I": 1, "B": true, "S": "foo"},
			"",
		},
		{
			"wrong type",
			map[string]any{"I": 1.5, "B": true},
			"cannot unmarshal",
		},
		{
			"extra property",
			map[string]any{"I": 1, "B": true, "C": 2},
			"unknown field",
		},
		{
			"value for pointer",
			map[string]any{"I": 1, "B": true, "P": 3},
			"",
		},
		{
			"null for pointer",
			map[string]any{"I": 1, "B": true, "P": nil},
			"",
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			raw, err := json.Marshal(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tool.rawHandler(context.Background(), nil,
				&CallToolParamsFor[json.RawMessage]{Arguments: json.RawMessage(raw)})
			if err == nil && tt.want != "" {
				t.Error("got success, wanted failure")
			}
			if err != nil {
				if tt.want == "" {
					t.Fatalf("failed with:\n%s\nwanted success", err)
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("got:\n%s\nwanted to contain %q", err, tt.want)
				}
			}
		})
	}
}
