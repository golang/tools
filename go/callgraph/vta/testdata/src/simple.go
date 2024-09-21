// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

var gl int

type X struct {
	a int
	b int
}

func main() {
	print(gl)
}

func foo() (r int) { return gl }
