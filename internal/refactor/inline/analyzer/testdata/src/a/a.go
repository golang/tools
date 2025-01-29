package a

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

//go:fix inline
const in1 = one // want in1: `goFixInline const "a".one`

const (
	no1 = one

	//go:fix inline
	in2 = one // want in2: `goFixInline const "a".one`
)

//go:fix inline
const (
	in3  = one // want in3: `goFixInline const "a".one`
	in4  = one // want in4: `goFixInline const "a".one`
	bad1 = 1   // want `invalid //go:fix inline directive: const value is not the name of another constant`
)

//go:fix inline
const in5, // want in5: `goFixInline const "a".one`
	in6, // want in6: `goFixInline const "a".one`
	bad2 = one, one,
	one + 1 // want `invalid //go:fix inline directive: const value is not the name of another constant`

// Make sure we don't crash on iota consts, but still process the whole decl.
//
//go:fix inline
const (
	a = iota
	b
	in7 = one // want in7: `goFixInline const "a".one`
)

func _() {
	x := in1 // want `Constant in1 should be inlined`
	x = in2  // want `Constant in2 should be inlined`
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
