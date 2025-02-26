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

// Type aliases

//go:fix inline
type A = T // want A: `goFixInline alias`

var _ A // want `Type alias A should be inlined`

//go:fix inline
type AA = // want AA: `goFixInline alias`
A         // want `Type alias A should be inlined`

var _ AA // want `Type alias AA should be inlined`

//go:fix inline
type (
	B = []T                 // want B: `goFixInline alias`
	C = map[*string][]error // want C: `goFixInline alias`
)

var _ B // want `Type alias B should be inlined`
var _ C // want `Type alias C should be inlined`

//go:fix inline
type E = map[[Uno]string][]*T // want `invalid //go:fix inline directive: array types not supported`

var _ E // nothing should happen here

//go:fix inline
type F = map[internal.T]T // want F: `goFixInline alias`

var _ F // want `Type alias F should be inlined`

//go:fix inline
type G = []chan *internal.T // want G: `goFixInline alias`

var _ G // want `Type alias G should be inlined`

// local shadowing
func _() {
	type string = int
	const T = 1

	var _ B // nope: B's RHS contains T, which is shadowed
	var _ C // nope: C's RHS contains string, which is shadowed
}

// local inlining
func _[P any]() {
	const a = 1
	//go:fix inline
	const b = a

	x := b // want `Constant b should be inlined`

	//go:fix inline
	type u = []P

	var y u // want `Type alias u should be inlined`

	_ = x
	_ = y
}
