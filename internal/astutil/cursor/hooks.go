// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.23

package cursor

import (
	"go/ast"
	_ "unsafe" // for go:linkname

	"golang.org/x/tools/go/ast/inspector"
)

// This file defines backdoor access to inspector.

// Copied from inspector.event; must remain in sync.
// (Note that the linkname effects a type coercion too.)
type event struct {
	node   ast.Node
	typ    uint64 // typeOf(node) on push event, or union of typ strictly between push and pop events on pop events
	index  int32  // index of corresponding push or pop event (relative to this event's index, +ve=push, -ve=pop)
	parent int32  // index of parent's push node (defined for push nodes only)
}

//go:linkname maskOf golang.org/x/tools/go/ast/inspector.maskOf
func maskOf(nodes []ast.Node) uint64

//go:linkname events golang.org/x/tools/go/ast/inspector.events
func events(in *inspector.Inspector) []event

func (c Cursor) events() []event { return events(c.in) }
