package stringsbuilder

import "strings"

// basic test
func _() {
	var s string
	s += "before"
	for range 10 {
		s += "in" // want "using string \\+= string in a loop is inefficient"
		s += "in2"
	}
	s += "after"
	print(s)
}

// with initializer
func _() {
	var s = "a"
	for range 10 {
		s += "b" // want "using string \\+= string in a loop is inefficient"
	}
	print(s)
}

// with empty initializer
func _() {
	var s = ""
	for range 10 {
		s += "b" // want "using string \\+= string in a loop is inefficient"
	}
	print(s)
}

// with short decl
func _() {
	s := "a"
	for range 10 {
		s += "b" // want "using string \\+= string in a loop is inefficient"
	}
	print(s)
}

// with short decl and empty initializer
func _() {
	s := ""
	for range 10 {
		s += "b" // want "using string \\+= string in a loop is inefficient"
	}
	print(s)
}

// nope: += must appear at least once within a loop.
func _() {
	var s string
	s += "a"
	s += "b"
	s += "c"
	print(s)
}

// nope: the declaration of s is not in a block.
func _() {
	if s := "a"; true {
		for range 10 {
			s += "x"
		}
		print(s)
	}
}

// in a switch (special case of "in a block" logic)
func _() {
	switch {
	default:
		s := "a"
		for range 10 {
			s += "b" // want "using string \\+= string in a loop is inefficient"
		}
		print(s)
	}
}

// nope: don't handle direct assignments to the string  (only +=).
func _(x string) string {
	var s string
	s = x
	for range 3 {
		s += "" + x
	}
	return s
}

// Regression test for bug in a GenDecl with parens.
func issue75318(slice []string) string {
	var (
		msg string
	)
	for _, s := range slice {
		msg += s // want "using string \\+= string in a loop is inefficient"
	}
	return msg
}

// Regression test for https://go.dev/issue/76983.
// We offer only the first fix if the second would overlap.
// This is an ad-hoc mitigation of the more general issue #76476.
func _(slice []string) string {
	a := "12"
	for range 2 {
		a += "34" // want "using string \\+= string in a loop is inefficient"
	}
	b := "56"
	for range 2 {
		b += "78"
	}
	a += b
	return a
}
func _(slice []string) string {
	var a strings.Builder
	a.WriteString("12")
	for range 2 {
		a.WriteString("34")
	}
	b := "56"
	for range 2 {
		b += "78" // want "using string \\+= string in a loop is inefficient"
	}
	a.WriteString(b)
	return a.String()
}

// Regression test for go.dev/issue/76934, which mutilated the var decl.
func stringDeclaredWithVarDecl() {
	var (
		before int // this is ok
		str    = "hello world"
	)
	for range 100 {
		str += "!" // want "using string \\+= string in a loop is inefficient"
	}
	println(before, str)
}

func nopeStringIsNotLastValueSpecInVarDecl() {
	var (
		str   = "hello world"
		after int // this defeats the transformation
	)
	for range 100 {
		str += "!" // nope
	}
	println(str, after)
}
