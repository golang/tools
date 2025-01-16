// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package astutil

import (
	"go/ast"
	"strings"
)

// Deprecation returns the paragraph of the doc comment that starts with the
// conventional "Deprecation: " marker, as defined by
// https://go.dev/wiki/Deprecated, or "" if the documented symbol is not
// deprecated.
func Deprecation(doc *ast.CommentGroup) string {
	for _, p := range strings.Split(doc.Text(), "\n\n") {
		// There is still some ambiguity for deprecation message. This function
		// only returns the paragraph introduced by "Deprecated: ". More
		// information related to the deprecation may follow in additional
		// paragraphs, but the deprecation message should be able to stand on
		// its own. See golang/go#38743.
		if strings.HasPrefix(p, "Deprecated: ") {
			return p
		}
	}
	return ""
}
