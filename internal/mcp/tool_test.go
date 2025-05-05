// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

// testToolHandler is used for type inference in TestMakeTool.
func testToolHandler[T any](context.Context, *mcp.ClientConnection, T) ([]mcp.Content, error) {
	panic("not implemented")
}

func TestMakeTool(t *testing.T) {
	tests := []struct {
		tool *mcp.Tool
		want *jsonschema.Schema
	}{
		{
			mcp.MakeTool("basic", "", testToolHandler[struct {
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
			mcp.MakeTool("enum", "", testToolHandler[struct{ Name string }], mcp.Input(
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
			mcp.MakeTool("required", "", testToolHandler[struct {
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
			mcp.MakeTool("set_schema", "", testToolHandler[struct {
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
		if diff := cmp.Diff(test.want, test.tool.Definition.InputSchema, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("MakeTool(%v) mismatch (-want +got):\n%s", test.tool.Definition.Name, diff)
		}
	}
}
