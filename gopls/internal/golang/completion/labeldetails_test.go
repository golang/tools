// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
)

func TestLabelDetails(t *testing.T) {
	tests := []struct {
		name            string
		kind            protocol.CompletionItemKind
		detail          string
		description     string
		wantNil         bool
		wantDetail      string
		wantDescription string
	}{
		{
			name:       "function drops the func prefix",
			kind:       protocol.FunctionCompletion,
			detail:     "func(format string, a ...any) string",
			wantDetail: "(format string, a ...any) string",
		},
		{
			name:       "method drops the func prefix",
			kind:       protocol.MethodCompletion,
			detail:     "func() error",
			wantDetail: "() error",
		},
		{
			// A named function type has a detail of the same shape as a
			// function, and must keep its prefix.
			name:       "function type keeps the func prefix",
			kind:       protocol.ClassCompletion,
			detail:     "func(int)",
			wantDetail: " func(int)",
		},
		{
			name:       "other kinds gain a leading space",
			kind:       protocol.ConstantCompletion,
			detail:     "int",
			wantDetail: " int",
		},
		{
			name:            "package reports only the path",
			kind:            protocol.ModuleCompletion,
			detail:          `"os"`,
			description:     "os",
			wantDescription: "os",
		},
		{
			name:    "imported package has nothing to add",
			kind:    protocol.ModuleCompletion,
			detail:  `"os"`,
			wantNil: true,
		},
		{
			name:            "import path becomes the description",
			kind:            protocol.FunctionCompletion,
			detail:          "func(v any) ([]byte, error)",
			description:     "encoding/json",
			wantDetail:      "(v any) ([]byte, error)",
			wantDescription: "encoding/json",
		},
		{
			name:    "nothing to report",
			kind:    protocol.TextCompletion,
			wantNil: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := labelDetails(test.kind, test.detail, test.description)
			if test.wantNil {
				if got != nil {
					t.Fatalf("labelDetails(...) = %+v, want nil", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("labelDetails(...) = nil, want non-nil")
			}
			if got.Detail != test.wantDetail {
				t.Errorf("Detail = %q, want %q", got.Detail, test.wantDetail)
			}
			if got.Description != test.wantDescription {
				t.Errorf("Description = %q, want %q", got.Description, test.wantDescription)
			}
		})
	}
}
