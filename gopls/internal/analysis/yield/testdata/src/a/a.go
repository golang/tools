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

// Regression test for issue #70598.
func shortCircuitAND(yield func(int) bool) {
	ok := yield(1)
	ok = ok && yield(2)
	ok = ok && yield(3)
	ok = ok && yield(4)
}

// This example has a bug because a false yield(2) may be followed by yield(3).
func tricky2(yield func(int) bool) {
	cleanup := func() {}
	ok := yield(1)          // want "yield may be called again .on L104"
	stop := !ok || yield(2) // want "yield may be called again .on L104"
	if stop {
		cleanup()
	} else {
		// dominated by !stop => !(!ok || yield(2)) => yield(1) && !yield(2): bad.
		yield(3)
	}
}

// This example is sound, but the analyzer reports a false positive.
// TODO(adonovan): prune infeasible paths more carefully.
func tricky3(yield func(int) bool) {
	cleanup := func() {}
	ok := yield(1)           // want "yield may be called again .on L118"
	stop := !ok || !yield(2) // want "yield may be called again .on L118"
	if stop {
		cleanup()
	} else {
		// dominated by !stop => !(!ok || !yield(2)) => yield(1) && yield(2): good.
		yield(3)
	}
}
