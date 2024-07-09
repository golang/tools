// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import "go/types"

// containsOrigin reports whether the provided object set contains an object
// with the same origin as the provided obj (which may be a synthetic object
// created during instantiation).
func containsOrigin(objSet map[types.Object]bool, obj types.Object) bool {
	objOrigin := origin(obj)
	for target := range objSet {
		if origin(target) == objOrigin {
			return true
		}
	}
	return false
}

func origin(obj types.Object) types.Object {
	switch obj := obj.(type) {
	case *types.Var:
		return obj.Origin()
	case *types.Func:
		return obj.Origin()
	}
	return obj
}
