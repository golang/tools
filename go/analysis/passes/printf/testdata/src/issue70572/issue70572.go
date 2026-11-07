package issue70572

// Regression test for failure to detect that a call to B[bool].Printf
// was printf-like, because of a missing call to types.Func.Origin.

import "fmt"

type A struct{}

func (v A) Printf(format string, values ...any) { // want Printf:"printfWrapper"
	fmt.Printf(format, values...)
}

type B[T any] struct{}

func (v B[T]) Printf(format string, values ...any) { // want Printf:"printfWrapper"
	fmt.Printf(format, values...)
}

func main() {
	var a A
	var b B[bool]
	a.Printf("x", 1) // want "arguments but no formatting directives"
	b.Printf("x", 1) // want "arguments but no formatting directives"
}
