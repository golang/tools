// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package modernize providers the modernizer analyzer.
//
// # Analyzer modernize
//
// modernize: simplify code by using modern constructs
//
// This analyzer reports opportunities for simplifying and clarifying
// existing code by using more modern features of Go and its standard
// library.
//
// Each diagnostic provides a fix. Our intent is that these fixes may
// be safely applied en masse without changing the behavior of your
// program. In some cases the suggested fixes are imperfect and may
// lead to (for example) unused imports or unused local variables,
// causing build breakage. However, these problems are generally
// trivial to fix. We regard any modernizer whose fix changes program
// behavior to have a serious bug and will endeavor to fix it.
//
// To apply all modernization fixes en masse, you can use the
// following command:
//
//	$ go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest -fix -test ./...
//
// If the tool warns of conflicting fixes, you may need to run it more
// than once until it has applied all fixes cleanly. This command is
// not an officially supported interface and may change in the future.
//
// Changes produced by this tool should be reviewed as usual before
// being merged. In some cases, a loop may be replaced by a simple
// function call, causing comments within the loop to be discarded.
// Human judgment may be required to avoid losing comments of value.
//
// Each diagnostic reported by modernize has a specific category. (The
// categories are listed below.) Diagnostics in some categories, such
// as "efaceany" (which replaces "interface{}" with "any" where it is
// safe to do so) are particularly numerous. It may ease the burden of
// code review to apply fixes in two passes, the first change
// consisting only of fixes of category "efaceany", the second
// consisting of all others. This can be achieved using the -category flag:
//
//	$ modernize -category=efaceany  -fix -test ./...
//	$ modernize -category=-efaceany -fix -test ./...
//
// Categories of modernize diagnostic:
//
//   - minmax: replace an if/else conditional assignment by a call to
//     the built-in min or max functions added in go1.21.
//
//   - sortslice: replace sort.Slice(x, func(i, j int) bool) { return s[i] < s[j] }
//     by a call to slices.Sort(s), added in go1.21.
//
//   - efaceany: replace interface{} by the 'any' type added in go1.18.
//
//   - slicesclone: replace append([]T(nil), s...) by slices.Clone(s) or
//     slices.Concat(s), added in go1.21.
//
//   - mapsloop: replace a loop around an m[k]=v map update by a call
//     to one of the Collect, Copy, Clone, or Insert functions from
//     the maps package, added in go1.21.
//
//   - fmtappendf: replace []byte(fmt.Sprintf...) by fmt.Appendf(nil, ...),
//     added in go1.19.
//
//   - testingcontext: replace uses of context.WithCancel in tests
//     with t.Context, added in go1.24.
//
//   - omitzero: replace omitempty by omitzero on structs, added in go1.24.
//
//   - bloop: replace "for i := range b.N" or "for range b.N" in a
//     benchmark with "for b.Loop()", and remove any preceding calls
//     to b.StopTimer, b.StartTimer, and b.ResetTimer.
//
//   - slicesdelete: replace append(s[:i], s[i+1]...) by
//     slices.Delete(s, i, i+1), added in go1.21.
//
//   - rangeint: replace a 3-clause "for i := 0; i < n; i++" loop by
//     "for i := range n", added in go1.22.
//
//   - stringseq: replace Split in "for range strings.Split(...)" by go1.24's
//     more efficient SplitSeq, or Fields with FieldSeq.
//
//   - stringscutprefix: replace some uses of HasPrefix followed by TrimPrefix with CutPrefix,
//     added to the strings package in go1.20.
package modernize
