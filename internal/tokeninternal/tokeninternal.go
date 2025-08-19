// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// package tokeninternal provides access to some internal features of the token
// package.
package tokeninternal

import (
	"go/token"
	"sync"
	"unsafe"
)

// GetLines returns the table of line-start offsets from a token.File.
func GetLines(file *token.File) []int {
	// token.File has a Lines method on Go 1.21 and later.
	if file, ok := (interface{})(file).(interface{ Lines() []int }); ok {
		return file.Lines()
	}

	// This declaration must match that of token.File.
	// This creates a risk of dependency skew.
	// For now we check that the size of the two
	// declarations is the same, on the (fragile) assumption
	// that future changes would add fields.
	type tokenFile119 struct {
		_     string
		_     int
		_     int
		mu    sync.Mutex // we're not complete monsters
		lines []int
		_     []struct{}
	}

	if unsafe.Sizeof(*file) != unsafe.Sizeof(tokenFile119{}) {
		panic("unexpected token.File size")
	}
	var ptr *tokenFile119
	type uP = unsafe.Pointer
	*(*uP)(uP(&ptr)) = uP(file)
	ptr.mu.Lock()
	defer ptr.mu.Unlock()
	return ptr.lines
}
