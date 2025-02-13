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
// existing code by using more modern features of Go, such as:
//
//   - replacing an if/else conditional assignment by a call to the
//     built-in min or max functions added in go1.21;
//   - replacing sort.Slice(x, func(i, j int) bool) { return s[i] < s[j] }
//     by a call to slices.Sort(s), added in go1.21;
//   - replacing interface{} by the 'any' type added in go1.18;
//   - replacing append([]T(nil), s...) by slices.Clone(s) or
//     slices.Concat(s), added in go1.21;
//   - replacing a loop around an m[k]=v map update by a call
//     to one of the Collect, Copy, Clone, or Insert functions
//     from the maps package, added in go1.21;
//   - replacing []byte(fmt.Sprintf...) by fmt.Appendf(nil, ...),
//     added in go1.19;
//   - replacing uses of context.WithCancel in tests with t.Context, added in
//     go1.24;
//   - replacing omitempty by omitzero on structs, added in go1.24;
//   - replacing append(s[:i], s[i+1]...) by slices.Delete(s, i, i+1),
//     added in go1.21
//   - replacing a 3-clause for i := 0; i < n; i++ {} loop by
//     for i := range n {}, added in go1.22;
//   - replacing Split in "for range strings.Split(...)" by go1.24's
//     more efficient SplitSeq;
//
// To apply all modernization fixes en masse, you can use the
// following command:
//
//	$ go run golang.org/x/tools/gopls/internal/analysis/modernize/cmd/modernize@latest -test ./...
//
// If the tool warns of conflicting fixes, you may need to run it more
// than once until it has applied all fixes cleanly. This command is
// not an officially supported interface and may change in the future.
package modernize
