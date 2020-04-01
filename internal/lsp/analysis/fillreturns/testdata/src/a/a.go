// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fillreturns

import (
	"errors"
	ast2 "go/ast"
	"io"
	"net/http"
	. "net/http"
	"net/url"
)

type T struct{}
type T1 = T
type I interface{}
type I1 = I
type z func(string, http.Handler) error

func x() error {
	return errors.New("foo")
}

func b() (string, int, error) {
	return "", errors.New("foo") // want "wrong number of return values \\(want 3, got 2\\)"
}

func c() (string, int, error) {
	return 7, errors.New("foo") // want "wrong number of return values \\(want 3, got 2\\)"
}

func d() (string, int, error) {
	return "", 7 // want "wrong number of return values \\(want 3, got 2\\)"
}

func e() (T, error, *bool) {
	return (z(http.ListenAndServe))("", nil) // want "wrong number of return values \\(want 3, got 1\\)"
}

func closure() (string, error) {
	_ = func() (int, error) {
		return errors.New("foo") // want "wrong number of return values \\(want 2, got 1\\)"
	}
	return // want "wrong number of return values \\(want 2, got 0\\)"
}

func basic() (uint8, uint16, uint32, uint64, int8, int16, int32, int64, float32, float64, complex64, complex128, byte, rune, uint, int, uintptr, string, bool, error) {
	return // want "wrong number of return values \\(want 20, got 0\\)"
}

func complex() (*int, []int, [2]int, map[int]int) {
	return // want "wrong number of return values \\(want 4, got 0\\)"
}

func structsAndInterfaces() (T, url.URL, T1, I, I1, io.Reader, Client, ast2.Stmt) {
	return // want "wrong number of return values \\(want 8, got 0\\)"
}

func m() (int, error) {
	if 1 == 2 {
		return // want "wrong number of return values \\(want 2, got 0\\)"
	} else if 1 == 3 {
		return // want "wrong number of return values \\(want 2, got 0\\)"
	} else {
		return // want "wrong number of return values \\(want 2, got 0\\)"
	}
	return // want "wrong number of return values \\(want 2, got 0\\)"
}
