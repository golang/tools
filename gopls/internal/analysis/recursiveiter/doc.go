// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package recursiveiter defines an Analyzer that checks for mistakes
// in iterators for recursive data structures.
//
// # Analyzer recursiveiter
//
// recursiveiter: check for inefficient recursive iterators
//
// This analyzer reports when a function that returns an iterator
// (iter.Seq or iter.Seq2) calls itself as the operand of a range
// statement, as this is inefficient.
//
// When implementing an iterator (e.g. iter.Seq[T]) for a recursive
// data type such as a tree or linked list, it is tempting to
// recursively range over the iterator for each child element.
//
// Here's an example of a naive iterator over a binary tree:
//
//	type tree struct {
//		value       int
//		left, right *tree
//	}
//
//	func (t *tree) All() iter.Seq[int] {
//		return func(yield func(int) bool) {
//			if t != nil {
//				for elem := range t.left.All() { // "inefficient recursive iterator"
//					if !yield(elem) {
//						return
//					}
//				}
//				if !yield(t.value) {
//					return
//				}
//				for elem := range t.right.All() { // "inefficient recursive iterator"
//					if !yield(elem) {
//						return
//					}
//				}
//			}
//		}
//	}
//
// Though it correctly enumerates the elements of the tree, it hides a
// significant performance problem--two, in fact. Consider a balanced
// tree of N nodes. Iterating the root node will cause All to be
// called once on every node of the tree. This results in a chain of
// nested active range-over-func statements when yield(t.value) is
// called on a leaf node.
//
// The first performance problem is that each range-over-func
// statement must typically heap-allocate a variable, so iteration of
// the tree allocates as many variables as there are elements in the
// tree, for a total of O(N) allocations, all unnecessary.
//
// The second problem is that each call to yield for a leaf of the
// tree causes each of the enclosing range loops to receive a value,
// which they then immediately pass on to their respective yield
// function. This results in a chain of log(N) dynamic yield calls per
// element, a total of O(N*log N) dynamic calls overall, when only
// O(N) are necessary.
//
// A better implementation strategy for recursive iterators is to
// first define the "every" operator for your recursive data type,
// where every(f) reports whether f(x) is true for every element x in
// the data type. For our tree, the every function would be:
//
//	func (t *tree) every(f func(int) bool) bool {
//		return t == nil ||
//			t.left.every(f) && f(t.value) && t.right.every(f)
//	}
//
// Then the iterator can be simply expressed as a trivial wrapper
// around this function:
//
//	func (t *tree) All() iter.Seq[int] {
//		return func(yield func(int) bool) {
//			_ = t.every(yield)
//		}
//	}
//
// In effect, tree.All computes whether yield returns true for each
// element, short-circuiting if it every returns false, then discards
// the final boolean result.
//
// This has much better performance characteristics: it makes one
// dynamic call per element of the tree, and it doesn't heap-allocate
// anything. It is also clearer.
package recursiveiter
