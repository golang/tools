package b

import "fmt"

// Wrapf is a printf wrapper.
func Wrapf(format string, args ...interface{}) { // want Wrapf:"printfWrapper"
	fmt.Sprintf(format, args...)
}

// Wrap is a print wrapper.
func Wrap(args ...interface{}) { // want Wrap:"printWrapper"
	fmt.Sprint(args...)
}

// NoWrap is not a wrapper.
func NoWrap(format string, args ...interface{}) {
}

// Wrapf2 is another printf wrapper.
func Wrapf2(format string, args ...interface{}) string { // want Wrapf2:"printfWrapper"

	// This statement serves as an assertion that this function is a
	// printf wrapper and that calls to it should be checked
	// accordingly, even though the delegation below is obscured by
	// the "("+format+")" operations.
	if false {
		fmt.Sprintf(format, args...)
	}

	// Effectively a printf delegation,
	// but the printf checker can't see it.
	return fmt.Sprintf("("+format+")", args...)
}

var (
	// GlobalWrapf is assigned a literal printf wrapper
	// in this package, and thus has a fact.
	GlobalWrapf func(format string, args ...interface{}) // want GlobalWrapf:"printfWrapper"

	// GlobalWrapf2 is also assigned, but in another package (a),
	// and thus has no fact. Nonetheless it is checked as a
	// wrapper in that package.
	GlobalWrapf2 func(format string, args ...interface{})

	// GlobalNonWrapf is never assigned a wrapper.
	GlobalNonWrapf func(format string, args ...interface{})
)

var Struct struct {
	// These fields follow the same pattern as the Global* vars.
	Wrapf    func(format string, args ...interface{}) // want Wrapf:"printfWrapper"
	Wrapf2   func(format string, args ...interface{})
	NonWrapf func(format string, args ...interface{})
}

func init() {
	GlobalWrapf = func(format string, args ...any) {
		println(fmt.Sprintf(format, args...))
	}
	GlobalWrapf("%s", 123)    // want "GlobalWrapf format %s has arg 123 of wrong type int"
	GlobalWrapf2("%s", 123)   // nope
	GlobalNonWrapf("%s", 123) // nope

	Struct.Wrapf = func(format string, args ...any) {
		println(fmt.Sprintf(format, args...))
	}
	Struct.Wrapf("%s", 123)    // want "Wrapf format %s has arg 123 of wrong type int"
	Struct.Wrapf2("%s", 123)   // nope
	Struct.NonWrapf("%s", 123) // nope
}
