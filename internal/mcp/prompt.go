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

	"golang.org/x/tools/internal/mcp/internal/util"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

// A PromptHandler handles a call to prompts/get.
type PromptHandler func(context.Context, *ServerSession, *GetPromptParams) (*GetPromptResult, error)

// A Prompt is a prompt definition bound to a prompt handler.
type ServerPrompt struct {
	Prompt  *Prompt
	Handler PromptHandler
}

// NewPrompt is a helper that uses reflection to create a prompt for the given handler.
//
// The arguments for the prompt are extracted from the request type for the
// handler. The handler request type must be a struct consisting only of fields
// of type string or *string. The argument names for the resulting prompt
// definition correspond to the JSON names of the request fields, and any
// fields that are not marked "omitempty" are considered required.
//
// The handler is passed [GetPromptParams] so it can have access to prompt parameters other than name and arguments.
// At present, there are no such parameters.
func NewPrompt[TReq any](name, description string, handler func(context.Context, *ServerSession, TReq, *GetPromptParams) (*GetPromptResult, error), opts ...PromptOption) *ServerPrompt {
	schema, err := jsonschema.For[TReq]()
	if err != nil {
		panic(err)
	}
	if schema.Type != "object" || !reflect.DeepEqual(schema.AdditionalProperties, &jsonschema.Schema{Not: &jsonschema.Schema{}}) {
		panic(fmt.Sprintf("handler request type must be a struct"))
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		panic(err)
	}
	prompt := &ServerPrompt{
		Prompt: &Prompt{
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
		prompt.Prompt.Arguments = append(prompt.Prompt.Arguments, &PromptArgument{
			Name:        name,
			Description: prop.Description,
			Required:    required[name],
		})
	}
	prompt.Handler = func(ctx context.Context, ss *ServerSession, params *GetPromptParams) (*GetPromptResult, error) {
		// For simplicity, just marshal and unmarshal the arguments.
		// This could be avoided in the future.
		rawArgs, err := json.Marshal(params.Arguments)
		if err != nil {
			return nil, err
		}
		var v TReq
		if err := unmarshalSchema(rawArgs, resolved, &v); err != nil {
			return nil, err
		}
		return handler(ctx, ss, v, params)
	}
	for _, opt := range opts {
		opt.set(prompt)
	}
	return prompt
}

// A PromptOption configures the behavior of a Prompt.
type PromptOption interface {
	set(*ServerPrompt)
}

type promptSetter func(*ServerPrompt)

func (s promptSetter) set(p *ServerPrompt) { s(p) }

// Argument configures the 'schema' of a prompt argument.
// If the argument does not exist, it is added.
//
// Since prompt arguments are not a full JSON schema, Argument only accepts
// Required and Description, and panics when encountering any other option.
func Argument(name string, opts ...SchemaOption) PromptOption {
	return promptSetter(func(p *ServerPrompt) {
		i := slices.IndexFunc(p.Prompt.Arguments, func(arg *PromptArgument) bool {
			return arg.Name == name
		})
		var arg *PromptArgument
		if i < 0 {
			i = len(p.Prompt.Arguments)
			arg = &PromptArgument{Name: name}
			p.Prompt.Arguments = append(p.Prompt.Arguments, arg)
		} else {
			arg = p.Prompt.Arguments[i]
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
		p.Prompt.Arguments[i] = arg
	})
}
