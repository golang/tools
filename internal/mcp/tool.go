// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"golang.org/x/tools/internal/mcp/jsonschema"
)

// A ToolHandler handles a call to tools/call.
type ToolHandler[TArgs any] func(context.Context, *ServerSession, *CallToolParams[TArgs]) (*CallToolResult, error)

// A Tool is a tool definition that is bound to a tool handler.
type ServerTool struct {
	Tool    *Tool
	Handler ToolHandler[json.RawMessage]
}

// NewTool is a helper to make a tool using reflection on the given handler.
//
// If provided, variadic [ToolOption] values may be used to customize the tool.
//
// The input schema for the tool is extracted from the request type for the
// handler, and used to unmmarshal and validate requests to the handler. This
// schema may be customized using the [Input] option.
//
// The handler request type must translate to a valid schema, as documented by
// [jsonschema.ForType]; otherwise, NewTool panics.
//
// TODO: just have the handler return a CallToolResult: returning []Content is
// going to be inconsistent with other server features.
func NewTool[TReq any](name, description string, handler ToolHandler[TReq], opts ...ToolOption) *ServerTool {
	schema, err := jsonschema.For[TReq]()
	if err != nil {
		panic(err)
	}
	// We must resolve the schema after the ToolOptions have had a chance to update it.
	// But the handler needs access to the resolved schema, and the options may change
	// the handler too.
	// The best we can do is use the resolved schema in our own wrapped handler,
	// and hope that no ToolOption replaces it.
	// TODO(jba): at a minimum, document this.
	var resolved *jsonschema.Resolved
	wrapped := func(ctx context.Context, cc *ServerSession, params *CallToolParams[json.RawMessage]) (*CallToolResult, error) {
		var params2 CallToolParams[TReq]
		if params.Arguments != nil {
			if err := unmarshalSchema(params.Arguments, resolved, &params2.Arguments); err != nil {
				return nil, err
			}
		}
		res, err := handler(ctx, cc, &params2)
		// TODO: investigate why server errors are embedded in this strange way,
		// rather than returned as jsonrpc2 server errors.
		if err != nil {
			return &CallToolResult{
				Content: []*Content{NewTextContent(err.Error())},
				IsError: true,
			}, nil
		}
		return res, nil
	}
	t := &ServerTool{
		Tool: &Tool{
			Name:        name,
			Description: description,
			InputSchema: schema,
		},
		Handler: wrapped,
	}
	for _, opt := range opts {
		opt.set(t)
	}
	if schema := t.Tool.InputSchema; schema != nil {
		// Resolve the schema, with no base URI. We don't expect tool schemas to
		// refer outside of themselves.
		resolved, err = schema.Resolve(nil)
		if err != nil {
			panic(fmt.Errorf("resolving input schema %s: %w", schemaJSON(schema), err))
		}
	}
	return t
}

// unmarshalSchema unmarshals data into v and validates the result according to
// the given resolved schema.
func unmarshalSchema(data json.RawMessage, resolved *jsonschema.Resolved, v any) error {
	// TODO: use reflection to create the struct type to unmarshal into.
	// Separate validation from assignment.

	// Disallow unknown fields.
	// Otherwise, if the tool was built with a struct, the client could send extra
	// fields and json.Unmarshal would ignore them, so the schema would never get
	// a chance to declare the extra args invalid.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("unmarshaling: %w", err)
	}
	if resolved != nil {
		if err := resolved.Validate(v); err != nil {
			return fmt.Errorf("validating\n\t%s\nagainst\n\t %s:\n %w", data, schemaJSON(resolved.Schema()), err)
		}
	}
	return nil
}

// A ToolOption configures the behavior of a Tool.
type ToolOption interface {
	set(*ServerTool)
}

type toolSetter func(*ServerTool)

func (s toolSetter) set(t *ServerTool) { s(t) }

// Input applies the provided [SchemaOption] configuration to the tool's input
// schema.
func Input(opts ...SchemaOption) ToolOption {
	return toolSetter(func(t *ServerTool) {
		for _, opt := range opts {
			opt.set(t.Tool.InputSchema)
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

// schemaJSON returns the JSON value for s as a string, or a string indicating an error.
func schemaJSON(s *jsonschema.Schema) string {
	m, err := json.Marshal(s)
	if err != nil {
		return fmt.Sprintf("<!%s>", err)
	}
	return string(m)
}
