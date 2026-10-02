// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

import (
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/internal/testenv"
)

func TestMethodWrapperSignatureNotCanonicalized(t *testing.T) {
	testenv.NeedsGoCommand1Point(t, 27)

	prog := NewProgram(token.NewFileSet(), 0)
	// Give the method a type parameter so maybeInstance instantiates its
	// signature before createWrapper adapts the receiver.
	tp := types.NewTypeParam(types.NewTypeName(token.NoPos, nil, "T", nil), types.Universe.Lookup("any").Type())
	recv := types.NewVar(token.NoPos, nil, "recv", types.Typ[types.Int])
	param := types.NewVar(token.NoPos, nil, "x", tp)
	result := types.NewVar(token.NoPos, nil, "", tp)
	method := types.NewSignatureType(recv, nil, []*types.TypeParam{tp}, types.NewTuple(param), types.NewTuple(result), false)

	// Instantiating the method must retain its receiver, even though the
	// resulting signature has the same parameters and results as a function.
	_, instance := maybeInstance(prog, "M", method, []types.Type{types.Typ[types.String]})
	if instance.Recv() == nil {
		t.Fatal("instantiated method lost its receiver")
	}

	// Signature identity ignores the receiver. When the ordinary function is
	// canonicalized, it must not retrieve the instantiated method instead.
	function := types.NewSignatureType(nil, nil, nil, instance.Params(), instance.Results(), false)
	canonical := prog.canon.Type(function).(*types.Signature)
	if canonical.Recv() != nil {
		t.Fatalf("canonical function acquired method receiver: %v", canonical.Recv())
	}

	// A second, separately allocated function signature with the same types
	// should retrieve the first function from the cache. This checks that the
	// preceding assertion is not passing because the cache is never used.
	again := types.NewSignatureType(nil, nil, nil, instance.Params(), instance.Results(), false)
	if got := prog.canon.Type(again); got != canonical {
		t.Fatalf("identical function signature missed cache: got %p, want %p", got, canonical)
	}
}
