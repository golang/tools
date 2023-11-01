// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tests

import (
	"fmt"

	"golang.org/x/tools/gopls/internal/lsp/protocol"
)

// DiffCallHierarchyItems returns the diff between expected and actual call locations for incoming/outgoing call hierarchies
func DiffCallHierarchyItems(gotCalls []protocol.CallHierarchyItem, expectedCalls []protocol.CallHierarchyItem) string {
	expected := make(map[protocol.Location]bool)
	for _, call := range expectedCalls {
		expected[protocol.Location{URI: call.URI, Range: call.Range}] = true
	}

	got := make(map[protocol.Location]bool)
	for _, call := range gotCalls {
		got[protocol.Location{URI: call.URI, Range: call.Range}] = true
	}
	if len(got) != len(expected) {
		return fmt.Sprintf("expected %d calls but got %d", len(expected), len(got))
	}
	for spn := range got {
		if !expected[spn] {
			return fmt.Sprintf("incorrect calls, expected locations %v but got locations %v", expected, got)
		}
	}
	return ""
}
