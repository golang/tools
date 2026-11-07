// Regression test for an unsound optimization to initialize S{...} in
// place, even though that violates the required ordering between the
// evaluation of the "..." operands and the assignments to the fields
// of S.

package main

type S struct {
	X, Y int
}

var s S

func main() {
	// The function calls must occur before the assignment. That
	// means g() should observe the s.X=42 effect of the call f()
	// but not s.X=1 effect of the composite literal field assignment;
	// that should happens after g().
	s = S{f(), g()}

	if s.X != 1 || s.Y != 2 {
		panic("s should be {1, 2}")
	}
}

func f() int {
	s.X = 42
	return 1
}

func g() int {
	if s.X != 42 {
		panic("g should see s.X == 42 from side effect of f")
	}
	return 2
}
