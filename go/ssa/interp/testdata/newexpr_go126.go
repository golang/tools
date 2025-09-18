// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.26

package main

func main() {
	// Test new(expr), introduced in go1.26.
	var ptr *int = new(123)
	if *ptr != 123 {
		panic("wrong value")
	}
}
