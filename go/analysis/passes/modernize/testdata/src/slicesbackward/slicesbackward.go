// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slicesbackward

import "slices"

var _ = slices.Backward[[]int] // force import of "slices" to avoid duplicate import edits

// Basic: only use of i is s[i] — should suggest "for _, v := range slices.Backward(s)"
func onlyIndexUse(s []int) {
	for i := len(s) - 1; i >= 0; i-- { // want "backward loop over slice can be modernized using slices.Backward"
		println(s[i])
	}
}

// Index used for something other than s[i] — keep both i and v.
func indexUsedElsewhere(s []int) {
	for i := len(s) - 1; i >= 0; i-- { // want "backward loop over slice can be modernized using slices.Backward"
		println(i, s[i])
	}
}

// Index used only for non-slice purpose (no s[i] at all).
func indexNoSliceAccess(s []int) {
	for i := len(s) - 1; i >= 0; i-- { // want "backward loop over slice can be modernized using slices.Backward"
		println(i)
	}
}

// Should NOT fire: condition is i > 0, not i >= 0.
func condGT(s []int) {
	for i := len(s) - 1; i > 0; i-- {
		println(s[i])
	}
}

// Should NOT fire: post is i -= 2.
func postNotDec(s []int) {
	for i := len(s) - 1; i >= 0; i -= 2 {
		println(s[i])
	}
}

// Should NOT fire: init is not len(s) - 1.
func initNotLenMinus1(s []int) {
	for i := len(s) - 2; i >= 0; i-- {
		println(s[i])
	}
}

// Should NOT fire: i is assigned inside the body.
func indexAssignedInBody(s []int) {
	for i := len(s) - 1; i >= 0; i-- {
		i = i - 1 // nolint: ignore for test
		println(s[i])
	}
}

// Should work with a named slice variable.
func namedSlice() {
	nums := []string{"a", "b", "c"}
	for i := len(nums) - 1; i >= 0; i-- { // want "backward loop over slice can be modernized using slices.Backward"
		println(nums[i])
	}
}

// Should fire: i is declared before the loop but not address-taken.
func iDeclaredBeforeLoop(s []int) {
	var i int
	for i = len(s) - 1; i >= 0; i-- { // want "backward loop over slice can be modernized using slices.Backward"
		println(s[i])
	}
	_ = i
}

// Should NOT fire: i is address-taken before the loop (init uses =, not :=).
func iAddressTakenBeforeLoop(s []int) {
	var i int
	p := &i
	for i = len(s) - 1; i >= 0; i-- {
		println(s[i])
	}
	_ = p
}

// Should NOT fire: an index expression is used as an lvalue (slice mutation)
func indexExprAssign(s []int) {
	for i := len(s) - 1; i >= 0; i-- {
		_ = s[i]
		s[i] = 0
	}
}

// Should NOT fire: an index expression is used as an lvalue (slice mutation)
func indexExprAssignWithOp(s []int) {
	for i := len(s) - 1; i >= 0; i-- {
		_ = s[i]
		s[i] += 1
		s[i] -= 1
		s[i] *= 1
		s[i] /= 1
	}
}

// Should NOT fire: an index expression is used as an lvalue (slice mutation)
func indexExprIncDec(s []int) {
	for i := len(s) - 1; i >= 0; i-- {
		_ = s[i]
		s[i]++
		s[i]--
	}
}

// Should NOT fire: an index expression is address-taken
func indexExprAddr(s []int) {
	for i := len(s) - 1; i >= 0; i-- {
		_ = &s[i]
	}
}
