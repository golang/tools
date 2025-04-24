// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// The value of the "$schema" keyword for the version that we can validate.
const draft202012 = "https://json-schema.org/draft/2020-12/schema"

// Temporary definition of ResolvedSchema.
// The full definition deals with references between schemas, specifically the $id, $anchor and $ref keywords.
// We'll ignore that for now.
type ResolvedSchema struct {
	root *Schema
}

// Validate validates the instance, which must be a JSON value, against the schema.
// It returns nil if validation is successful or an error if it is not.
func (rs *ResolvedSchema) Validate(instance any) error {
	if s := rs.root.Schema; s != "" && s != draft202012 {
		return fmt.Errorf("cannot validate version %s, only %s", s, draft202012)
	}
	st := &state{rs: rs}
	return st.validate(reflect.ValueOf(instance), st.rs.root, nil)
}

// state is the state of single call to ResolvedSchema.Validate.
type state struct {
	rs    *ResolvedSchema
	depth int
}

// validate validates the reflected value of the instance.
// It keeps track of the path within the instance for better error messages.
func (st *state) validate(instance reflect.Value, schema *Schema, path []any) (err error) {
	defer func() {
		if err != nil {
			if p := formatPath(path); p != "" {
				err = fmt.Errorf("%s: %w", p, err)
			}
		}
	}()

	st.depth++
	defer func() { st.depth-- }()
	if st.depth >= 100 {
		return fmt.Errorf("max recursion depth of %d reached", st.depth)
	}

	// Treat the nil schema like the empty schema, as accepting everything.
	if schema == nil {
		return nil
	}

	// Step through interfaces.
	if instance.IsValid() && instance.Kind() == reflect.Interface {
		instance = instance.Elem()
	}

	// type: https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-01#section-6.1.1
	if schema.Type != "" || schema.Types != nil {
		gotType, ok := jsonType(instance)
		if !ok {
			return fmt.Errorf("%v of type %[1]T is not a valid JSON value", instance)
		}
		if schema.Type != "" {
			// "number" subsumes integers
			if !(gotType == schema.Type ||
				gotType == "integer" && schema.Type == "number") {
				return fmt.Errorf("type: %s has type %q, want %q", instance, gotType, schema.Type)
			}
		} else {
			if !(slices.Contains(schema.Types, gotType) || (gotType == "integer" && slices.Contains(schema.Types, "number"))) {
				return fmt.Errorf("type: %s has type %q, want one of %q",
					instance, gotType, strings.Join(schema.Types, ", "))
			}
		}
	}
	// enum: https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-01#section-6.1.2
	if schema.Enum != nil {
		ok := false
		for _, e := range schema.Enum {
			if equalValue(reflect.ValueOf(e), instance) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("enum: %v does not equal any of: %v", instance, schema.Enum)
		}
	}

	// const: https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-01#section-6.1.3
	if schema.Const != nil {
		if !equalValue(reflect.ValueOf(*schema.Const), instance) {
			return fmt.Errorf("const: %v does not equal %v", instance, *schema.Const)
		}
	}
	return nil
}

func formatPath(path []any) string {
	var b strings.Builder
	for i, p := range path {
		if n, ok := p.(int); ok {
			fmt.Fprintf(&b, "[%d]", n)
		} else {
			if i > 0 {
				b.WriteByte('.')
			}
			fmt.Fprintf(&b, "%q", p)
		}
	}
	return b.String()
}
