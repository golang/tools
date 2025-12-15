// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cursorutil provides utility functions for working with [inspector.Cursor].
//
// It should create no additional dependencies beyond those of Cursor
// itself, so that functions can be promoted to the public API of
// Cursor in due course.
package cursorutil

import (
	"go/ast"

	"golang.org/x/tools/go/ast/inspector"
)

// FirstEnclosing returns the first value from [cursor.Enclosing] as
// both a designated type and a [inspector.Cursor] pointing to it.
//
// It returns the zero value if it is not found.
//
// A common usage is:
//
//	call, callCur := cursorutil.FirstEnclosing[*ast.CallExpr](cur)
//	if call == nil {
//		// Not Found
//	}
func FirstEnclosing[N ast.Node](cur inspector.Cursor) (N, inspector.Cursor) {
	var typ N
	for cur := range cur.Enclosing(typ) {
		return cur.Node().(N), cur
	}
	return typ, inspector.Cursor{}
}

// Path returns the specified node followed by all its ancestors up to the file.
// Use it as an adaptor between cursors and code that works with PathEnclosingInterval.
// Ultimately all such code should be eliminated.
func Path(cur inspector.Cursor) (path []ast.Node) {
	for cur := range cur.Enclosing() {
		path = append(path, cur.Node())
	}
	return
}
