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
// [CallToolParams.Arguments] will contain a map[string]any that has been validated
// against the input schema.
// Perhaps this should be an alias for ToolHandlerFor[map[string]any, map[string]any].
type ToolHandler func(context.Context, *ServerSession, *CallToolParamsFor[map[string]any]) (*CallToolResult, error)

// A ToolHandlerFor handles a call to tools/call with typed arguments and results.
type ToolHandlerFor[In, Out any] func(context.Context, *ServerSession, *CallToolParamsFor[In]) (*CallToolResultFor[Out], error)

// A rawToolHandler is like a ToolHandler, but takes the arguments as json.RawMessage.
type rawToolHandler = func(context.Context, *ServerSession, *CallToolParamsFor[json.RawMessage]) (*CallToolResult, error)

// A Tool is a tool definition that is bound to a tool handler.
type ServerTool struct {
	Tool    *Tool
	Handler ToolHandler
	// Set in NewServerTool or Server.AddToolsErr.
	rawHandler rawToolHandler
	// Resolved tool schemas. Set in Server.AddToolsErr.
	// TODO(rfindley): re-enable schema validation. For now, it is causing breakage in Google.
	// inputResolved, outputResolved *jsonschema.Resolved
}

// NewServerTool is a helper to make a tool using reflection on the given type parameters.
// When the tool is called, CallToolParams.Arguments will be of type In.
//
// If provided, variadic [ToolOption] values may be used to customize the tool.
//
// The input schema for the tool is extracted from the request type for the
// handler, and used to unmmarshal and validate requests to the handler. This
// schema may be customized using the [Input] option.
func NewServerTool[In, Out any](name, description string, handler ToolHandlerFor[In, Out], opts ...ToolOption) *ServerTool {
	st, err := newServerToolErr[In, Out](name, description, handler, opts...)
	if err != nil {
		panic(fmt.Errorf("NewServerTool(%q): %w", name, err))
	}
	return st
}

func newServerToolErr[In, Out any](name, description string, handler ToolHandlerFor[In, Out], opts ...ToolOption) (*ServerTool, error) {
	// TODO: check that In is a struct.
	ischema, err := jsonschema.For[In]()
	if err != nil {
		return nil, err
	}
	// TODO: uncomment when output schemas drop.
	// oschema, err := jsonschema.For[TRes]()
	// if err != nil {
	// 	return nil, err
	// }

	t := &ServerTool{
		Tool: &Tool{
			Name:        name,
			Description: description,
			InputSchema: ischema,
			// OutputSchema: oschema,
		},
	}
	for _, opt := range opts {
		opt.set(t)
	}

	t.rawHandler = func(ctx context.Context, ss *ServerSession, rparams *CallToolParamsFor[json.RawMessage]) (*CallToolResult, error) {
		var args In
		if rparams.Arguments != nil {
			// TODO(rfindley): re-enable schema validation. See note in [ServerTool].
			if err := unmarshalSchema(rparams.Arguments, nil, &args); err != nil {
				return nil, err
			}
		}
		// TODO(jba): future-proof this copy.
		params := &CallToolParamsFor[In]{
			Meta:      rparams.Meta,
			Name:      rparams.Name,
			Arguments: args,
		}
		res, err := handler(ctx, ss, params)
		if err != nil {
			return nil, err
		}

		var ctr CallToolResult
		if res != nil {
			// TODO(jba): future-proof this copy.
			ctr.Meta = res.Meta
			ctr.Content = res.Content
			ctr.IsError = res.IsError
		}
		return &ctr, nil
	}
	return t, nil
}

// newRawHandler creates a rawToolHandler for tools not created through NewServerTool.
// It unmarshals the arguments into a map[string]any and validates them against the
// schema, then calls the ServerTool's handler.
func newRawHandler(st *ServerTool) rawToolHandler {
	if st.Handler == nil {
		panic("st.Handler is nil")
	}
	return func(ctx context.Context, ss *ServerSession, rparams *CallToolParamsFor[json.RawMessage]) (*CallToolResult, error) {
		// Unmarshal the args into what should be a map.
		var args map[string]any
		if rparams.Arguments != nil {
			// TODO(rfindley): re-enable schema validation. See note in [ServerTool].
			if err := unmarshalSchema(rparams.Arguments, nil, &args); err != nil {
				return nil, err
			}
		}
		// TODO: generate copy
		params := &CallToolParamsFor[map[string]any]{
			Meta:      rparams.Meta,
			Name:      rparams.Name,
			Arguments: args,
		}
		res, err := st.Handler(ctx, ss, params)
		// TODO(rfindley): investigate why server errors are embedded in this strange way,
		// rather than returned as jsonrpc2 server errors.
		if err != nil {
			return &CallToolResult{
				Content: []*Content{NewTextContent(err.Error())},
				IsError: true,
			}, nil
		}
		return res, nil
	}
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
	// TODO: test with nil args.
	if resolved != nil {
		if err := resolved.ApplyDefaults(v); err != nil {
			return fmt.Errorf("applying defaults from \n\t%s\nto\n\t%s:\n%w", schemaJSON(resolved.Schema()), data, err)
		}
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
