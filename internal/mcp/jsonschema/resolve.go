// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file deals with preparing a schema for validation, including various checks,
// optimizations, and the resolution of cross-schema references.

package jsonschema

import (
	"errors"
	"fmt"
	"regexp"
)

// A Resolved consists of a [Schema] along with associated information needed to
// validate documents against it.
// A Resolved has been validated against its meta-schema, and all its references
// (the $ref and $dynamicRef keywords) have been resolved to their referenced Schemas.
// Call [Schema.Resolve] to obtain a Resolved from a Schema.
type Resolved struct {
	root *Schema
}

// Resolve resolves all references within the schema and performs other tasks that
// prepare the schema for validation.
func (root *Schema) Resolve() (*Resolved, error) {
	// There are three steps involved in preparing a schema to validate.
	// 1. Check: validate the schema against a meta-schema, and perform other well-formedness
	//    checks. Precompute some values along the way.
	// 2. Resolve URIs: TODO.
	// 3. Resolve references: TODO.
	if err := root.check(); err != nil {
		return nil, err
	}
	return &Resolved{root: root}, nil
}

func (s *Schema) check() error {
	if s == nil {
		return errors.New("nil schema")
	}
	var errs []error
	report := func(err error) { errs = append(errs, err) }

	for ss := range s.all() {
		ss.checkLocal(report)
	}
	return errors.Join(errs...)
}

// checkLocal checks s for validity, independently of other schemas it may refer to.
// Since checking a regexp involves compiling it, checkLocal saves those compiled regexps
// in the schema for later use.
// It appends the errors it finds to errs.
func (s *Schema) checkLocal(report func(error)) {
	addf := func(format string, args ...any) {
		report(fmt.Errorf("jsonschema.Schema: "+format, args...))
	}

	if s == nil {
		addf("nil subschema")
		return
	}
	if err := s.basicChecks(); err != nil {
		report(err)
		return
	}

	// TODO: validate the schema's properties,
	// ideally by jsonschema-validating it against the meta-schema.

	// Check and compile regexps.
	if s.Pattern != "" {
		re, err := regexp.Compile(s.Pattern)
		if err != nil {
			addf("pattern: %w", err)
		} else {
			s.pattern = re
		}
	}
	if len(s.PatternProperties) > 0 {
		s.patternProperties = map[*regexp.Regexp]*Schema{}
		for reString, subschema := range s.PatternProperties {
			re, err := regexp.Compile(reString)
			if err != nil {
				addf("patternProperties[%q]: %w", reString, err)
				continue
			}
			s.patternProperties[re] = subschema
		}
	}
}
