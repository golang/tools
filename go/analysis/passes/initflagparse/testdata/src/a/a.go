// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package a

import "flag"

func init() {
	flag.Parse() // want `flag.Parse call within package initialization`
}

type Test struct{}

func (_ *Test) init() {
	flag.Parse()
}

func main() {
	flag.Parse()
}
