package b

import "a"
import . "c"

func f() {
	a.One() // want `cannot inline call to a.One because body refers to non-exported one`

	new(a.T).Two() // want `Call of \(a.T\).Two should be inlined`
}

//go:fix inline
const in2 = a.Uno

//go:fix inline
const in3 = C // c.C, by dot import

func g() {
	x := a.In1 // want `Constant a\.In1 should be inlined`

	a := 1
	// Although the package identifier "a" is shadowed here,
	// a second import of "a" will be added with a new package identifer.
	x = in2 // want `Constant in2 should be inlined`

	x = in3 // want `Constant in3 should be inlined`

	_ = a
	_ = x
}

const d = a.D // nope: a.D refers to a constant in a package that is not visible here.
