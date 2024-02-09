// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/gopls/internal/analysis/embeddirective"
	"golang.org/x/tools/gopls/internal/analysis/fillstruct"
	"golang.org/x/tools/gopls/internal/analysis/stubmethods"
	"golang.org/x/tools/gopls/internal/analysis/undeclaredname"
	"golang.org/x/tools/gopls/internal/analysis/unusedparams"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/imports"
)

// A fixer is a function that suggests a fix for a diagnostic produced
// by the analysis framework. This is done outside of the analyzer Run
// function so that the construction of expensive fixes can be
// deferred until they are requested by the user.
//
// The actual diagnostic is not provided; only its position, as the
// triple (pgf, start, end); the resulting SuggestedFix implicitly
// relates to that file.
//
// The supplied token positions (start, end) must belong to
// pkg.FileSet(), and the returned positions
// (SuggestedFix.TextEdits[*].{Pos,End}) must belong to the returned
// FileSet.
//
// A fixer may return (nil, nil) if no fix is available.
type fixer func(ctx context.Context, s *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error)

// A singleFileFixer is a Fixer that inspects only a single file,
// and does not depend on data types from the cache package.
//
// TODO(adonovan): move fillstruct and undeclaredname into this
// package, so we can remove the import restriction and push
// the singleFile wrapper down into each singleFileFixer?
type singleFileFixer func(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, pkg *types.Package, info *types.Info) (*token.FileSet, *analysis.SuggestedFix, error)

// singleFile adapts a single-file fixer to a Fixer.
func singleFile(fixer1 singleFileFixer) fixer {
	return func(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
		return fixer1(pkg.FileSet(), start, end, pgf.Src, pgf.File, pkg.GetTypes(), pkg.GetTypesInfo())
	}
}

// Names of ApplyFix.Fix created directly by the CodeAction handler.
const (
	fixExtractVariable   = "extract_variable"
	fixExtractFunction   = "extract_function"
	fixExtractMethod     = "extract_method"
	fixInlineCall        = "inline_call"
	fixInvertIfCondition = "invert_if_condition"
)

// ApplyFix applies the specified kind of suggested fix to the given
// file and range, returning the resulting edits.
//
// A fix kind is either the Category of an analysis.Diagnostic that
// had a SuggestedFix with no edits; or the name of a fix agreed upon
// by [CodeActions] and this function.
// Fix kinds identify fixes in the command protocol.
//
// TODO(adonovan): come up with a better mechanism for registering the
// connection between analyzers, code actions, and fixers. A flaw of
// the current approach is that the same Category could in theory
// apply to a Diagnostic with several lazy fixes, making them
// impossible to distinguish. It would more precise if there was a
// SuggestedFix.Category field, or some other way to squirrel metadata
// in the fix.
func ApplyFix(ctx context.Context, fix string, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) ([]protocol.TextDocumentEdit, error) {
	// This can't be expressed as an entry in the fixer table below
	// because it operates in the protocol (not go/{token,ast}) domain.
	// (Sigh; perhaps it was a mistake to factor out the
	// NarrowestPackageForFile/RangePos/suggestedFixToEdits
	// steps.)
	if fix == unusedparams.FixCategory {
		changes, err := RemoveUnusedParameter(ctx, fh, rng, snapshot)
		if err != nil {
			return nil, err
		}
		// Unwrap TextDocumentEdits again!
		var edits []protocol.TextDocumentEdit
		for _, change := range changes {
			edits = append(edits, *change.TextDocumentEdit)
		}
		return edits, nil
	}

	fixers := map[string]fixer{
		// Fixes for analyzer-provided diagnostics.
		// These match the Diagnostic.Category.
		embeddirective.FixCategory: addEmbedImport,
		fillstruct.FixCategory:     singleFile(fillstruct.SuggestedFix),
		stubmethods.FixCategory:    stubMethodsFixer,
		undeclaredname.FixCategory: singleFile(undeclaredname.SuggestedFix),

		// Ad-hoc fixers: these are used when the command is
		// constructed directly by logic in server/code_action.
		fixExtractFunction:   singleFile(extractFunction),
		fixExtractMethod:     singleFile(extractMethod),
		fixExtractVariable:   singleFile(extractVariable),
		fixInlineCall:        inlineCall,
		fixInvertIfCondition: singleFile(invertIfCondition),
	}
	fixer, ok := fixers[fix]
	if !ok {
		return nil, fmt.Errorf("no suggested fix function for %s", fix)
	}
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, err
	}
	fixFset, suggestion, err := fixer(ctx, snapshot, pkg, pgf, start, end)
	if err != nil {
		return nil, err
	}
	if suggestion == nil {
		return nil, nil
	}
	return suggestedFixToEdits(ctx, snapshot, fixFset, suggestion)
}

// suggestedFixToEdits converts the suggestion's edits from analysis form into protocol form.
func suggestedFixToEdits(ctx context.Context, snapshot *cache.Snapshot, fset *token.FileSet, suggestion *analysis.SuggestedFix) ([]protocol.TextDocumentEdit, error) {
	editsPerFile := map[protocol.DocumentURI]*protocol.TextDocumentEdit{}
	for _, edit := range suggestion.TextEdits {
		tokFile := fset.File(edit.Pos)
		if tokFile == nil {
			return nil, bug.Errorf("no file for edit position")
		}
		end := edit.End
		if !end.IsValid() {
			end = edit.Pos
		}
		fh, err := snapshot.ReadFile(ctx, protocol.URIFromPath(tokFile.Name()))
		if err != nil {
			return nil, err
		}
		te, ok := editsPerFile[fh.URI()]
		if !ok {
			te = &protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
					Version: fh.Version(),
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{
						URI: fh.URI(),
					},
				},
			}
			editsPerFile[fh.URI()] = te
		}
		content, err := fh.Content()
		if err != nil {
			return nil, err
		}
		m := protocol.NewMapper(fh.URI(), content) // TODO(adonovan): opt: memoize in map
		rng, err := m.PosRange(tokFile, edit.Pos, end)
		if err != nil {
			return nil, err
		}
		te.Edits = append(te.Edits, protocol.Or_TextDocumentEdit_edits_Elem{
			Value: protocol.TextEdit{
				Range:   rng,
				NewText: string(edit.NewText),
			},
		})
	}
	var edits []protocol.TextDocumentEdit
	for _, edit := range editsPerFile {
		edits = append(edits, *edit)
	}
	return edits, nil
}

// addEmbedImport adds a missing embed "embed" import with blank name.
func addEmbedImport(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, _, _ token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	// Like golang.AddImport, but with _ as Name and using our pgf.
	protoEdits, err := ComputeOneImportFixEdits(snapshot, pgf, &imports.ImportFix{
		StmtInfo: imports.ImportInfo{
			ImportPath: "embed",
			Name:       "_",
		},
		FixType: imports.AddImport,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("compute edits: %w", err)
	}

	var edits []analysis.TextEdit
	for _, e := range protoEdits {
		start, end, err := pgf.RangePos(e.Range)
		if err != nil {
			return nil, nil, err // e.g. invalid range
		}
		edits = append(edits, analysis.TextEdit{
			Pos:     start,
			End:     end,
			NewText: []byte(e.NewText),
		})
	}

	return pkg.FileSet(), &analysis.SuggestedFix{
		Message:   "Add embed import",
		TextEdits: edits,
	}, nil
}
