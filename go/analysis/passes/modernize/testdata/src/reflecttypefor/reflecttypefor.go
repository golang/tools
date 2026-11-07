package reflecttypefor

import (
	"io"
	"reflect"
	"time"
)

type A string

type B[T any] int

type tokenKind int

type namedStruct struct{ field int }

type Fn func([4]byte)

const arrayLen = 4

func helper(a int, b string) {}

var (
	x     any
	a     A
	b     B[int]
	token tokenKind
	named = namedStruct{field: 1}
	ptr   = &named
	_     = reflect.TypeOf(x)                               // nope (dynamic)
	_     = reflect.TypeOf(0)                               // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(nil)                             // nope (likely a mistake)
	_     = reflect.TypeOf(uint(0))                         // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(error(nil))                      // nope (likely a mistake)
	_     = reflect.TypeOf((*error)(nil))                   // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(io.Reader(nil))                  // nope (likely a mistake)
	_     = reflect.TypeOf((io.Reader)(nil))                // nope (likely a mistake)
	_     = reflect.TypeOf((*io.Reader)(nil))               // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(*new(time.Time))                 // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(time.Time{})                     // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(time.Duration(0))                // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(&a)                              // nope (mentions symbol a)
	_     = reflect.TypeOf(&b)                              // nope (mentions symbol b)
	_     = reflect.TypeOf(tokenKind(0))                    // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(namedStruct{})                   // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf([4]byte{})                       // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf((*[4]byte)(nil))                 // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(any([4]byte{}).([4]byte))        // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(helper)                          // nope (mentions symbol helper)
	_     = reflect.TypeOf(token)                           // nope (mentions symbol token)
	_     = reflect.TypeOf(named.field)                     // nope (mentions symbols named and field)
	_     = reflect.TypeOf(ptr).Elem()                      // nope (mentions symbol ptr)
	_     = reflect.TypeOf([arrayLen]byte{})                // nope (mentions symbol arrayLen)
	_     = reflect.TypeOf((*[arrayLen]byte)(nil))          // nope (mentions symbol arrayLen)
	_     = reflect.TypeOf(any([4]byte{}).([arrayLen]byte)) // nope (mentions symbol arrayLen)
	_     = reflect.TypeOf(Fn(func([arrayLen]byte) {}))     // nope (mentions symbol arrayLen, in a func literal)
	_     = reflect.TypeOf([]io.Reader(nil)).Elem()         // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf([]*io.Reader(nil)).Elem()        // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf((*io.Reader)(nil)).Elem()        // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf([0]io.Reader{}).Elem()           // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf([1]io.Reader{nil}).Elem()        // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf([...]io.Reader{}).Elem()         // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf([...]io.Reader{nil}).Elem()      // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(chan int(nil)).Elem()            // want "reflect.TypeOf call can be simplified using TypeFor"
	_     = reflect.TypeOf(map[string]int(nil)).Elem()      // want "reflect.TypeOf call can be simplified using TypeFor"
)

// Preserve local variables used as symbolic stand-ins.
func _() {
	// Test for shadowed nil
	nil := "nil"
	_ = reflect.TypeOf(nil) // nope (mentions symbol nil)
	_ = nil                 // shadowed nil has multiple uses

	var zero string
	_ = reflect.TypeOf(zero) // nope (mentions symbol zero)

	var z2 string
	_ = reflect.TypeOf(z2) // nope (mentions symbol z2)
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
