// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package writestring defines an Analyzer that detects
// inefficient string concatenation in uses of WriteString.
//
// # Analyzer writestring
//
// writestring: detect inefficient string concatenation in uses of WriteString
//
// The writestring analyzer offers to replace a call to WriteString(x + y) by
// two calls WriteString(x); WriteString(y). This is more efficient because it
// avoids the additional memory allocation produced by string concatenation;
// instead we just write each string into the buffer directly.
//
// It explicitly looks for calls to certain well-known writers such as
// bytes.Buffer, strings.Builder and bufio.Writer. The analyzer will not suggest
// a fix for calls to, say, (*os.File).WriteString, because for certain kinds of
// file such as a UDP socket, it could split a single message into two.
// Similarly it does not offer fixes when the type of the writer is unknown (as
// in calls to io.WriteString).
//
// For example:
//
//	func f(a string, b string) string {
//		 var s strings.Builder
//		 s.WriteString(a+b)
//		 return s.String()
//	}
//
// would become:
//
//	func f(a string, b string) string {
//		var s strings.Builder
//		s.WriteString(a)
//		s.WriteString(b)
//		return s.String()
//	}
package writestring
