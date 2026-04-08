// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.27

package main

func main() {
	// Test direct initialization of promoted fields in struct literals,
	// introduced in go1.27. See golang/go#9859.
	type Inner struct {
		X int
	}
	type Outer struct {
		Inner
		Y int
	}

	o := Outer{
		X: 1,
		Y: 2,
	}

	if o.X != 1 || o.Y != 2 {
		panic("failed to initialize promoted field")
	}
}
