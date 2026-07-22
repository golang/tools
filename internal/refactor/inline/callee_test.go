// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inline

import (
	"bytes"
	"maps"
	"testing"
)

// sampleCallee returns a Callee whose gob-serialized form includes several
// non-empty shadowMaps (via both object.Shadow and paramInfo.Shadow), which
// is where non-deterministic map encoding would show up.
func sampleCallee() *Callee {
	return &Callee{impl: gobCallee{
		Name:    "f",
		PkgPath: "example.com/p",
		FreeObjs: []object{
			{Name: "x", Kind: "var", Shadow: shadowMap{
				"a": 1, "b": 2, "c": -1, "d": 3, "e": -1, "f": 4, "g": 5, "h": -1, "i": 6, "j": 7,
			}},
		},
		Params: []*paramInfo{
			{Name: "p", Shadow: shadowMap{
				"k": 1, "l": -1, "m": 2, "n": 3, "o": -1, "q": 4, "r": 5, "s": -1, "t": 6, "u": 7,
			}},
		},
	}}
}

// TestCalleeGobEncodeDeterministic verifies that the gob encoding of a Callee
// is byte-for-byte stable across repeated encodings. The analysis framework
// requires deterministic fact encoding to avoid spurious cache misses in build
// systems (e.g. Bazel nogo). See golang/go#80237.
func TestCalleeGobEncodeDeterministic(t *testing.T) {
	callee := sampleCallee()

	first, err := callee.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode failed: %v", err)
	}
	for i := range 100 {
		got, err := callee.GobEncode()
		if err != nil {
			t.Fatalf("GobEncode failed on iteration %d: %v", i, err)
		}
		if !bytes.Equal(first, got) {
			t.Fatalf("GobEncode is non-deterministic: encoding %d differs from the first encoding", i)
		}
	}
}

// TestCalleeGobRoundTrip verifies that encoding then decoding a Callee
// preserves the contents of its shadowMaps.
func TestCalleeGobRoundTrip(t *testing.T) {
	want := sampleCallee()

	data, err := want.GobEncode()
	if err != nil {
		t.Fatalf("GobEncode failed: %v", err)
	}
	var got Callee
	if err := got.GobDecode(data); err != nil {
		t.Fatalf("GobDecode failed: %v", err)
	}

	if !maps.Equal(got.impl.FreeObjs[0].Shadow, want.impl.FreeObjs[0].Shadow) {
		t.Errorf("object.Shadow round-trip mismatch:\n got %v\nwant %v",
			got.impl.FreeObjs[0].Shadow, want.impl.FreeObjs[0].Shadow)
	}
	if !maps.Equal(got.impl.Params[0].Shadow, want.impl.Params[0].Shadow) {
		t.Errorf("paramInfo.Shadow round-trip mismatch:\n got %v\nwant %v",
			got.impl.Params[0].Shadow, want.impl.Params[0].Shadow)
	}
}
