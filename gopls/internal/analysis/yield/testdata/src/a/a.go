// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package yield

import (
	"bufio"
	"io"
	"iter"
	"runtime"
)

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

// This case caused the former sparse dataflow implementation
// to give a false positive at yield(1).
func tricky2(yield func(int) bool) {
	cleanup := func() {}
	ok := yield(1)          // ok
	stop := !ok || yield(2) // want "yield may be called again .on L105"
	if stop {
		cleanup()
	} else {
		// dominated by !stop => !(!ok || yield(2)) => yield(1) && !yield(2): bad.
		yield(3)
	}
}

// This case from issue #74136 is sound, and should produce no diagnostic.
func tricky3(yield func(int) bool) {
	cleanup := func() {}
	ok := yield(1)
	stop := !ok || !yield(2)
	if stop {
		cleanup()
	} else {
		// dominated by !stop => !(!ok || !yield(2)) => yield(1) && yield(2): good.
		yield(3)
	}
}

// So is this one, from issue #75924
func tricky5(list []string, cond bool) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, s := range list {
			var ok bool
			if cond {
				ok = yield(s)
			} else {
				ok = yield(s)
			}
			if !ok {
				return
			}
		}
	}
}

// This one is from the tests of the "iter" package.
func tricky6() iter.Seq[int] {
	return func(yield func(int) bool) {
		for {
			if !yield(55) { // nope
				runtime.Goexit()
			}
		}
	}
}

func tricky7(cond bool, yield func(int) bool) {
	ok := yield(1) // want `yield may be called again \(on L...\) after returning false`
	if !ok {
		print(1)
	} else {
		return
	}
	if ok {
		print(2) // unreachable
	} else {
		yield(2)
	}
}

func tricky8(cond bool, yield func(int) bool) {
	ok := yield(1) // no diagnostic
	if !ok {
		print(1)
	} else {
		return
	}
	if !ok {
		print(2)
	} else {
		yield(2) // unreachable
	}
}

func tricky9(yield func(int) bool) {
	ok := yield(1) // no diagnostic
	if ok {
		return
	}
	if ok {
		yield(2) // unreachable
	}
}

func tricky10(yield func(int) bool, cond bool) {
	b := false
	if cond {
		b = true
	}
	// Inv: b is a phi of two constants.
	yield(1) // want "yield may be called again"
	if !b {
		if cond {
			yield(2)
		}
	}
}

// Regression test for issue #77681. A boolean switch is now
// handled just like an if/else chain in the SSA builder.
func switchShortCircuit(seq iter.Seq[int]) iter.Seq[int] {
	return func(yield func(int) bool) {
		for item := range seq {
			isZero := item == 0
			switch {
			case !isZero && !yield(item): // ok
				return
			case !isZero:
				continue
			case !yield(0): // ok
				return
			}
		}
	}
}

// Regression test for issue #76803.
// The former sparse dataflow implementation used to report a false positive here.
func issue76803() iter.Seq[int] {
	return func(yield func(int) bool) {
		ok := true
		for i := range 3 {
			if ok {
				ok = yield(i) // ok
			}
		}
	}
}

// Regression test for an order dependency in the DFS implementation.
func dfsOrder(yield func(int) bool, cond bool) {
	ok := yield(1) // want "yield may be called again"
	if cond {
		// Do nothing
	} else {
		ok = true
	}
	if ok {
		yield(2)
	}
}

// Regression tests for intra-block transfer function (ok = !ok).
func intraBlock(yield func(int) bool) {
	ok := yield(1)
	ok = !ok
	if !ok { // means ok was true, so yield(1) returned true.
		yield(2) // Should NOT report
	}
}
func intraBlockNegated(yield func(int) bool) {
	ok := yield(1) // want "yield may be called again"
	ok = !ok
	if ok { // means ok was false, so yield(1) returned false.
		yield(2)
	}
}

// Constant 'if' conditions are not used to prune paths (since they
// tend to result in overly specialized results as such conditions are
// often things like runtime.GOOS=="linux"), hence this false positive.
func constantIf(yield func(int) bool) {
	yield(1) // want "yield may be called again"
	if false {
		yield(2)
	}
}

func loopPhi(yield func(int) bool, cond bool) {
	yield(1) // want "yield may be called again.*on L..."
	b := true
	for {
		if !b {
			yield(2) // want "yield may be called again"
		}
		b = cond
	}
}

func path1(yield func(int) bool) {
	ok := yield(1) // want "yield may be called again.*on L..."
	b := false
	if !ok {
		b = true
	}
	if b {
		yield(2)
	}
}
func path2(yield func(int) bool) {
	ok := yield(1) // no diagnostic
	b := false
	if !ok {
		b = true
	}
	// Inv: b == !ok
	if !b {
		yield(2)
	}
}
func path3(yield func(int) bool) {
	ok := yield(1) // no diagnostic
	b := false
	if !ok {
		b = false
	} else {
		b = ok
	}
	// Inv: !b
	if b {
		yield(2)
	}
}
func path4(yield func(int) bool) {
	ok := yield(1) // want "yield may be called again.*on L..."
	b := false
	if !ok {
		b = true
	} else {
		b = ok
	}
	if b {
		yield(2)
	}
}
