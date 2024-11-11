// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typesinternal

import (
	"fmt"
	"go/types"
	"strings"
)

// ZeroString returns the string representation of the "zero" value of the type t.
// See [analysisutil.ZeroValue] for a variant that returns an [ast.Expr].
// TOOD(hxjiang): move [analysisutil.ZeroValue] and [analysisutil.IsZeroValue]
// to this file.
func ZeroString(t types.Type, qf types.Qualifier) string {
	switch t := t.(type) {
	case *types.Basic:
		switch {
		case t.Info()&types.IsBoolean != 0:
			return "false"
		case t.Info()&types.IsNumeric != 0:
			return "0"
		case t.Info()&types.IsString != 0:
			return `""`
		case t.Kind() == types.UnsafePointer:
			fallthrough
		case t.Kind() == types.UntypedNil:
			return "nil"
		default:
			panic(fmt.Sprint("ZeroString for unexpected type:", t))
		}
	case *types.Pointer, *types.Slice, *types.Interface, *types.Chan, *types.Map, *types.Signature:
		return "nil"
	case *types.Named, *types.Alias:
		switch under := t.Underlying().(type) {
		case *types.Map, *types.Slice, *types.Struct, *types.Array:
			return types.TypeString(t, qf) + "{}"
		default:
			return ZeroString(under, qf)
		}
	case *types.Array, *types.Struct:
		return types.TypeString(t, qf) + "{}"
	case *types.Tuple:
		// Tuples are not normal values.
		// We are currently format as "(t[0], ..., t[n])". Could be something else.
		components := make([]string, t.Len())
		for i := 0; i < t.Len(); i++ {
			components[i] = ZeroString(t.At(i).Type(), qf)
		}
		return "(" + strings.Join(components, ", ") + ")"
	case *types.TypeParam, *types.Union:
		return "*new(" + types.TypeString(t, qf) + ")"
	default:
		panic(t) // unreachable.
	}
}
