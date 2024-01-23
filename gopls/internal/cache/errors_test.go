// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/protocol"
)

func TestParseErrorMessage(t *testing.T) {
	tests := []struct {
		name             string
		in               string
		expectedFileName string
		expectedLine     int
		expectedColumn   int
	}{
		{
			name:             "from go list output",
			in:               "\nattributes.go:13:1: expected 'package', found 'type'",
			expectedFileName: "attributes.go",
			expectedLine:     13,
			expectedColumn:   1,
		},
		{
			name:             "windows driver letter",
			in:               "C:\\foo\\bar.go:13: message",
			expectedFileName: "bar.go",
			expectedLine:     13,
			expectedColumn:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn, line, col8 := parseGoListError(packages.Error{Msg: tt.in}, ".")

			if !strings.HasSuffix(fn, tt.expectedFileName) {
				t.Errorf("expected filename with suffix %v but got %v", tt.expectedFileName, fn)
			}
			if line != tt.expectedLine {
				t.Errorf("expected line %v but got %v", tt.expectedLine, line)
			}
			if col8 != tt.expectedColumn {
				t.Errorf("expected col %v but got %v", tt.expectedLine, col8)
			}
		})
	}
}

func TestDiagnosticEncoding(t *testing.T) {
	diags := []*Diagnostic{
		{}, // empty
		{
			URI: "file///foo",
			Range: protocol.Range{
				Start: protocol.Position{Line: 4, Character: 2},
				End:   protocol.Position{Line: 6, Character: 7},
			},
			Severity: protocol.SeverityWarning,
			Code:     "red",
			CodeHref: "https://go.dev",
			Source:   "test",
			Message:  "something bad happened",
			Tags:     []protocol.DiagnosticTag{81},
			Related: []protocol.DiagnosticRelatedInformation{
				{
					Location: protocol.Location{
						URI: "file:///other",
						Range: protocol.Range{
							Start: protocol.Position{Line: 3, Character: 6},
							End:   protocol.Position{Line: 4, Character: 9},
						},
					},
					Message: "psst, over here",
				},
			},

			// Fields below are used internally to generate quick fixes. They aren't
			// part of the LSP spec and don't leave the server.
			SuggestedFixes: []SuggestedFix{
				{
					Title: "fix it!",
					Edits: map[protocol.DocumentURI][]protocol.TextEdit{
						"file:///foo": {{
							Range: protocol.Range{
								Start: protocol.Position{Line: 4, Character: 2},
								End:   protocol.Position{Line: 6, Character: 7},
							},
							NewText: "abc",
						}},
						"file:///other": {{
							Range: protocol.Range{
								Start: protocol.Position{Line: 4, Character: 2},
								End:   protocol.Position{Line: 6, Character: 7},
							},
							NewText: "!@#!",
						}},
					},
					Command: &protocol.Command{
						Title:     "run a command",
						Command:   "gopls.fix",
						Arguments: []json.RawMessage{json.RawMessage(`{"a":1}`)},
					},
					ActionKind: protocol.QuickFix,
				},
			},
		},
		{
			URI: "file//bar",
			// other fields tested above
		},
	}

	data := encodeDiagnostics(diags)
	diags2 := decodeDiagnostics(data)

	if diff := cmp.Diff(diags, diags2); diff != "" {
		t.Errorf("decoded diagnostics do not match (-original +decoded):\n%s", diff)
	}
}
