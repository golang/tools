// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package underscore declares identifiers whose names contain
// underscores, to test that example names are resolved as in go/doc.
// See golang/go#81778.
package underscore

type Foo struct{}

func (Foo) Method() {}

func (Foo) Do_It() {}

type Foo_Bar struct{}

func (Foo_Bar) Method() {}

// Only_Under has no counterpart named Only.
type Only_Under struct{}

// Amb_Bar and Amb.Bar are both matched by ExampleAmb_Bar.
type Amb struct{}

func (Amb) Bar() {}

type Amb_Bar struct{}

type Gen_T[T any] struct{}

func (Gen_T[T]) Method() {}

func Do_Thing() {}
