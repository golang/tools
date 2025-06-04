// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

// testToolHandler is used for type inference in TestNewTool.
func testToolHandler[T any](context.Context, *mcp.ServerSession, *mcp.CallToolParams[T]) (*mcp.CallToolResult, error) {
	panic("not implemented")
}

func TestNewTool(t *testing.T) {
	tests := []struct {
		tool *mcp.ServerTool
		want *jsonschema.Schema
	}{
		{
			mcp.NewTool("basic", "", testToolHandler[struct {
				Name string `json:"name"`
			}]),
			&jsonschema.Schema{
				Type:     "object",
				Required: []string{"name"},
				Properties: map[string]*jsonschema.Schema{
					"name": {Type: "string"},
				},
				AdditionalProperties: &jsonschema.Schema{Not: new(jsonschema.Schema)},
			},
		},
		{
			mcp.NewTool("enum", "", testToolHandler[struct{ Name string }], mcp.Input(
				mcp.Property("Name", mcp.Enum("x", "y", "z")),
			)),
			&jsonschema.Schema{
				Type:     "object",
				Required: []string{"Name"},
				Properties: map[string]*jsonschema.Schema{
					"Name": {Type: "string", Enum: []any{"x", "y", "z"}},
				},
				AdditionalProperties: &jsonschema.Schema{Not: new(jsonschema.Schema)},
			},
		},
		{
			mcp.NewTool("required", "", testToolHandler[struct {
				Name     string `json:"name"`
				Language string `json:"language"`
				X        int    `json:"x,omitempty"`
				Y        int    `json:"y,omitempty"`
			}], mcp.Input(
				mcp.Property("x", mcp.Required(true)))),
			&jsonschema.Schema{
				Type:     "object",
				Required: []string{"name", "language", "x"},
				Properties: map[string]*jsonschema.Schema{
					"language": {Type: "string"},
					"name":     {Type: "string"},
					"x":        {Type: "integer"},
					"y":        {Type: "integer"},
				},
				AdditionalProperties: &jsonschema.Schema{Not: new(jsonschema.Schema)},
			},
		},
		{
			mcp.NewTool("set_schema", "", testToolHandler[struct {
				X int `json:"x,omitempty"`
				Y int `json:"y,omitempty"`
			}], mcp.Input(
				mcp.Schema(&jsonschema.Schema{Type: "object"})),
			),
			&jsonschema.Schema{
				Type: "object",
			},
		},
	}
	for _, test := range tests {
		if diff := cmp.Diff(test.want, test.tool.Tool.InputSchema, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("NewTool(%v) mismatch (-want +got):\n%s", test.tool.Tool.Name, diff)
		}
	}
}

func TestNewToolValidate(t *testing.T) {
	// Check that the tool returned from NewTool properly validates its input schema.

	type req struct {
		I int
		B bool
		S string `json:",omitempty"`
		P *int   `json:",omitempty"`
	}

	dummyHandler := func(context.Context, *mcp.ServerSession, *mcp.CallToolParams[req]) (*mcp.CallToolResult, error) {
		return nil, nil
	}

	tool := mcp.NewTool("test", "test", dummyHandler)
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
			_, err = tool.Handler(context.Background(), nil,
				&mcp.CallToolParams[json.RawMessage]{Arguments: json.RawMessage(raw)})
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
