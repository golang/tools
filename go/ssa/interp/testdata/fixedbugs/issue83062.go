package main

// This is a regression test for issue 83062, in which the builder
// emitted impossible type assertions in instances of generic
// functions, such as asserting I to B in F[B] below. The builder now
// lowers them to code with the same behavior.

import "strings"

type I interface{ M() }

type A struct{}

func (A) M() {}

type B struct{ n int }

func F[T any](x I) (commaOk, switched, asserted bool) {
	if v, ok := x.(T); ok {
		commaOk = true
	} else if any(v) != any(*new(T)) {
		panic("comma-ok value is not the zero value")
	}
	switch x.(type) {
	case T:
		switched = true
	}
	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); !ok || !strings.Contains(err.Error(), "interface conversion:") {
				panic(r)
			}
		}
	}()
	_ = x.(T)
	asserted = true
	return
}

func main() {
	for _, x := range []I{A{}, nil} {
		if c, s, a := F[B](x); c || s || a {
			panic("impossible assertion succeeded")
		}
	}
	if c, s, a := F[A](A{}); !c || !s || !a {
		panic("possible assertion failed")
	}
}
