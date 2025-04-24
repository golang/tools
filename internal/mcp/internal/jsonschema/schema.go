// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package jsonschema is an implementation of the JSON Schema
// specification: https://json-schema.org.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// A Schema is a JSON schema object.
// It corresponds to the 2020-12 draft, as described in
// https://json-schema.org/draft/2020-12.
type Schema struct {
	// core
	ID      string             `json:"$id,omitempty"`
	Schema  string             `json:"$schema,omitempty"`
	Ref     string             `json:"$ref,omitempty"`
	Comment string             `json:"$comment,omitempty"`
	Defs    map[string]*Schema `json:"$defs,omitempty"`
	// definitions is deprecated but still allowed. It is a synonym for defs.
	Definitions map[string]*Schema `json:"definitions,omitempty"`

	Anchor        string          `json:"$anchor,omitempty"`
	DynamicAnchor string          `json:"$dynamicAnchor,omitempty"`
	DynamicRef    string          `json:"$dynamicRef,omitempty"`
	Vocabulary    map[string]bool `json:"$vocabulary,omitempty"`

	// metadata
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`

	// validation
	// Use Type for a single type, or Types for multiple types; never both.
	Type  string   `json:"-"`
	Types []string `json:"-"`
	Enum  []any    `json:"enum,omitempty"`
	// Const is *any because a JSON null (Go nil) is a valid value.
	Const            *any     `json:"const,omitempty"`
	MultipleOf       *float64 `json:"multipleOf,omitempty"`
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	ExclusiveMaximum *float64 `json:"exclusiveMaximum,omitempty"`
	MinLength        *float64 `json:"minLength,omitempty"`
	MaxLength        *float64 `json:"maxLength,omitempty"`
	Pattern          string   `json:"pattern,omitempty"`

	// arrays
	PrefixItems      []*Schema `json:"prefixItems,omitempty"`
	Items            *Schema   `json:"items,omitempty"`
	MinItems         *float64  `json:"minItems,omitempty"`
	MaxItems         *float64  `json:"maxItems,omitempty"`
	AdditionalItems  *Schema   `json:"additionalItems,omitempty"`
	UniqueItems      bool      `json:"uniqueItems,omitempty"`
	Contains         *Schema   `json:"contains,omitempty"`
	MinContains      *float64  `json:"minContains,omitempty"`
	MaxContains      *float64  `json:"maxContains,omitempty"`
	UnevaluatedItems *Schema   `json:"unevaluatedItems,omitempty"`

	// objects
	MinProperties         *float64            `json:"minProperties,omitempty"`
	MaxProperties         *float64            `json:"maxProperties,omitempty"`
	Required              []string            `json:"required,omitempty"`
	DependentRequired     map[string][]string `json:"dependentRequired,omitempty"`
	Properties            map[string]*Schema  `json:"properties,omitempty"`
	PatternProperties     map[string]*Schema  `json:"patternProperties,omitempty"`
	AdditionalProperties  *Schema             `json:"additionalProperties,omitempty"`
	PropertyNames         *Schema             `json:"propertyNames,omitempty"`
	UnevaluatedProperties *Schema             `json:"unevaluatedProperties,omitempty"`

	// logic
	AllOf []*Schema `json:"allOf,omitempty"`
	AnyOf []*Schema `json:"anyOf,omitempty"`
	OneOf []*Schema `json:"oneOf,omitempty"`
	Not   *Schema   `json:"not,omitempty"`

	// conditional
	If               *Schema            `json:"if,omitempty"`
	Then             *Schema            `json:"then,omitempty"`
	Else             *Schema            `json:"else,omitempty"`
	DependentSchemas map[string]*Schema `json:"dependentSchemas,omitempty"`
}

// String returns a short description of the schema.
func (s *Schema) String() string {
	if s.ID != "" {
		return s.ID
	}
	// TODO: return something better, like a JSON Pointer from the base.
	return "<anonymous schema>"
}

// json returns the schema in json format.
func (s *Schema) json() string {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Sprintf("<jsonschema.Schema:%v>", err)
	}
	return string(data)
}

func (s *Schema) basicChecks() error {
	if s.Type != "" && s.Types != nil {
		return errors.New("both Type and Types are set; at most one should be")
	}
	if s.Defs != nil && s.Definitions != nil {
		return errors.New("both Defs and Definitions are set; at most one should be")
	}
	return nil
}

type schemaWithoutMethods Schema // doesn't implement json.{Unm,M}arshaler

func (s *Schema) MarshalJSON() ([]byte, error) {
	if err := s.basicChecks(); err != nil {
		return nil, err
	}
	// Marshal either Type or Types as "type".
	var typ any
	switch {
	case s.Type != "":
		typ = s.Type
	case s.Types != nil:
		typ = s.Types
	}
	ms := struct {
		Type any `json:"type,omitempty"`
		*schemaWithoutMethods
	}{
		Type:                 typ,
		schemaWithoutMethods: (*schemaWithoutMethods)(s),
	}
	return json.Marshal(ms)
}

func (s *Schema) UnmarshalJSON(data []byte) error {
	// A JSON boolean is a valid schema.
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			// true is the empty schema, which validates everything.
			*s = Schema{}
		} else {
			// false is the schema that validates nothing.
			*s = Schema{Not: &Schema{}}
		}
		return nil
	}

	ms := struct {
		Type  json.RawMessage `json:"type,omitempty"`
		Const json.RawMessage `json:"const,omitempty"`
		*schemaWithoutMethods
	}{
		schemaWithoutMethods: (*schemaWithoutMethods)(s),
	}
	if err := json.Unmarshal(data, &ms); err != nil {
		return err
	}
	// Unmarshal "type" as either Type or Types.
	var err error
	if len(ms.Type) > 0 {
		switch ms.Type[0] {
		case '"':
			err = json.Unmarshal(ms.Type, &s.Type)
		case '[':
			err = json.Unmarshal(ms.Type, &s.Types)
		default:
			err = fmt.Errorf("invalid type: %q", ms.Type)
		}
	}
	if err != nil {
		return err
	}
	// Setting Const to a pointer to null will marshal properly, but won't unmarshal:
	// the *any is set to nil, not a pointer to nil.
	if len(ms.Const) > 0 {
		if bytes.Equal(ms.Const, []byte("null")) {
			s.Const = new(any)
		} else if err := json.Unmarshal(ms.Const, &s.Const); err != nil {
			return err
		}
	}
	return nil
}

// Ptr returns a pointer to a new variable whose value is x.
func Ptr[T any](x T) *T { return &x }
