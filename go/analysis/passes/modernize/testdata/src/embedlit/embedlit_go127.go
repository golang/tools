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

type T struct {
	a int
	b int
	U
}

type U struct {
	x int
}

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

	_ = A{a: 1, B: B{}} // nope: empty composite lit

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
	_ = A{B: /* comment */ B{b: 1}}       // nope: comment in range to delete
	_ = A{B: B{b: 1} /* comment */, a: 2} // want "embedded field type can be removed from struct literal"
	_ = K{L: L{zero: 0}}                  // nope: cannot promote slice elements
	_ = K{L: L{0: 100}}                   // nope: cannot promote slice elements

	_ = A{ // want "embedded field type can be removed from struct literal"
		B: B{
			C: C{
				c: 1,
			},
			b: 2,
		},
		a: 3,
	}

	_ = A{B: B{C: C{c: 1}}} // want "embedded field type can be removed from struct literal"

	_ = A{B: B{ // want "embedded field type can be removed from struct literal"
		C: C{
			c: 1,
		},
	}}

	_ = A{ // want "embedded field type can be removed from struct literal"
		B: B{C: C{c: 1}},
	}

	_ = A{ // want "embedded field type can be removed from struct literal"
		B: B{ // comment here
			C: C{
				c: 1,
			},
		},
	}
)

func _() {
	t1 := A{a: 1} // want "embedded field assignment can be moved to struct literal"
	t1.b = 2

	var t2 A
	t2 = A{a: 1} // want "embedded field assignment can be moved to struct literal"
	t2.b = 2

	var t3 = A{a: 1} // want "embedded field assignment can be moved to struct literal"
	t3.b = 2

	t4 := T{1, 2, U{x: 3}} // nope: can't mix keyed and unkeyed elements
	t4.x = 4

	t5 := A{a: 1}
	_ = t5 // nope: intervening statement
	t5.b = 2

	t6 := A{a: 1} // nope: value assigned depends on t6 itself
	t6.b = t6.a + 1

	t7 := A{a: 1} // want "embedded field assignment can be moved to struct literal"
	t7.b = foo()

	// Only apply edits from pattern A first even though both patterns apply.
	t8 := A{ // want "embedded field type can be removed from struct literal"
		B: B{b: 1},
	}
	t8.a = 2

	t9 := A{} // nope: multiple assignments not yet supported
	t9.a, t9.b = 1, 2

	t10 := A{} // want "embedded field assignment can be moved to struct literal"
	t10.a = 1  // this comment is preserved
	t10.b = 2  // this one too

	t11 := A{a: 1} // want "embedded field assignment can be moved to struct literal"
	t11.b = 2
	t11.c = 3

	t12 := A{} // want "embedded field assignment can be moved to struct literal"
	t12.B = B{
		b: 1, // this comment is preserved
	}

	t13 := A{} // want "embedded field assignment can be moved to struct literal"
	t13.a = 1
	// comment between assignments
	t13.b = 2

	t14 := A{} // want "embedded field assignment can be moved to struct literal"
	t14.a = 1
	t14.b =
		foo() +
			1

	t15 := A{a: 1} // nope: += in field assignment
	t15.b += 2

	t16 := A{ // want "embedded field assignment can be moved to struct literal"
		a: 1, // comment, with a comma
	}
	t16.b = 2
}

func foo() int {
	return 0
}
