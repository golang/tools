// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"fmt"
	"hash/maphash"
	"math"
	"math/big"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// The value of the "$schema" keyword for the version that we can validate.
const draft202012 = "https://json-schema.org/draft/2020-12/schema"

// Validate validates the instance, which must be a JSON value, against the schema.
// It returns nil if validation is successful or an error if it is not.
func (rs *Resolved) Validate(instance any) error {
	if s := rs.root.Schema; s != "" && s != draft202012 {
		return fmt.Errorf("cannot validate version %s, only %s", s, draft202012)
	}
	st := &state{rs: rs}
	var pathBuffer [4]any
	return st.validate(reflect.ValueOf(instance), st.rs.root, nil, pathBuffer[:0])
}

// state is the state of single call to ResolvedSchema.Validate.
type state struct {
	rs    *Resolved
	depth int
}

// validate validates the reflected value of the instance.
// It keeps track of the path within the instance for better error messages.
func (st *state) validate(instance reflect.Value, schema *Schema, callerAnns *annotations, path []any) (err error) {
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

	// We checked for nil schemas in [Schema.Resolve].
	assert(schema != nil, "nil schema")

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
				return fmt.Errorf("type: %v has type %q, want %q", instance, gotType, schema.Type)
			}
		} else {
			if !(slices.Contains(schema.Types, gotType) || (gotType == "integer" && slices.Contains(schema.Types, "number"))) {
				return fmt.Errorf("type: %v has type %q, want one of %q",
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

	// numbers: https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-01#section-6.2
	if schema.MultipleOf != nil || schema.Minimum != nil || schema.Maximum != nil || schema.ExclusiveMinimum != nil || schema.ExclusiveMaximum != nil {
		n, ok := jsonNumber(instance)
		if ok { // these keywords don't apply to non-numbers
			if schema.MultipleOf != nil {
				// TODO: validate MultipleOf as non-zero.
				// The test suite assumes floats.
				nf, _ := n.Float64() // don't care if it's exact or not
				if _, f := math.Modf(nf / *schema.MultipleOf); f != 0 {
					return fmt.Errorf("multipleOf: %s is not a multiple of %f", n, *schema.MultipleOf)
				}
			}

			m := new(big.Rat) // reuse for all of the following
			cmp := func(f float64) int { return n.Cmp(m.SetFloat64(f)) }

			if schema.Minimum != nil && cmp(*schema.Minimum) < 0 {
				return fmt.Errorf("minimum: %s is less than %f", n, *schema.Minimum)
			}
			if schema.Maximum != nil && cmp(*schema.Maximum) > 0 {
				return fmt.Errorf("maximum: %s is greater than %f", n, *schema.Maximum)
			}
			if schema.ExclusiveMinimum != nil && cmp(*schema.ExclusiveMinimum) <= 0 {
				return fmt.Errorf("exclusiveMinimum: %s is less than or equal to %f", n, *schema.ExclusiveMinimum)
			}
			if schema.ExclusiveMaximum != nil && cmp(*schema.ExclusiveMaximum) >= 0 {
				return fmt.Errorf("exclusiveMaximum: %s is greater than or equal to %f", n, *schema.ExclusiveMaximum)
			}
		}
	}

	// strings: https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-01#section-6.3
	if instance.Kind() == reflect.String && (schema.MinLength != nil || schema.MaxLength != nil || schema.Pattern != "") {
		str := instance.String()
		n := utf8.RuneCountInString(str)
		if schema.MinLength != nil {
			if m := *schema.MinLength; n < m {
				return fmt.Errorf("minLength: %q contains %d Unicode code points, fewer than %d", str, n, m)
			}
		}
		if schema.MaxLength != nil {
			if m := *schema.MaxLength; n > m {
				return fmt.Errorf("maxLength: %q contains %d Unicode code points, more than %d", str, n, m)
			}
		}

		if schema.Pattern != "" && !schema.pattern.MatchString(str) {
			return fmt.Errorf("pattern: %q does not match regular expression %q", str, schema.Pattern)
		}
	}

	// logic
	// https://json-schema.org/draft/2020-12/json-schema-core#section-10.2
	// These must happen before arrays and objects because if they evaluate an item or property,
	// then the unevaluatedItems/Properties schemas don't apply to it.
	// See https://json-schema.org/draft/2020-12/json-schema-core#section-11.2, paragraph 4.
	//
	// If any of these fail, then validation fails, even if there is an unevaluatedXXX
	// keyword in the schema. The spec is unclear about this, but that is the intention.

	var anns annotations // all the annotations for this call and child calls

	valid := func(s *Schema, anns *annotations) bool { return st.validate(instance, s, anns, path) == nil }

	if schema.AllOf != nil {
		for _, ss := range schema.AllOf {
			if err := st.validate(instance, ss, &anns, path); err != nil {
				return err
			}
		}
	}
	if schema.AnyOf != nil {
		// We must visit them all, to collect annotations.
		ok := false
		for _, ss := range schema.AnyOf {
			if valid(ss, &anns) {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("anyOf: did not validate against any of %v", schema.AnyOf)
		}
	}
	if schema.OneOf != nil {
		// Exactly one.
		var okSchema *Schema
		for _, ss := range schema.OneOf {
			if valid(ss, &anns) {
				if okSchema != nil {
					return fmt.Errorf("oneOf: validated against both %v and %v", okSchema, ss)
				}
				okSchema = ss
			}
		}
		if okSchema == nil {
			return fmt.Errorf("oneOf: did not validate against any of %v", schema.OneOf)
		}
	}
	if schema.Not != nil {
		// Ignore annotations from "not".
		if valid(schema.Not, nil) {
			return fmt.Errorf("not: validated against %v", schema.Not)
		}
	}
	if schema.If != nil {
		var ss *Schema
		if valid(schema.If, &anns) {
			ss = schema.Then
		} else {
			ss = schema.Else
		}
		if ss != nil {
			if err := st.validate(instance, ss, &anns, path); err != nil {
				return err
			}
		}
	}

	// arrays
	if instance.Kind() == reflect.Array || instance.Kind() == reflect.Slice {
		// https://json-schema.org/draft/2020-12/json-schema-core#section-10.3.1
		// This validate call doesn't collect annotations for the items of the instance; they are separate
		// instances in their own right.
		// TODO(jba): if the test suite doesn't cover this case, add a test. For example, nested arrays.
		for i, ischema := range schema.PrefixItems {
			if i >= instance.Len() {
				break // shorter is OK
			}
			if err := st.validate(instance.Index(i), ischema, nil, append(path, i)); err != nil {
				return err
			}
		}
		anns.noteEndIndex(min(len(schema.PrefixItems), instance.Len()))

		if schema.Items != nil {
			for i := len(schema.PrefixItems); i < instance.Len(); i++ {
				if err := st.validate(instance.Index(i), schema.Items, nil, append(path, i)); err != nil {
					return err
				}
			}
			// Note that all the items in this array have been validated.
			anns.allItems = true
		}

		nContains := 0
		if schema.Contains != nil {
			for i := range instance.Len() {
				if err := st.validate(instance.Index(i), schema.Contains, nil, append(path, i)); err == nil {
					nContains++
					anns.noteIndex(i)
				}
			}
			if nContains == 0 && (schema.MinContains == nil || *schema.MinContains > 0) {
				return fmt.Errorf("contains: %s does not have an item matching %s",
					instance, schema.Contains)
			}
		}

		// https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-01#section-6.4
		// TODO(jba): check that these next four keywords' values are integers.
		if schema.MinContains != nil && schema.Contains != nil {
			if m := *schema.MinContains; nContains < m {
				return fmt.Errorf("minContains: contains validated %d items, less than %d", nContains, m)
			}
		}
		if schema.MaxContains != nil && schema.Contains != nil {
			if m := *schema.MaxContains; nContains > m {
				return fmt.Errorf("maxContains: contains validated %d items, greater than %d", nContains, m)
			}
		}
		if schema.MinItems != nil {
			if m := *schema.MinItems; instance.Len() < m {
				return fmt.Errorf("minItems: array length %d is less than %d", instance.Len(), m)
			}
		}
		if schema.MaxItems != nil {
			if m := *schema.MaxItems; instance.Len() > m {
				return fmt.Errorf("maxItems: array length %d is greater than %d", instance.Len(), m)
			}
		}
		if schema.UniqueItems {
			if instance.Len() > 1 {
				// Hash each item and compare the hashes.
				// If two hashes differ, the items differ.
				// If two hashes are the same, compare the collisions for equality.
				// (The same logic as hash table lookup.)
				// TODO(jba): Use container/hash.Map when it becomes available (https://go.dev/issue/69559),
				hashes := map[uint64][]int{} // from hash to indices
				seed := maphash.MakeSeed()
				for i := range instance.Len() {
					item := instance.Index(i)
					var h maphash.Hash
					h.SetSeed(seed)
					hashValue(&h, item)
					hv := h.Sum64()
					if sames := hashes[hv]; len(sames) > 0 {
						for _, j := range sames {
							if equalValue(item, instance.Index(j)) {
								return fmt.Errorf("uniqueItems: array items %d and %d are equal", i, j)
							}
						}
					}
					hashes[hv] = append(hashes[hv], i)
				}
			}
		}

		// https://json-schema.org/draft/2020-12/json-schema-core#section-11.2
		if schema.UnevaluatedItems != nil && !anns.allItems {
			// Apply this subschema to all items in the array that haven't been successfully validated.
			// That includes validations by subschemas on the same instance, like allOf.
			for i := anns.endIndex; i < instance.Len(); i++ {
				if !anns.evaluatedIndexes[i] {
					if err := st.validate(instance.Index(i), schema.UnevaluatedItems, nil, append(path, i)); err != nil {
						return err
					}
				}
			}
			anns.allItems = true
		}
	}

	// objects
	// https://json-schema.org/draft/2020-12/json-schema-core#section-10.3.2
	if instance.Kind() == reflect.Map {
		if kt := instance.Type().Key(); kt.Kind() != reflect.String {
			return fmt.Errorf("map key type %s is not a string", kt)
		}
		// Track the evaluated properties for just this schema, to support additionalProperties.
		// If we used anns here, then we'd be including properties evaluated in subschemas
		// from allOf, etc., which additionalProperties shouldn't observe.
		evalProps := map[string]bool{}
		for prop, schema := range schema.Properties {
			val := instance.MapIndex(reflect.ValueOf(prop))
			if !val.IsValid() {
				// It's OK if the instance doesn't have the property.
				continue
			}
			if err := st.validate(val, schema, nil, append(path, prop)); err != nil {
				return err
			}
			evalProps[prop] = true
		}
		if len(schema.PatternProperties) > 0 {
			for vprop, val := range instance.Seq2() {
				prop := vprop.String()
				// Check every matching pattern.
				for re, schema := range schema.patternProperties {
					if re.MatchString(prop) {
						if err := st.validate(val, schema, nil, append(path, prop)); err != nil {
							return err
						}
						evalProps[prop] = true
					}
				}
			}
		}
		if schema.AdditionalProperties != nil {
			// Apply to all properties not handled above.
			for vprop, val := range instance.Seq2() {
				prop := vprop.String()
				if !evalProps[prop] {
					if err := st.validate(val, schema.AdditionalProperties, nil, append(path, prop)); err != nil {
						return err
					}
					evalProps[prop] = true
				}
			}
		}
		anns.noteProperties(evalProps)
		if schema.PropertyNames != nil {
			for prop := range instance.Seq() {
				if err := st.validate(prop, schema.PropertyNames, nil, append(path, prop.String())); err != nil {
					return err
				}
			}
		}

		// https://json-schema.org/draft/2020-12/draft-bhutton-json-schema-validation-01#section-6.5
		if schema.MinProperties != nil {
			if n, m := instance.Len(), *schema.MinProperties; n < m {
				return fmt.Errorf("minProperties: object has %d properties, less than %d", n, m)
			}
		}
		if schema.MaxProperties != nil {
			if n, m := instance.Len(), *schema.MaxProperties; n > m {
				return fmt.Errorf("maxProperties: object has %d properties, greater than %d", n, m)
			}
		}

		hasProperty := func(prop string) bool {
			return instance.MapIndex(reflect.ValueOf(prop)).IsValid()
		}

		missingProperties := func(props []string) []string {
			var missing []string
			for _, p := range props {
				if !hasProperty(p) {
					missing = append(missing, p)
				}
			}
			return missing
		}

		if schema.Required != nil {
			if m := missingProperties(schema.Required); len(m) > 0 {
				return fmt.Errorf("required: missing properties: %q", m)
			}
		}
		if schema.DependentRequired != nil {
			// "Validation succeeds if, for each name that appears in both the instance
			// and as a name within this keyword's value, every item in the corresponding
			// array is also the name of a property in the instance." §6.5.4
			for dprop, reqs := range schema.DependentRequired {
				if hasProperty(dprop) {
					if m := missingProperties(reqs); len(m) > 0 {
						return fmt.Errorf("dependentRequired[%q]: missing properties %q", dprop, m)
					}
				}
			}
		}

		// https://json-schema.org/draft/2020-12/json-schema-core#section-10.2.2.4
		if schema.DependentSchemas != nil {
			// This does not collect annotations, although it seems like it should.
			for dprop, ss := range schema.DependentSchemas {
				if hasProperty(dprop) {
					// TODO: include dependentSchemas[dprop] in the errors.
					err := st.validate(instance, ss, &anns, path)
					if err != nil {
						return err
					}
				}
			}
		}
		if schema.UnevaluatedProperties != nil && !anns.allProperties {
			// This looks a lot like AdditionalProperties, but depends on in-place keywords like allOf
			// in addition to sibling keywords.
			for vprop, val := range instance.Seq2() {
				prop := vprop.String()
				if !anns.evaluatedProperties[prop] {
					if err := st.validate(val, schema.UnevaluatedProperties, nil, append(path, prop)); err != nil {
						return err
					}
				}
			}
			// The spec says the annotation should be the set of evaluated properties, but we can optimize
			// by setting a single boolean, since after this succeeds all properties will be validated.
			// See https://json-schema.slack.com/archives/CT7FF623C/p1745592564381459.
			anns.allProperties = true
		}
	}

	if callerAnns != nil {
		// Our caller wants to know what we've validated.
		callerAnns.merge(&anns)
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
