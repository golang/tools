package directive

// Functions.

func f() {
	One()

	new(T).Two()
}

type T struct{}

//go:fix inline
func One() int { return one } // want One:`goFixInline directive.One`

const one = 1

//go:fix inline
func (T) Two() int { return 2 } // want Two:`goFixInline \(directive.T\).Two`

// Constants.

const Uno = 1

//go:fix inline
const In1 = Uno // want In1: `goFixInline const "directive".Uno`

const (
	no1 = one

	//go:fix inline
	In2 = one // want In2: `goFixInline const "directive".one`
)

//go:fix inline
const bad1 = 1 // want `invalid //go:fix inline directive: const value is not the name of another constant`

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

const (
	x = 1
	//go:fix inline
	in8 = x
)

//go:fix inline
const in9 = iota // want `invalid //go:fix inline directive: const value is iota`

//go:fix inline
type E = map[[Uno]string][]*T // want `invalid //go:fix inline directive: array types not supported`
