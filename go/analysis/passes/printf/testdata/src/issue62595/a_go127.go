//go:build go1.27

package a

import (
	"fmt"
	"unsafe"
)

var (
	i int
	_ = fmt.Sprintf("%d", &i)             // want `fmt.Sprintf format %d has arg &i of wrong type \*int \(use %p for a pointer\)`
	_ = fmt.Sprintf("%d", make(chan int)) // want `fmt.Sprintf format %d has arg make\(chan int\) of wrong type chan int`
	u unsafe.Pointer
	_ = fmt.Sprintf("%d", u) // want `fmt.Sprintf format %d has arg u of wrong type unsafe.Pointer \(use %p for a pointer\)`
)
