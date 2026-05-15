// Regression test for an unsound optimization to initialize S{...} in
// place at *s (even though s is nil).

package main

type S struct {
	F1, F2 int
}

var n int

func gen() int {
	n++
	return n
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			if n != 2 {
				panic("n should be 2")
			}
		} else {
			panic("should have panicked")
		}
	}()
	var s *S
	*s = S{gen(), gen()}
}
