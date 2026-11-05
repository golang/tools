package stringsbuilder

import "strings"

// Moving the initializer after the Builder declaration would make its s
// refer to the Builder instead of the outer string.
func shortDeclShadow(s string) string {
	{
		s := s
		for range 2 {
			s += "b"
		}
		return s
	}
}

func varDeclShadow(s string) string {
	{
		var s string = s
		for range 2 {
			s += "b"
		}
		return s
	}
}

func groupedVarDeclShadow(s string) string {
	{
		var (
			s = s
		)
		for range 2 {
			s += "b"
		}
		return s
	}
}

func concatenationShadow(s string) string {
	{
		s := s + "!"
		for range 2 {
			s += "b"
		}
		return s
	}
}

// References in nested function literals are affected too.
func closureShadow(s string) string {
	{
		s := func() string { return s }()
		for range 2 {
			s += "b"
		}
		return s
	}
}

// The outer object may be an imported package, rather than a variable.
func packageShadow() string {
	strings := strings.Repeat("a", 2)
	for range 2 {
		strings += "b"
	}
	return strings
}

// A parameter of a nested function has its own binding, which is preserved.
func initializerLocalBinding(s string) string {
	{
		s := func(s string) string { return s }("a")
		for range 2 {
			s += "b" // want "using string \\+= string in a loop is inefficient"
		}
		return s
	}
}

// A selector's field name does not refer to the outer variable.
func initializerFieldBinding(s string, value struct{ s string }) string {
	{
		s := value.s
		for range 2 {
			s += "b" // want "using string \\+= string in a loop is inefficient"
		}
		return s
	}
}

// Empty constant initializers are discarded, so their binding is irrelevant.
func initializerEmptyConstant() string {
	const s = ""
	{
		s := s
		for range 2 {
			s += "b" // want "using string \\+= string in a loop is inefficient"
		}
		return s
	}
}

func varDeclEmptyConstant() string {
	const s = ""
	{
		var s string = s
		for range 2 {
			s += "b" // want "using string \\+= string in a loop is inefficient"
		}
		return s
	}
}
