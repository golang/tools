// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e

// No fix is offered for a generic type, as its type parameters are not
// in scope in the package-level declaration that a fix would insert.
// The absence of e.go.golden asserts that no fix is suggested.
type GenericAmbiguousErr[T any] struct { // want `both GenericAmbiguousErr and \*GenericAmbiguousErr implement the error interface, making the intent ambiguous`
	error
}
