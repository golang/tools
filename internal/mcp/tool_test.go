// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

// testToolHandler is used for type inference in TestNewServerTool.
func testToolHandler[T any](context.Context, *ServerSession, *CallToolParamsFor[T]) (*CallToolResultFor[any], error) {
	panic("not implemented")
}

func TestNewServerTool(t *testing.T) {
	tests := []struct {
		tool *ServerTool
		want *jsonschema.Schema
	}{
		{
			NewServerTool("basic", "", testToolHandler[struct {
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
			NewServerTool("enum", "", testToolHandler[struct{ Name string }], Input(
				Property("Name", Enum("x", "y", "z")),
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
			NewServerTool("required", "", testToolHandler[struct {
				Name     string `json:"name"`
				Language string `json:"language"`
				X        int    `json:"x,omitempty"`
				Y        int    `json:"y,omitempty"`
			}], Input(
				Property("x", Required(true)))),
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
			NewServerTool("set_schema", "", testToolHandler[struct {
				X int `json:"x,omitempty"`
				Y int `json:"y,omitempty"`
			}], Input(
				Schema(&jsonschema.Schema{Type: "object"})),
			),
			&jsonschema.Schema{
				Type: "object",
			},
		},
	}
	for _, test := range tests {
		if diff := cmp.Diff(test.want, test.tool.Tool.InputSchema, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("NewServerTool(%v) mismatch (-want +got):\n%s", test.tool.Tool.Name, diff)
		}
	}
}

func TestUnmarshalSchema(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"x": {Type: "integer", Default: json.RawMessage("3")},
		},
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatal(err)
	}

	type S struct {
		X int `json:"x"`
	}

	for _, tt := range []struct {
		data string
		v    any
		want any
	}{
		{`{"x": 1}`, new(S), &S{X: 1}},
		{`{}`, new(S), &S{X: 3}},       // default applied
		{`{"x": 0}`, new(S), &S{X: 3}}, // FAIL: should be 0. (requires double unmarshal)
		{`{"x": 1}`, new(map[string]any), &map[string]any{"x": 1.0}},
		{`{}`, new(map[string]any), &map[string]any{"x": 3.0}}, // default applied
		{`{"x": 0}`, new(map[string]any), &map[string]any{"x": 0.0}},
	} {
		raw := json.RawMessage(tt.data)
		if err := unmarshalSchema(raw, resolved, tt.v); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(tt.v, tt.want) {
			t.Errorf("got %#v, want %#v", tt.v, tt.want)
		}

	}
}
