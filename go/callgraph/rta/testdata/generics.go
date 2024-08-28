//go:build ignore
// +build ignore

package main

// Test of generic function calls.

type I interface {
	Foo()
}

type A struct{}

func (a A) Foo() {}

type B struct{}

func (b B) Foo() {}

func instantiated[X I](x X) {
	x.Foo()
}

var a A
var b B

func main() {
	instantiated[A](a) // static call
	instantiated[B](b) // static call

	local[C]().Foo()

	lambda[A]()()()
}

func local[X I]() I {
	var x X
	return x
}

type C struct{}

func (c C) Foo() {}

func lambda[X I]() func() func() {
	return func() func() {
		var x X
		return x.Foo
	}
}

// Note: command-line-arguments is used here as we load a single file by packages.Load,
// it will use command-line-arguments instead of the package name for ImportedPath

// WANT:
//
//  edge (*C).Foo --static method call--> (C).Foo
//  edge (A).Foo$bound --static method call--> (A).Foo
//  edge instantiated[command-line-arguments.A] --static method call--> (A).Foo
//  edge instantiated[command-line-arguments.B] --static method call--> (B).Foo
//  edge main --dynamic method call--> (*C).Foo
//  edge main --dynamic function call--> (A).Foo$bound
//  edge main --dynamic method call--> (C).Foo
//  edge main --static function call--> instantiated[command-line-arguments.A]
//  edge main --static function call--> instantiated[command-line-arguments.B]
//  edge main --static function call--> lambda[command-line-arguments.A]
//  edge main --dynamic function call--> lambda[command-line-arguments.A]$1
//  edge main --static function call--> local[command-line-arguments.C]
//
//  reachable (*C).Foo
//  reachable (A).Foo
//  reachable (A).Foo$bound
//  reachable (B).Foo
//  reachable (C).Foo
//  reachable instantiated[command-line-arguments.A]
//  reachable instantiated[command-line-arguments.B]
//  reachable lambda[command-line-arguments.A]
//  reachable lambda[command-line-arguments.A]$1
//  reachable local[command-line-arguments.C]
//
//  rtype *C
//  rtype C
