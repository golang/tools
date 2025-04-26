// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file deals with preparing a schema for validation, including various checks,
// optimizations, and the resolution of cross-schema references.

package jsonschema

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
)

// A Resolved consists of a [Schema] along with associated information needed to
// validate documents against it.
// A Resolved has been validated against its meta-schema, and all its references
// (the $ref and $dynamicRef keywords) have been resolved to their referenced Schemas.
// Call [Schema.Resolve] to obtain a Resolved from a Schema.
type Resolved struct {
	root *Schema
	// map from $ids to their schemas
	resolvedURIs map[string]*Schema
}

// Resolve resolves all references within the schema and performs other tasks that
// prepare the schema for validation.
// baseURI can be empty, or an absolute URI (one that starts with a scheme).
// It is resolved (in the URI sense; see [url.ResolveReference]) with root's $id property.
// If the resulting URI is not absolute, then the schema cannot not contain relative URI references.
func (root *Schema) Resolve(baseURI string) (*Resolved, error) {
	// There are three steps involved in preparing a schema to validate.
	// 1. Check: validate the schema against a meta-schema, and perform other well-formedness
	//    checks. Precompute some values along the way.
	// 2. Resolve URIs: determine the base URI of the root and all its subschemas, and
	//    resolve (in the URI sense) all identifiers and anchors with their bases. This step results
	//    in a map from URIs to schemas within root.
	// 3. Resolve references: TODO.
	if err := root.check(); err != nil {
		return nil, err
	}
	var base *url.URL
	if baseURI == "" {
		base = &url.URL{} // so we can call ResolveReference on it
	} else {
		var err error
		base, err = url.Parse(baseURI)
		if err != nil {
			return nil, fmt.Errorf("parsing base URI: %w", err)
		}
	}
	m, err := resolveURIs(root, base)
	if err != nil {
		return nil, err
	}
	return &Resolved{
		root:         root,
		resolvedURIs: m,
	}, nil
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

// resolveURIs resolves the ids and anchors in all the schemas of root, relative
// to baseURI.
// See https://json-schema.org/draft/2020-12/json-schema-core#section-8.2, section
// 8.2.1.

// TODO(jba): dynamicAnchors (§8.2.2)
//
// Every schema has a base URI and a parent base URI.
//
// The parent base URI is the base URI of the lexically enclosing schema, or for
// a root schema, the URI it was loaded from or the one supplied to [Schema.Resolve].
//
// If the schema has no $id property, the base URI of a schema is that of its parent.
// If the schema does have an $id, it must be a URI, possibly relative. The schema's
// base URI is the $id resolved (in the sense of [url.URL.ResolveReference]) against
// the parent base.
//
// As an example, consider this schema loaded from http://a.com/root.json (quotes omitted):
//
//	{
//	    allOf: [
//	        {$id: "sub1.json", minLength: 5},
//	        {$id: "http://b.com", minimum: 10},
//	        {not: {maximum: 20}}
//	    ]
//	}
//
// The base URIs are as follows. Schema locations are expressed in the JSON Pointer notation.
//
//	schema         base URI
//	root           http://a.com/root.json
//	allOf/0        http://a.com/sub1.json
//	allOf/1        http://b.com (absolute $id; doesn't matter that it's not under the loaded URI)
//	allOf/2        http://a.com/root.json (inherited from parent)
//	allOf/2/not    http://a.com/root.json (inherited from parent)
func resolveURIs(root *Schema, baseURI *url.URL) (map[string]*Schema, error) {
	resolvedURIs := map[string]*Schema{}

	var resolve func(s, base *Schema) error
	resolve = func(s, base *Schema) error {
		// ids are scoped to the root.
		if s.ID == "" {
			// If a schema doesn't have an $id, its base is the parent base.
			s.baseURI = base.baseURI
		} else {
			// A non-empty ID establishes a new base.
			idURI, err := url.Parse(s.ID)
			if err != nil {
				return err
			}
			if idURI.Fragment != "" {
				return fmt.Errorf("$id %s must not have a fragment", s.ID)
			}
			// The base URI for this schema is its $id resolved against the parent base.
			s.baseURI = base.baseURI.ResolveReference(idURI)
			if !s.baseURI.IsAbs() {
				return fmt.Errorf("$id %s does not resolve to an absolute URI (base is %s)", s.ID, s.baseURI)
			}
			resolvedURIs[s.baseURI.String()] = s
			base = s // needed for anchors
		}

		// Anchors are URI fragments that are scoped to their base.
		// We treat them as keys in a map stored within the schema.
		if s.Anchor != "" {
			if base.anchors[s.Anchor] != nil {
				return fmt.Errorf("duplicate anchor %q in %s", s.Anchor, base.baseURI)
			}
			if base.anchors == nil {
				base.anchors = map[string]*Schema{}
			}
			base.anchors[s.Anchor] = s
		}

		for c := range s.children() {
			if err := resolve(c, base); err != nil {
				return err
			}
		}
		return nil
	}

	// Set the root URI to the base for now. If the root has an $id, the base will change.
	root.baseURI = baseURI
	// The original base, even if changed, is still a valid way to refer to the root.
	resolvedURIs[baseURI.String()] = root
	if err := resolve(root, root); err != nil {
		return nil, err
	}
	return resolvedURIs, nil
}
