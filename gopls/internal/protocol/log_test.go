// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/tools/internal/jsonrpc2"
)

// TestIssue42157 tests that error responses are properly formatted in LSP trace
// logs and that pending call entries are cleared from memory.
// See golang.org/issue/42157.
func TestIssue42157(t *testing.T) {
	var buf bytes.Buffer
	s := &loggingStream{log: &buf}

	call, _ := jsonrpc2.NewCall(jsonrpc2.NewIntID(8), "textDocument/codeAction", nil)
	s.logCommon(call, true) // simulate receiving request from client

	resp, _ := jsonrpc2.NewResponse(jsonrpc2.NewIntID(8), nil, jsonrpc2.NewError(0, "no packages returned: packages.Load error"))
	s.logCommon(resp, false) // simulate sending error response to client

	got := buf.String()
	for _, want := range []string{
		"Sending request 'textDocument/codeAction - (8)'.",
		"Received response 'textDocument/codeAction - (8)' in ",
		"Request failed: no packages returned: packages.Load error (0).",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log output missing %q, got:\n%s", want, got)
		}
	}

	maps.mu.Lock()
	defer maps.mu.Unlock()
	if len(maps.clientCalls) != 0 || len(maps.serverCalls) != 0 {
		t.Errorf("pending calls not cleaned up: client=%d, server=%d", len(maps.clientCalls), len(maps.serverCalls))
	}
}
