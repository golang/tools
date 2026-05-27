// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package errorsastypeshadow checks for shadowing problems when
// [errors.AsType] is used in an if/else chain.
//
// # Analyzer errorsastypeshadow
//
// errorsastypeshadow: report shadowing of errors.AsType[T] in if/else chains
//
// For example:
//
//	err := f()
//	if err, ok := errors.AsType[*FooErr](err); ok {
//	    useFoo(err)
//	} else if err, ok := errors.AsType[*BarErr](err); ok {
//	    useBar(err)
//	}
//
// In this case, the second call to errors.AsType does not operate on the
// original error. Instead, its operand is the zero value of type *FooErr
// produced by the first if statement; this is invariably a mistake.
package errorsastypeshadow
