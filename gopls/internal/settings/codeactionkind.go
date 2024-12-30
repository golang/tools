// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import "golang.org/x/tools/gopls/internal/protocol"

// This file defines constants for non-standard CodeActions.

// CodeAction kinds specific to gopls
//
// See ../protocol/tsprotocol.go for LSP standard kinds, including
//
//	quickfix
//	refactor
//	refactor.extract
//	refactor.inline
//	refactor.move
//	refactor.rewrite
//	source
//	source.organizeImports
//	source.fixAll
//	notebook
//
// Kinds are hierarchical: "refactor" subsumes "refactor.inline",
// which subsumes "refactor.inline.call". This rule implies that the
// empty string, confusingly named protocol.Empty, subsumes all kinds.
// The "Only" field in a CodeAction request may specify a category
// such as "refactor"; any matching code action will be returned.
//
// All CodeActions returned by gopls use a specific leaf kind such as
// "refactor.inline.call", except for quick fixes, which all use
// "quickfix". TODO(adonovan): perhaps quick fixes should also be
// hierarchical (e.g. quickfix.govulncheck.{reset,upgrade})?
//
// # VS Code
//
// The effects of CodeActionKind on the behavior of VS Code are
// baffling and undocumented. Here's what we have observed.
//
// Clicking on the "Refactor..." menu item shows a submenu of actions
// with kind="refactor.*", and clicking on "Source action..." shows
// actions with kind="source.*". A lightbulb appears in both cases.
//
// A third menu, "Quick fix...", not found on the usual context
// menu but accessible through the command palette or "⌘.",
// does not set the Only field in its request, so the set of
// kinds is determined by how the server interprets the default.
// The LSP 3.18 guidance is that this should be treated
// equivalent to Only=["quickfix"], and that is what gopls
// now does. (If the server responds with more kinds, they will
// be displayed in menu subsections.)
//
// All of these CodeAction requests have triggerkind=Invoked.
//
// Cursor motion also performs a CodeAction request, but with
// triggerkind=Automatic. Even if this returns a mix of action kinds,
// only the "refactor" and "quickfix" actions seem to matter.
// A lightbulb appears if that subset of actions is non-empty, and the
// menu displays them. (This was noisy--see #65167--so gopls now only
// reports diagnostic-associated code actions if kind is Invoked or
// missing.)
//
// None of these CodeAction requests specifies a "kind" restriction;
// the filtering is done on the response, by the client.
//
// In all these menus, VS Code organizes the actions' menu items
// into groups based on their kind, with hardwired captions such as
// "Refactor...", "Extract", "Inline", "More actions", and "Quick fix".
//
// The special category "source.fixAll" is intended for actions that
// are unambiguously safe to apply so that clients may automatically
// apply all actions matching this category on save. (That said, this
// is not VS Code's default behavior; see editor.codeActionsOnSave.)
const (
	// source
	GoAssembly                 protocol.CodeActionKind = "source.assembly"
	GoDoc                      protocol.CodeActionKind = "source.doc"
	GoFreeSymbols              protocol.CodeActionKind = "source.freesymbols"
	GoTest                     protocol.CodeActionKind = "source.test"
	GoToggleCompilerOptDetails protocol.CodeActionKind = "source.toggleCompilerOptDetails"
	AddTest                    protocol.CodeActionKind = "source.addTest"

	// gopls
	GoplsDocFeatures protocol.CodeActionKind = "gopls.doc.features"

	// refactor.rewrite
	RefactorRewriteChangeQuote       protocol.CodeActionKind = "refactor.rewrite.changeQuote"
	RefactorRewriteFillStruct        protocol.CodeActionKind = "refactor.rewrite.fillStruct"
	RefactorRewriteFillSwitch        protocol.CodeActionKind = "refactor.rewrite.fillSwitch"
	RefactorRewriteInvertIf          protocol.CodeActionKind = "refactor.rewrite.invertIf"
	RefactorRewriteJoinLines         protocol.CodeActionKind = "refactor.rewrite.joinLines"
	RefactorRewriteRemoveUnusedParam protocol.CodeActionKind = "refactor.rewrite.removeUnusedParam"
	RefactorRewriteMoveParamLeft     protocol.CodeActionKind = "refactor.rewrite.moveParamLeft"
	RefactorRewriteMoveParamRight    protocol.CodeActionKind = "refactor.rewrite.moveParamRight"
	RefactorRewriteSplitLines        protocol.CodeActionKind = "refactor.rewrite.splitLines"

	// refactor.inline
	RefactorInlineCall protocol.CodeActionKind = "refactor.inline.call"

	// refactor.extract
	RefactorExtractConstant    protocol.CodeActionKind = "refactor.extract.constant"
	RefactorExtractConstantAll protocol.CodeActionKind = "refactor.extract.constant-all"
	RefactorExtractFunction    protocol.CodeActionKind = "refactor.extract.function"
	RefactorExtractMethod      protocol.CodeActionKind = "refactor.extract.method"
	RefactorExtractVariable    protocol.CodeActionKind = "refactor.extract.variable"
	RefactorExtractVariableAll protocol.CodeActionKind = "refactor.extract.variable-all"
	RefactorExtractToNewFile   protocol.CodeActionKind = "refactor.extract.toNewFile"

	// Note: add new kinds to:
	// - the SupportedCodeActions map in default.go
	// - the codeActionProducers table in ../golang/codeaction.go
	// - the docs in ../../doc/features/transformation.md
)
