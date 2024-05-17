// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang_test

import (
	"testing"

	"golang.org/x/text/unicode/runenames"
)

func TestHoverLit_Issue65072(t *testing.T) {
	// This test attempts to demonstrate a root cause of the flake reported in
	// https://github.com/golang/go/issues/65072#issuecomment-2111425245 On the
	// aix-ppc64 builder, this rune was sometimes reported as "LETTER AMONGOLI".
	if got, want := runenames.Name(0x2211), "N-ARY SUMMATION"; got != want {
		t.Fatalf("runenames.Name(0x2211) = %q, want %q", got, want)
	}
}
