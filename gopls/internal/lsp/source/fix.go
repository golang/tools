// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/gopls/internal/analysis/fillstruct"
	"golang.org/x/tools/gopls/internal/analysis/undeclaredname"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/cache/parsego"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/imports"
)

// A Fixer is a function that suggests a fix for a diagnostic produced
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
// A Fixer may return (nil, nil) if no fix is available.
type Fixer func(ctx context.Context, s *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error)

// fixers maps each Fix id to its Fixer function.
var fixers = map[settings.Fix]Fixer{
	settings.AddEmbedImport:    addEmbedImport,
	settings.ExtractFunction:   singleFile(extractFunction),
	settings.ExtractMethod:     singleFile(extractMethod),
	settings.ExtractVariable:   singleFile(extractVariable),
	settings.FillStruct:        singleFile(fillstruct.SuggestedFix),
	settings.InlineCall:        inlineCall,
	settings.InvertIfCondition: singleFile(invertIfCondition),
	settings.StubMethods:       stubMethodsFixer,
	settings.UndeclaredName:    singleFile(undeclaredname.SuggestedFix),
}

// A singleFileFixer is a Fixer that inspects only a single file,
// and does not depend on data types from the cache package.
//
// TODO(adonovan): move fillstruct and undeclaredname into this
// package, so we can remove the import restriction and push
// the singleFile wrapper down into each singleFileFixer?
type singleFileFixer func(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, pkg *types.Package, info *types.Info) (*token.FileSet, *analysis.SuggestedFix, error)

// singleFile adapts a single-file fixer to a Fixer.
func singleFile(fixer singleFileFixer) Fixer {
	return func(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
		return fixer(pkg.FileSet(), start, end, pgf.Src, pgf.File, pkg.GetTypes(), pkg.GetTypesInfo())
	}
}

// ApplyFix applies the specified kind of suggested fix to the given
// file and range, returning the resulting edits.
func ApplyFix(ctx context.Context, fix settings.Fix, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) ([]protocol.TextDocumentEdit, error) {
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

// suggestedFixToEdits converts the suggestion's edits from analysis form into protocol form,
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
	// Like source.AddImport, but with _ as Name and using our pgf.
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
