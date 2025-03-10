// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"go/types"
)

// syntaxErrorContext represents the context of the scenario when
// the source code contains syntax errors during code completion.
type syntaxErrorContext struct {
	// hasPeriod is true if we are handling scenarios where the source
	// contains syntax errors and the candidate includes the period.
	hasPeriod bool
	// lit is the literal value of the token that appeared before the period.
	lit string
}

// syntaxErrorCompletion provides better code completion when the source contains
// syntax errors and the candidate has periods. Only triggered if hasPeriod is true.
func (c *completer) syntaxErrorCompletion(obj types.Object) {
	// Check if the object is equal to the literal before the period.
	// If not, check for nested types (e.g., "foo.bar.baz<>").
	if obj.Name() != c.completionContext.syntaxError.lit {
		c.nestedSynaxErrorCompletion(obj.Type())
		return
	}

	switch obj := obj.(type) {
	case *types.PkgName:
		c.packageMembers(obj.Imported(), stdScore, nil, c.deepState.enqueue)
	default:
		c.methodsAndFields(obj.Type(), isVar(obj), nil, c.deepState.enqueue)
	}
}

// nestedSynaxErrorCompletion attempts to resolve code completion within nested types
// when the source contains syntax errors. It visits the types to find a match for the literal.
func (c *completer) nestedSynaxErrorCompletion(T types.Type) {
	var visit func(T types.Type)
	visit = func(T types.Type) {
		switch t := T.Underlying().(type) {
		case *types.Struct:
			for i := 0; i < t.NumFields(); i++ {
				field := t.Field(i)
				if field.Name() == c.completionContext.syntaxError.lit {
					c.methodsAndFields(field.Type(), isVar(field), nil, c.deepState.enqueue)
					return
				}
				if t, ok := field.Type().Underlying().(*types.Struct); ok {
					visit(t)
				}
			}
		}
	}
	visit(T)
}
