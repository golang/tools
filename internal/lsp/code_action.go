// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"
	"strings"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
)

func (s *Server) codeAction(ctx context.Context, params *protocol.CodeActionParams) ([]protocol.CodeAction, error) {
	uri := span.NewURI(params.TextDocument.URI)
	view := s.session.ViewOf(uri)
	gof, m, err := getGoFile(ctx, view, uri)
	if err != nil {
		return nil, err
	}
	spn, err := m.RangeSpan(params.Range)
	if err != nil {
		return nil, err
	}
	var codeActions []protocol.CodeAction
	// TODO(rstambler): Handle params.Context.Only when VSCode-Go uses a
	// version of vscode-languageclient that fixes
	// https://github.com/Microsoft/vscode-languageserver-node/issues/442.
	edits, err := organizeImports(ctx, view, spn)
	if err != nil {
		return nil, err
	}
	if len(edits) > 0 {
		codeActions = append(codeActions, protocol.CodeAction{
			Title: "Organize Imports",
			Kind:  protocol.SourceOrganizeImports,
			Edit: &protocol.WorkspaceEdit{
				Changes: &map[string][]protocol.TextEdit{
					string(spn.URI()): edits,
				},
			},
		})
		// If we also have diagnostics, we can associate them with quick fixes.
		if findImportErrors(params.Context.Diagnostics) {
			// TODO(rstambler): Separate this into a set of codeActions per diagnostic,
			// where each action is the addition or removal of one import.
			// This can only be done when https://golang.org/issue/31493 is resolved.
			codeActions = append(codeActions, protocol.CodeAction{
				Title: "Organize All Imports", // clarify that all imports will change
				Kind:  protocol.QuickFix,
				Edit: &protocol.WorkspaceEdit{
					Changes: &map[string][]protocol.TextEdit{
						string(uri): edits,
					},
				},
			})
		}
		diags := gof.GetPackage(ctx).GetDiagnostics()
		for _, diag := range diags {
			pdiag, err := toProtocolDiagnostic(ctx, view, diag)
			if err != nil {
				return nil, err
			}
			for _, ca := range diag.SuggestedFixes {
				codeActions = append(codeActions, protocol.CodeAction{
					Title: ca.Title,
					Kind:  protocol.QuickFix, // TODO(matloob): Be more accurate about these?
					Edit: &protocol.WorkspaceEdit{
						Changes: &map[string][]protocol.TextEdit{
							string(spn.URI()): edits,
						},
					},
					Diagnostics: []protocol.Diagnostic{pdiag},
				})
			}
		}
	}
	return codeActions, nil
}

func organizeImports(ctx context.Context, view source.View, s span.Span) ([]protocol.TextEdit, error) {
	f, m, rng, err := spanToRange(ctx, view, s)
	if err != nil {
		return nil, err
	}
	edits, err := source.Imports(ctx, f, rng)
	if err != nil {
		return nil, err
	}
	return ToProtocolEdits(m, edits)
}

// findImports determines if a given diagnostic represents an error that could
// be fixed by organizing imports.
// TODO(rstambler): We need a better way to check this than string matching.
func findImportErrors(diagnostics []protocol.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		// "undeclared name: X" may be an unresolved import.
		if strings.HasPrefix(diagnostic.Message, "undeclared name: ") {
			return true
		}
		// "could not import: X" may be an invalid import.
		if strings.HasPrefix(diagnostic.Message, "could not import: ") {
			return true
		}
		// "X imported but not used" is an unused import.
		if strings.HasSuffix(diagnostic.Message, " imported but not used") {
			return true
		}
	}
	return false
}
