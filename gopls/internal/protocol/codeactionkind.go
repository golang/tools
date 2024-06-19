// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

// This file defines constants for non-standard CodeActions.

// CodeAction kinds specific to gopls
//
// See tsprotocol.go for LSP standard kinds, including
//
//	"quickfix"
//	"refactor"
//	"refactor.extract"
//	"refactor.inline"
//	"refactor.move"
//	"refactor.rewrite"
//	"source"
//	"source.organizeImports"
//	"source.fixAll"
//	"notebook"
//
// The effects of CodeActionKind on the behavior of VS Code are
// baffling and undocumented. Here's what we have observed.
//
// Clicking on the "Refactor..." menu item shows a submenu of actions
// with kind="refactor.*", and clicking on "Source action..." shows
// actions with kind="source.*". A lightbulb appears in both cases.
// A third menu, "Quick fix...", not found on the usual context
// menu but accessible through the command palette or "⌘.",
// displays code actions of kind "quickfix.*" and "refactor.*".
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
// "Extract", "Inline", "More actions", and "Quick fix".
//
// The special category "source.fixAll" is intended for actions that
// are unambiguously safe to apply so that clients may automatically
// apply all actions matching this category on save. (That said, this
// is not VS Code's default behavior; see editor.codeActionsOnSave.)
//
// TODO(adonovan): the intent of CodeActionKind is a hierarchy. We
// should changes gopls so that we don't create instances of the
// predefined kinds directly, but treat them as interfaces.
//
// For example,
//
//	instead of:		we should create:
//	refactor.extract	refactor.extract.const
//				refactor.extract.var
//				refactor.extract.func
//	refactor.rewrite	refactor.rewrite.fillstruct
//				refactor.rewrite.unusedparam
//	quickfix		quickfix.govulncheck.reset
//				quickfix.govulncheck.upgrade
//
// etc, so that client editors and scripts can be more specific in
// their requests.
//
// This entails that we use a segmented-path matching operator
// instead of == for CodeActionKinds throughout gopls.
// See golang/go#40438 for related discussion.
const (
	GoAssembly    CodeActionKind = "source.assembly"
	GoDoc         CodeActionKind = "source.doc"
	GoFreeSymbols CodeActionKind = "source.freesymbols"
	GoTest        CodeActionKind = "goTest" // TODO(adonovan): rename "source.test"
)

// CodeActionUnknownTrigger indicates that the trigger for a
// CodeAction request is unknown. A missing
// CodeActionContext.TriggerKind should be treated as equivalent.
const CodeActionUnknownTrigger CodeActionTriggerKind = 0
