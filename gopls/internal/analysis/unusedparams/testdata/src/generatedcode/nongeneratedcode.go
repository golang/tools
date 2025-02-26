package generatedcode

// This file does not have the generated code comment.
// It exists to ensure that generated code is considered
// when determining whether or not function parameters
// are used.

type implementsGeneratedInterface struct{}

// The f parameter should not be reported as unused,
// because this method implements the parent interface defined
// in the generated code.
func (implementsGeneratedInterface) n(f bool) {
	// The body must not be empty, otherwise unusedparams will
	// not report the unused parameter regardles of the
	// interface.
	println()
}

func b(x bool) { println() } // want "unused parameter: x"
