// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
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
	"golang.org/x/tools/gopls/internal/util/safetoken"
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
		return fixer1(pkg.FileSet(), start, end, pgf.Src, pgf.File, pkg.Types(), pkg.TypesInfo())
	}
}

// Names of ApplyFix.Fix created directly by the CodeAction handler.
const (
	fixExtractVariable   = "extract_variable"
	fixExtractFunction   = "extract_function"
	fixExtractInterface  = "extract_interface"
	fixExtractMethod     = "extract_method"
	fixInlineCall        = "inline_call"
	fixInvertIfCondition = "invert_if_condition"
	fixSplitLines        = "split_lines"
	fixJoinLines         = "join_lines"
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
func ApplyFix(ctx context.Context, fix string, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) ([]*protocol.TextDocumentEdit, error) {
	// This can't be expressed as an entry in the fixer table below
	// because it operates in the protocol (not go/{token,ast}) domain.
	// (Sigh; perhaps it was a mistake to factor out the
	// NarrowestPackageForFile/RangePos/suggestedFixToEdits
	// steps.)
	if fix == unusedparams.FixCategory {
		return RemoveUnusedParameter(ctx, fh, rng, snapshot)
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
		fixExtractInterface:  extractInterface,
		fixExtractFunction:   singleFile(extractFunction),
		fixExtractMethod:     singleFile(extractMethod),
		fixExtractVariable:   singleFile(extractVariable),
		fixInlineCall:        inlineCall,
		fixInvertIfCondition: singleFile(invertIfCondition),
		fixSplitLines:        singleFile(splitLines),
		fixJoinLines:         singleFile(joinLines),
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

func extractInterface(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	path, _ := astutil.PathEnclosingInterval(pgf.File, start, end)

	var field *ast.Field
	var decl ast.Decl
	for _, node := range path {
		if f, ok := node.(*ast.Field); ok {
			field = f
			continue
		}

		// Record the node that starts the declaration of the type that contains
		// the field we are creating the interface for.
		if d, ok := node.(ast.Decl); ok {
			decl = d
			break // we have both the field and the declaration
		}
	}

	if field == nil || decl == nil {
		return nil, nil, nil
	}

	p := safetoken.StartPosition(pkg.FileSet(), field.Pos())
	pos := protocol.Position{
		Line:      uint32(p.Line - 1),   // Line is zero-based
		Character: uint32(p.Column - 1), // Character is zero-based
	}

	fh, err := snapshot.ReadFile(ctx, pgf.URI)
	if err != nil {
		return nil, nil, err
	}

	refs, err := references(ctx, snapshot, fh, pos, false)
	if err != nil {
		return nil, nil, err
	}

	type method struct {
		signature *types.Signature
		name      string
	}

	var methods []method
	for _, ref := range refs {
		locPkg, locPgf, err := NarrowestPackageForFile(ctx, snapshot, ref.location.URI)
		if err != nil {
			return nil, nil, err
		}

		_, end, err := locPgf.RangePos(ref.location.Range)
		if err != nil {
			return nil, nil, err
		}

		// We are interested in the method call, so we need the node after the dot
		rangeEnd := end + token.Pos(len("."))
		path, _ := astutil.PathEnclosingInterval(locPgf.File, rangeEnd, rangeEnd)
		id, ok := path[0].(*ast.Ident)
		if !ok {
			continue
		}

		obj := locPkg.TypesInfo().ObjectOf(id)
		if obj == nil {
			continue
		}

		sig, ok := obj.Type().(*types.Signature)
		if !ok {
			return nil, nil, errors.New("cannot extract interface with non-method accesses")
		}

		fc := method{signature: sig, name: obj.Name()}
		if !slices.Contains(methods, fc) {
			methods = append(methods, fc)
		}
	}

	interfaceName := "I" + pkg.TypesInfo().ObjectOf(field.Names[0]).Name()
	var buf bytes.Buffer
	buf.WriteString("\ntype ")
	buf.WriteString(interfaceName)
	buf.WriteString(" interface {\n")
	for _, fc := range methods {
		buf.WriteString("\t")
		buf.WriteString(fc.name)
		types.WriteSignature(&buf, fc.signature, relativeTo(pkg.Types()))
		buf.WriteByte('\n')
	}
	buf.WriteByte('}')
	buf.WriteByte('\n')

	interfacePos := decl.Pos() - 1
	// Move the interface above the documentation comment if the type declaration
	// includes one.
	switch d := decl.(type) {
	case *ast.GenDecl:
		if d.Doc != nil {
			interfacePos = d.Doc.Pos() - 1
		}
	case *ast.FuncDecl:
		if d.Doc != nil {
			interfacePos = d.Doc.Pos() - 1
		}
	}

	return pkg.FileSet(), &analysis.SuggestedFix{
		Message: "Extract interface",
		TextEdits: []analysis.TextEdit{{
			Pos:     interfacePos,
			End:     interfacePos,
			NewText: buf.Bytes(),
		}, {
			Pos:     field.Type.Pos(),
			End:     field.Type.End(),
			NewText: []byte(interfaceName),
		}},
	}, nil
}

func relativeTo(pkg *types.Package) types.Qualifier {
	if pkg == nil {
		return nil
	}
	return func(other *types.Package) string {
		if pkg == other {
			return "" // same package; unqualified
		}
		return other.Name()
	}
}

// suggestedFixToEdits converts the suggestion's edits from analysis form into protocol form.
func suggestedFixToEdits(ctx context.Context, snapshot *cache.Snapshot, fset *token.FileSet, suggestion *analysis.SuggestedFix) ([]*protocol.TextDocumentEdit, error) {
	type fileInfo struct {
		fh     file.Handle
		mapper *protocol.Mapper
		edits  []protocol.TextEdit
	}
	files := make(map[protocol.DocumentURI]*fileInfo)
	for _, edit := range suggestion.TextEdits {
		tokFile := fset.File(edit.Pos)
		if tokFile == nil {
			return nil, bug.Errorf("no file for edit position")
		}
		end := edit.End
		if !end.IsValid() {
			end = edit.Pos
		}
		uri := protocol.URIFromPath(tokFile.Name())
		info, ok := files[uri]
		if !ok {
			// First edit: create a mapper.
			fh, err := snapshot.ReadFile(ctx, uri)
			if err != nil {
				return nil, err
			}
			content, err := fh.Content()
			if err != nil {
				return nil, err
			}
			mapper := protocol.NewMapper(uri, content)
			info = &fileInfo{fh, mapper, nil}
			files[uri] = info
		}
		rng, err := info.mapper.PosRange(tokFile, edit.Pos, end)
		if err != nil {
			return nil, err
		}
		info.edits = append(info.edits, protocol.TextEdit{
			Range:   rng,
			NewText: string(edit.NewText),
		})
	}
	var docedits []*protocol.TextDocumentEdit
	for _, info := range files {
		docedits = append(docedits, protocol.NewTextDocumentEdit(info.fh, info.edits))
	}
	return docedits, nil
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
