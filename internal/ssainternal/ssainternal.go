// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ssainternal exposes setters for internals of go/ssa.
// It cannot actually depend on symbols from go/ssa.
package ssainternal

import "go/types"

// SetNoReturn sets the predicate used when building the ssa.Program
// prog that reports whether a given function cannot return.
// This may be used to prune spurious control flow edges
// after (e.g.) log.Fatal, improving the precision of analyses.
//
// You must link [golang.org/x/tools/go/ssa] into your application for
// this function to be non-nil.
//
// TODO(adonovan): add (*ssa.Program).SetNoReturn to the public API.
var SetNoReturn = func(prog any, noreturn func(*types.Func) bool) {
	panic("golang.org/x/tools/go/ssa not linked into application")
}
