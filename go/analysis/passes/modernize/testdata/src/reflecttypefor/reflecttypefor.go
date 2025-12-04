package reflecttypefor

import (
	"io"
	"reflect"
	"time"
)

type A string

type B[T any] int

var (
	x any
	a A
	b B[int]
	_ = reflect.TypeOf(x)                 // nope (dynamic)
	_ = reflect.TypeOf(0)                 // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(uint(0))           // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(error(nil))        // nope (likely a mistake)
	_ = reflect.TypeOf((*error)(nil))     // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(io.Reader(nil))    // nope (likely a mistake)
	_ = reflect.TypeOf((*io.Reader)(nil)) // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(*new(time.Time))   // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(time.Time{})       // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(time.Duration(0))  // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(&a)                // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(&b)                // want "reflect.TypeOf call can be simplified using TypeFor"
)

// Eliminate local var if we deleted its last use.
func _() {
	var zero string
	_ = reflect.TypeOf(zero) // want "reflect.TypeOf call can be simplified using TypeFor"

	var z2 string
	_ = reflect.TypeOf(z2) // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = z2                 // z2 has multiple uses
}

type T struct {
	f struct {
		A bool
		B int
		C string
	}
}

type S struct {
	f [2]struct {
		A bool
		B int
		C string
	}
}

type R []struct {
	A int
}

type M[T struct{ F int }] int

type P struct {
	f interface {
	}
	g func() // fine length

	long func(a int, b int, c int) (bool, string, int) // too long

	s func(a struct{})

	q func() struct{}
}

func f(t *T, r R, m *M[struct{ F int }], s *S, p *P) {
	// No suggested fix for all of the following because the type is complicated -- e.g. has an unnamed struct,
	// interface, or signature -- so the fix would be more verbose than the original expression.
	// Also because structs and interfaces often acquire new fields and methods, and the type string
	// produced by this modernizer won't get updated automatically, potentially causing a bug.
	_ = reflect.TypeOf(&t.f)
	_ = reflect.TypeOf(r[0])
	_ = reflect.TypeOf(m)
	_ = reflect.TypeOf(&s.f)
	_ = reflect.TypeOf(&p.f)
	_ = reflect.TypeOf(&p.g)
	_ = reflect.TypeOf(&p.long)
	_ = reflect.TypeOf(&p.q)
	_ = reflect.TypeOf(&p.s)
}
