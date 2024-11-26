// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package yield

import (
	"bufio"
	"io"
)

//
//
// Modify this block of comment lines as needed when changing imports
// to avoid perturbing subsequent line numbers (and thus error messages).
//
// This is L16.

func goodIter(yield func(int) bool) {
	_ = yield(1) && yield(2) && yield(3) // ok
}

func badIterOR(yield func(int) bool) {
	_ = yield(1) || // want `yield may be called again \(on L25\) after returning false`
		yield(2) || // want `yield may be called again \(on L26\) after returning false`
		yield(3)
}

func badIterSeq(yield func(int) bool) {
	yield(1) // want `yield may be called again \(on L31\) after returning false`
	yield(2) // want `yield may be called again \(on L32\) after returning false`
	yield(3) // ok
}

func badIterLoop(yield func(int) bool) {
	for {
		yield(1) // want `yield may be called again after returning false`
	}
}

func goodIterLoop(yield func(int) bool) {
	for {
		if !yield(1) {
			break
		}
	}
}

func badIterIf(yield func(int) bool) {
	ok := yield(1) // want `yield may be called again \(on L52\) after returning false`
	if !ok {
		yield(2)
	} else {
		yield(3)
	}
}

func singletonIter(yield func(int) bool) {
	yield(1) // ok
}

func twoArgumentYield(yield func(int, int) bool) {
	_ = yield(1, 1) || // want `yield may be called again \(on L64\) after returning false`
		yield(2, 2)
}

func zeroArgumentYield(yield func() bool) {
	_ = yield() || // want `yield may be called again \(on L69\) after returning false`
		yield()
}

func tricky(in io.ReadCloser) func(yield func(string, error) bool) {
	return func(yield func(string, error) bool) {
		scan := bufio.NewScanner(in)
		for scan.Scan() {
			if !yield(scan.Text(), nil) { // want `yield may be called again \(on L82\) after returning false`
				_ = in.Close()
				break
			}
		}
		if err := scan.Err(); err != nil {
			yield("", err)
		}
	}
}
