// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"slices"

	"golang.org/x/tools/internal/mcp/internal/protocol"
	"golang.org/x/tools/internal/mcp/internal/util"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

// A ToolHandler handles a call to tools/call.
type ToolHandler func(context.Context, *ClientConnection, map[string]json.RawMessage) (*protocol.CallToolResult, error)

// A Tool is a tool definition that is bound to a tool handler.
type Tool struct {
	Definition protocol.Tool
	Handler    ToolHandler
}

// MakeTool is a helper to make a tool using reflection on the given handler.
//
// If provided, variadic [ToolOption] values may be used to customize the tool.
//
// The input schema for the tool is extracted from the request type for the
// handler, and used to unmmarshal and validate requests to the handler. This
// schema may be customized using the [Input] option.
//
// The handler request type must translate to a valid schema, as documented by
// [jsonschema.ForType]; otherwise, MakeTool panics.
//
// TODO: just have the handler return a CallToolResult: returning []Content is
// going to be inconsistent with other server features.
func MakeTool[TReq any](name, description string, handler func(context.Context, *ClientConnection, TReq) ([]Content, error), opts ...ToolOption) *Tool {
	schema, err := jsonschema.For[TReq]()
	if err != nil {
		panic(err)
	}
	wrapped := func(ctx context.Context, cc *ClientConnection, args map[string]json.RawMessage) (*protocol.CallToolResult, error) {
		// For simplicity, just marshal and unmarshal the arguments.
		// This could be avoided in the future.
		rawArgs, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		var v TReq
		if err := unmarshalSchema(rawArgs, schema, &v); err != nil {
			return nil, err
		}
		content, err := handler(ctx, cc, v)
		// TODO: investigate why server errors are embedded in this strange way,
		// rather than returned as jsonrpc2 server errors.
		if err != nil {
			return &protocol.CallToolResult{
				Content: []protocol.Content{TextContent{Text: err.Error()}.ToWire()},
				IsError: true,
			}, nil
		}
		res := &protocol.CallToolResult{
			Content: util.Apply(content, Content.ToWire),
		}
		return res, nil
	}
	t := &Tool{
		Definition: protocol.Tool{
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		Handler: wrapped,
	}
	for _, opt := range opts {
		opt.set(t)
	}
	return t
}

// unmarshalSchema unmarshals data into v and validates the result according to
// the given schema.
func unmarshalSchema(data json.RawMessage, _ *jsonschema.Schema, v any) error {
	// TODO: use reflection to create the struct type to unmarshal into.
	// Separate validation from assignment.
	return json.Unmarshal(data, v)
}

// A ToolOption configures the behavior of a Tool.
type ToolOption interface {
	set(*Tool)
}

type toolSetter func(*Tool)

func (s toolSetter) set(t *Tool) { s(t) }

// Input applies the provided [SchemaOption] configuration to the tool's input
// schema.
func Input(opts ...SchemaOption) ToolOption {
	return toolSetter(func(t *Tool) {
		for _, opt := range opts {
			opt.set(t.Definition.InputSchema)
		}
	})
}

// A SchemaOption configures a jsonschema.Schema.
type SchemaOption interface {
	set(s *jsonschema.Schema)
}

type schemaSetter func(*jsonschema.Schema)

func (s schemaSetter) set(schema *jsonschema.Schema) { s(schema) }

// Property configures the schema for the property of the given name.
// If there is no such property in the schema, it is created.
func Property(name string, opts ...SchemaOption) SchemaOption {
	return schemaSetter(func(schema *jsonschema.Schema) {
		propSchema, ok := schema.Properties[name]
		if !ok {
			propSchema = new(jsonschema.Schema)
			schema.Properties[name] = propSchema
		}
		// Apply the options, with special handling for Required, as it needs to be
		// set on the parent schema.
		for _, opt := range opts {
			if req, ok := opt.(required); ok {
				if req {
					if !slices.Contains(schema.Required, name) {
						schema.Required = append(schema.Required, name)
					}
				} else {
					schema.Required = slices.DeleteFunc(schema.Required, func(s string) bool {
						return s == name
					})
				}
			} else {
				opt.set(propSchema)
			}
		}
	})
}

// Required sets whether the associated property is required. It is only valid
// when used in a [Property] option: using Required outside of Property panics.
func Required(v bool) SchemaOption {
	return required(v)
}

// required must be a distinguished type as it needs special handling to mutate
// the parent schema, and to mutate prompt arguments.
type required bool

func (required) set(s *jsonschema.Schema) {
	panic("use of required outside of Property")
}

// Enum sets the provided values as the "enum" value of the schema.
func Enum(values ...any) SchemaOption {
	return schemaSetter(func(s *jsonschema.Schema) {
		s.Enum = values
	})
}

// Description sets the provided schema description.
func Description(desc string) SchemaOption {
	return description(desc)
}

// description must be a distinguished type so that it can be handled by prompt
// options.
type description string

func (d description) set(s *jsonschema.Schema) {
	s.Description = string(d)
}

// Schema overrides the inferred schema with a shallow copy of the given
// schema.
func Schema(schema *jsonschema.Schema) SchemaOption {
	return schemaSetter(func(s *jsonschema.Schema) {
		*s = *schema
	})
}
