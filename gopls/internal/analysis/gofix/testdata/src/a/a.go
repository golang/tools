package a

import "a/internal"

// Functions.

func f() {
	One() // want `Call of a.One should be inlined`

	new(T).Two() // want `Call of \(a.T\).Two should be inlined`
}

type T struct{}

//go:fix inline
func One() int { return one } // want One:`goFixInline a.One`

const one = 1

//go:fix inline
func (T) Two() int { return 2 } // want Two:`goFixInline \(a.T\).Two`

// Constants.

const Uno = 1

//go:fix inline
const In1 = Uno // want In1: `goFixInline const "a".Uno`

const (
	no1 = one

	//go:fix inline
	In2 = one // want In2: `goFixInline const "a".one`
)

//go:fix inline
const (
	in3  = one
	in4  = one
	bad1 = 1 // want `invalid //go:fix inline directive: const value is not the name of another constant`
)

//go:fix inline
const in5,
	in6,
	bad2 = one, one,
	one + 1 // want `invalid //go:fix inline directive: const value is not the name of another constant`

// Make sure we don't crash on iota consts, but still process the whole decl.
//
//go:fix inline
const (
	a = iota // want `invalid //go:fix inline directive: const value is iota`
	b
	in7 = one
)

func _() {
	x := In1 // want `Constant In1 should be inlined`
	x = In2  // want `Constant In2 should be inlined`
	x = in3  // want `Constant in3 should be inlined`
	x = in4  // want `Constant in4 should be inlined`
	x = in5  // want `Constant in5 should be inlined`
	x = in6  // want `Constant in6 should be inlined`
	x = in7  // want `Constant in7 should be inlined`
	x = no1
	_ = x

	in1 := 1 // don't inline lvalues
	_ = in1
}

const (
	x = 1
	//go:fix inline
	in8 = x
)

//go:fix inline
const D = internal.D // want D: `goFixInline const "a/internal".D`

func shadow() {
	var x int // shadows x at package scope

	//go:fix inline
	const a = iota // want `invalid //go:fix inline directive: const value is iota`

	const iota = 2
	// Below this point, iota is an ordinary constant.

	//go:fix inline
	const b = iota

	x = a // a is defined with the predeclared iota, so it cannot be inlined
	x = b // want `Constant b should be inlined`

	// Don't offer to inline in8, because the result, "x", would mean something different
	// in this scope than it does in the scope where in8 is defined.
	x = in8

	_ = x
}
