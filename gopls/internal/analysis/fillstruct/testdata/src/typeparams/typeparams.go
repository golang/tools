// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillstruct

type emptyStruct[A any] struct{}

var _ = emptyStruct[int]{}

type basicStruct[T any] struct {
	foo T
}

var _ = basicStruct[int]{} // want `basicStruct\[int\] literal has missing fields`

type twoArgStruct[F, B any] struct {
	foo F
	bar B
}

var _ = twoArgStruct[string, int]{} // want `twoArgStruct\[string, int\] literal has missing fields`

var _ = twoArgStruct[int, string]{ // want `twoArgStruct\[int, string\] literal has missing fields`
	bar: "bar",
}

type nestedStruct struct {
	bar   string
	basic basicStruct[int]
}

var _ = nestedStruct{} // want "nestedStruct literal has missing fields"

func _[T any]() {
	type S struct{ t T }
	x := S{} // want "S"
	_ = x
}

func Test() {
	var tests = []struct {
		a, b, c, d, e, f, g, h, i, j, k, l, m, n, o, p string
	}{
		{}, // want "anonymous struct{ a: string, b: string, c: string, ... } literal has missing fields"
	}
	for _, test := range tests {
		_ = test
	}
}
