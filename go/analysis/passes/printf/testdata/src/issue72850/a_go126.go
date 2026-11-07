//go:build go1.26

package a

import "fmt"

var (
	_ = fmt.Sprintf("%q", byte(65)) // ok
	_ = fmt.Sprintf("%q", rune(65)) // ok
	_ = fmt.Sprintf("%q", 123)      // want `fmt.Sprintf format %q has arg 123 of wrong type int`
)
