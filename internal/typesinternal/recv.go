// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesinternal

import (
	"go/types"

	"golang.org/x/tools/internal/aliases"
)

// ReceiverNamed returns the named type (if any) associated with the
// type of recv, which may be of the form N or *N, or aliases thereof.
// It also reports whether a Pointer was present.
func ReceiverNamed(recv *types.Var) (isPtr bool, named *types.Named) {
	t := recv.Type()
	if ptr, ok := aliases.Unalias(t).(*types.Pointer); ok {
		isPtr = true
		t = ptr.Elem()
	}
	named, _ = aliases.Unalias(t).(*types.Named)
	return
}
