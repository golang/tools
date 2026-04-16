//go:build go1.27

package embedlit

type A struct {
	a int
	B
}

type B struct {
	b int
	C
}

type C struct {
	c int
	D
}

type D struct {
	d int
	E
}

type E struct {
	e int
	F
}

type F struct {
	f int
}

type G struct {
	f int
}

type H struct {
	F
	G
}

type I struct {
	i int
}

type J struct {
	F
	G
	I
}

const zero = 0

type K struct{ L }
type L []int

var (
	_ = A{B: B{b: 1}}                         // want "embedded field type can be removed from struct literal"
	_ = A{a: 1, B: B{b: 1, C: C{c: 1}}}       // want "embedded field type can be removed from struct literal"
	_ = E{F: F{1}}                            // nope: cannot promote unkeyed fields
	_ = D{E: E{F: F{1}}, d: 1}                // want "embedded field type can be removed from struct literal"
	_ = D{E: E{e: 2, F: F{1}}}                // want "embedded field type can be removed from struct literal"
	_ = A{a: 10, B: B{C: C{1, D{d: 1}}}}      // want "embedded field type can be removed from struct literal"
	_ = H{F: F{f: 1}}                         // nope: cannot promote ambiguous fields
	_ = J{I: I{i: 1}, F: F{f: 1}, G: G{f: 1}} // want "embedded field type can be removed from struct literal"
	// multi-line with commas
	_ = A{ // want "embedded field type can be removed from struct literal"
		B: B{
			b: 1,
		},
	}
	// empty composite lit
	_ = A{a: 1, B: B{}} // want "embedded field type can be removed from struct literal"
	// don't suggest a fix if it's too tricky to preserve comments
	_ = A{ // nope: comments within range to delete
		B: B{ // one
			C: C{ // two
				c: 1, // three
			}, // four
		}, // five
	}
	_ = A{ // nope: comments within range to delete
		B: B{b: 1 /* comment, with comma */},
		a: 2,
	}
	_ = A{ // nope: comments within range to delete
		B: B{
			b: 1, // comment, with comma
		},
		a: 2,
	}
	_ = A{B: B{b: 1} /* comment */, a: 2} // want "embedded field type can be removed from struct literal"
	_ = K{L: L{zero: 0}}                  // nope: cannot promote slice elements
)
