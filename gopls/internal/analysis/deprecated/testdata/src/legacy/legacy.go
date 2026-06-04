// Package legacy
package legacy

// Object docs
//
// Deprecated: Use obj instead.
type Object struct {
	// Don't use.
	//
	// Deprecated: Use `Field` instead.
	//
	// Paragraph after.
	DocCommentField int

	LineCommentField int // Deprecated: Use `Field` instead.

	// Deprecated: Doc comment chosen
	BothCommentField int // Deprecated: Line comment chosen
}

type Interface interface {
	// Deprecated: Use Method instead.
	DocCommentMethod(int) int
	LineCommentMethod(int) int // Old method. Deprecated: Use Method instead.
}

// No deprecation notice in concrete method docs.
func (Object) DocCommentMethod(int) int {
	return 1
}

// No deprecation notice in concrete method docs.
func (Object) LineCommentMethod(int) int {
	return 1
}

// Deprecated: use X instead.
func Legacy() {}
