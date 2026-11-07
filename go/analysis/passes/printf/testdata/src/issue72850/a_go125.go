//go:build !go1.26

package a

import "fmt"

var (
	_ = fmt.Sprintf("%q", byte(65)) // ok
	_ = fmt.Sprintf("%q", rune(65)) // ok
	_ = fmt.Sprintf("%q", 123)      // ok: pre-1.26 code allows %q on any integer
)
