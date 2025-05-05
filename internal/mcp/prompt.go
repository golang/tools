// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"golang.org/x/tools/internal/mcp/internal/protocol"
	"golang.org/x/tools/internal/mcp/internal/util"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

// A PromptHandler handles a call to prompts/get.
type PromptHandler func(context.Context, *ClientConnection, map[string]string) (*protocol.GetPromptResult, error)

// A Prompt is a prompt definition bound to a prompt handler.
type Prompt struct {
	Definition protocol.Prompt
	Handler    PromptHandler
}

// MakePrompt is a helper to use reflection to create a prompt for the given
// handler.
//
// The arguments for the prompt are extracted from the request type for the
// handler. The handler request type must be a struct consisting only of fields
// of type string or *string. The argument names for the resulting prompt
// definition correspond to the JSON names of the request fields, and any
// fields that are not marked "omitempty" are considered required.
func MakePrompt[TReq any](name, description string, handler func(context.Context, *ClientConnection, TReq) (*protocol.GetPromptResult, error), opts ...PromptOption) *Prompt {
	schema, err := jsonschema.For[TReq]()
	if err != nil {
		panic(err)
	}
	if schema.Type != "object" || !reflect.DeepEqual(schema.AdditionalProperties, &jsonschema.Schema{Not: &jsonschema.Schema{}}) {
		panic(fmt.Sprintf("handler request type must be a struct"))
	}
	prompt := &Prompt{
		Definition: protocol.Prompt{
			Name:        name,
			Description: description,
		},
	}
	required := make(map[string]bool)
	for _, p := range schema.Required {
		required[p] = true
	}
	for name, prop := range util.Sorted(schema.Properties) {
		if prop.Type != "string" {
			panic(fmt.Sprintf("handler type must consist only of string fields"))
		}
		prompt.Definition.Arguments = append(prompt.Definition.Arguments, protocol.PromptArgument{
			Name:        name,
			Description: prop.Description,
			Required:    required[name],
		})
	}
	prompt.Handler = func(ctx context.Context, cc *ClientConnection, args map[string]string) (*protocol.GetPromptResult, error) {
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
		return handler(ctx, cc, v)
	}
	for _, opt := range opts {
		opt.set(prompt)
	}
	return prompt
}

// A PromptOption configures the behavior of a Prompt.
type PromptOption interface {
	set(*Prompt)
}

type promptSetter func(*Prompt)

func (s promptSetter) set(p *Prompt) { s(p) }

// Argument configures the 'schema' of a prompt argument.
// If the argument does not exist, it is added.
//
// Since prompt arguments are not a full JSON schema, Argument only accepts
// Required and Description, and panics when encountering any other option.
func Argument(name string, opts ...SchemaOption) PromptOption {
	return promptSetter(func(p *Prompt) {
		i := slices.IndexFunc(p.Definition.Arguments, func(arg protocol.PromptArgument) bool {
			return arg.Name == name
		})
		var arg protocol.PromptArgument
		if i < 0 {
			i = len(p.Definition.Arguments)
			arg = protocol.PromptArgument{Name: name}
			p.Definition.Arguments = append(p.Definition.Arguments, arg)
		} else {
			arg = p.Definition.Arguments[i]
		}
		for _, opt := range opts {
			switch v := opt.(type) {
			case required:
				arg.Required = bool(v)
			case description:
				arg.Description = string(v)
			default:
				panic(fmt.Sprintf("unsupported prompt argument schema option %T", opt))
			}
		}
		p.Definition.Arguments[i] = arg
	})
}
