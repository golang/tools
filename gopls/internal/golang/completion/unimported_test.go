// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestFuncSignature(t *testing.T) {
	const src = `package p

func NoResult(a int) {}

func OneResult(a, b int) string { return "" }

func TwoResults(v any) ([]byte, error) { return nil, nil }

func NamedResults(r io.Reader) (n int, err error) { return }

func Variadic(format string, a ...any) string { return "" }

func Unnamed(int, string) bool { return false }

func NoParams() {}

func (r recv) Method() {}
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		fname string
		want  string
	}{
		{"NoResult", "(a int)"},
		{"OneResult", "(a, b int) string"}, // the printer keeps the grouping
		{"TwoResults", "(v any) ([]byte, error)"},
		{"NamedResults", "(r io.Reader) (n int, err error)"},
		{"Variadic", "(format string, a ...any) string"},
		{"Unnamed", "(int, string) bool"},
		{"NoParams", "()"},
	}
	for _, test := range tests {
		fd := findFunc(f, test.fname)
		if fd == nil {
			t.Errorf("findFunc(%q) = nil, want the declaration", test.fname)
			continue
		}
		if got := funcSignature(fd); got != test.want {
			t.Errorf("funcSignature(%q) = %q, want %q", test.fname, got, test.want)
		}
	}

	// A method has a receiver, so it is not a candidate for an import, and
	// there is no function named Missing at all.
	for _, fname := range []string{"Method", "Missing"} {
		if fd := findFunc(f, fname); fd != nil {
			t.Errorf("findFunc(%q) = %v, want nil", fname, fd.Name)
		}
	}
}
