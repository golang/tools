// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"fmt"
	"reflect"
	"strings"
)

// A Schema is a JSON schema object.
//
// Right now, Schemas are only used for JSON serialization. In the future, they
// should support validation.
type Schema struct {
	Definitions          map[string]*Schema `json:"definitions"`
	Type                 any                `json:"type,omitempty"`
	Ref                  string             `json:"$ref,omitempty"`
	Description          string             `json:"description,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties any                `json:"additionalProperties,omitempty"`
}

// ForType constructs a JSON schema object for the given type argument.
//
// The type T must not contain (possibly recursively) any of the following Go
// types, as they are incompatible with the JSON schema spec.
//   - maps with key other than 'string'
//   - function types
//   - complex numbers
//   - unsafe pointers
//
// TODO(rfindley): we could perhaps just skip these incompatible fields.
func ForType[T any]() (*Schema, error) {
	return typeSchema(reflect.TypeFor[T](), make(map[reflect.Type]*Schema))
}

func typeSchema(t reflect.Type, seen map[reflect.Type]*Schema) (*Schema, error) {
	if s := seen[t]; s != nil {
		return s, nil
	}
	var (
		s   = new(Schema)
		err error
	)
	seen[t] = s

	switch t.Kind() {
	case reflect.Bool:
		s.Type = "boolean"

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s.Type = "integer"

	case reflect.Float32, reflect.Float64:
		s.Type = "number"

	case reflect.Interface:
		// Unrestricted

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unsupported map key type %v", t.Key().Kind())
		}
		s.Type = "object"
		valueSchema, err := typeSchema(t.Elem(), seen)
		if err != nil {
			return nil, fmt.Errorf("computing map value schema: %v", err)
		}
		s.AdditionalProperties = valueSchema

	case reflect.Pointer:
		s2, err := typeSchema(t.Elem(), seen)
		if err != nil {
			return nil, err
		}
		*s = *s2

	case reflect.Slice, reflect.Array:
		s.Type = "array"
		itemSchema, err := typeSchema(t.Elem(), seen)
		if err != nil {
			return nil, fmt.Errorf("computing element schema: %v", err)
		}
		s.Items = itemSchema

	case reflect.String:
		s.Type = "string"

	case reflect.Struct:
		s.Type = "object"
		s.AdditionalProperties = false

		for i := range t.NumField() {
			if s.Properties == nil {
				s.Properties = make(map[string]*Schema)
			}
			rfld := t.Field(i)
			name, ok := jsonName(rfld)
			if !ok {
				continue
			}
			s.Properties[name], err = typeSchema(rfld.Type, seen)
			if err != nil {
				return nil, err
			}
		}

	default:
		return nil, fmt.Errorf("type %v is unsupported by jsonschema", t.Kind())
	}
	return s, nil
}

func jsonName(f reflect.StructField) (string, bool) {
	j, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name, f.IsExported()
	}
	name, _, _ := strings.Cut(j, ",")
	if name == "" {
		return f.Name, f.IsExported()
	}
	return name, name != "" && name != "-"
}
