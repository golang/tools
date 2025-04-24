// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"

	"golang.org/x/tools/internal/mcp/internal/jsonschema"
	"golang.org/x/tools/internal/mcp/internal/protocol"
)

// A ToolHandler handles a call to tools/call.
type ToolHandler func(context.Context, *ClientConnection, json.RawMessage) (*protocol.CallToolResult, error)

// A Tool is a tool definition that is bound to a tool handler.
type Tool struct {
	Definition protocol.Tool
	Handler    ToolHandler
}

// MakeTool is a helper to make a tool using reflection on the given handler.
//
// The input schema for the tool is extracted from the request type for the
// handler, and used to unmmarshal and validate requests to the handler.
//
// It is the caller's responsibility that the handler request type can produce
// a valid schema, as documented by [jsonschema.ForType]; otherwise, MakeTool
// panics.
func MakeTool[TReq any](name, description string, handler func(context.Context, *ClientConnection, TReq) ([]Content, error)) *Tool {
	schema, err := jsonschema.For[TReq]()
	if err != nil {
		panic(err)
	}
	wrapped := func(ctx context.Context, cc *ClientConnection, args json.RawMessage) (*protocol.CallToolResult, error) {
		var v TReq
		if err := unmarshalSchema(args, schema, &v); err != nil {
			return nil, err
		}
		content, err := handler(ctx, cc, v)
		if err != nil {
			return &protocol.CallToolResult{
				Content: marshalContent([]Content{TextContent{Text: err.Error()}}),
				IsError: true,
			}, nil
		}
		res := &protocol.CallToolResult{
			Content: marshalContent(content),
		}
		return res, nil
	}
	return &Tool{
		Definition: protocol.Tool{
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		Handler: wrapped,
	}
}

// unmarshalSchema unmarshals data into v and validates the result according to
// the given schema.
func unmarshalSchema(data json.RawMessage, _ *jsonschema.Schema, v any) error {
	// TODO: use reflection to create the struct type to unmarshal into.
	// Separate validation from assignment.
	return json.Unmarshal(data, v)
}
