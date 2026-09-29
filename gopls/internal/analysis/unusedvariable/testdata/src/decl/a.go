// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package decl

func a() {
	var b, c bool // want `declared (and|but) not used`
	panic(c)

	if 1 == 1 {
		var s string // want `declared (and|but) not used`
	}
}

func b() {
	// b is a variable
	var b bool // want `declared (and|but) not used`
}

func c() {
	var (
		d string

		// some comment for c
		c bool // want `declared (and|but) not used`
	)

	panic(d)
}

func issue81809(f func() (int, error)) error {
	var b int // want `declared (and|but) not used`
	var err error
	b = 1
	b, _ = f()
	b, err = f()
	return err
}
