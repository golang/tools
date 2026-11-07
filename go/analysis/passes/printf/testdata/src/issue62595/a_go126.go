//go:build !go1.27

package a

import (
	"fmt"
	"unsafe"
)

var (
	i int
	_ = fmt.Sprintf("%d", &i)             // ok
	_ = fmt.Sprintf("%d", make(chan int)) // ok
	u unsafe.Pointer
	_ = fmt.Sprintf("%d", u) // ok
)
