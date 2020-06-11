// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"regexp"

	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/lsp/diff"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/refactor/satisfy"
	errors "golang.org/x/xerrors"
)

type renamer struct {
	ctx                context.Context
	fset               *token.FileSet
	refs               []*ReferenceInfo
	objsToUpdate       map[types.Object]bool
	hadConflicts       bool
	errors             string
	from, to           string
	satisfyConstraints map[satisfy.Constraint]bool
	packages           map[*types.Package]Package // may include additional packages that are a rdep of pkg
	msets              typeutil.MethodSetCache
	changeMethods      bool
}

type PrepareItem struct {
	Range protocol.Range
	Text  string
}

func PrepareRename(ctx context.Context, s Snapshot, f FileHandle, pp protocol.Position) (*PrepareItem, error) {
	ctx, done := event.Start(ctx, "source.PrepareRename")
	defer done()

	qos, err := qualifiedObjsAtProtocolPos(ctx, s, f, pp)
	if err != nil {
		return nil, err
	}
	node, obj, pkg := qos[0].node, qos[0].obj, qos[0].sourcePkg
	mr, err := posToMappedRange(s.View(), pkg, node.Pos(), node.End())
	if err != nil {
		return nil, err
	}
	rng, err := mr.Range()
	if err != nil {
		return nil, err
	}
	if _, isImport := node.(*ast.ImportSpec); isImport {
		// We're not really renaming the import path.
		rng.End = rng.Start
	}
	return &PrepareItem{
		Range: rng,
		Text:  obj.Name(),
	}, nil
}

// Rename returns a map of TextEdits for each file modified when renaming a given identifier within a package.
func Rename(ctx context.Context, s Snapshot, f FileHandle, pp protocol.Position, newName string) (map[span.URI][]protocol.TextEdit, error) {
	ctx, done := event.Start(ctx, "source.Rename")
	defer done()

	qos, err := qualifiedObjsAtProtocolPos(ctx, s, f, pp)
	if err != nil {
		return nil, err
	}

	obj := qos[0].obj
	pkg := qos[0].pkg

	if obj.Name() == newName {
		return nil, errors.Errorf("old and new names are the same: %s", newName)
	}
	if !isValidIdentifier(newName) {
		return nil, errors.Errorf("invalid identifier to rename: %q", newName)
	}
	if pkg == nil || pkg.IsIllTyped() {
		return nil, errors.Errorf("package for %s is ill typed", f.URI())
	}
	refs, err := references(ctx, s, qos, true)
	if err != nil {
		return nil, err
	}
	r := renamer{
		ctx:          ctx,
		fset:         s.View().Session().Cache().FileSet(),
		refs:         refs,
		objsToUpdate: make(map[types.Object]bool),
		from:         obj.Name(),
		to:           newName,
		packages:     make(map[*types.Package]Package),
	}
	for _, from := range refs {
		r.packages[from.pkg.GetTypes()] = from.pkg
	}

	// Check that the renaming of the identifier is ok.
	for _, ref := range refs {
		r.check(ref.obj)
		if r.hadConflicts { // one error is enough.
			break
		}
	}
	if r.hadConflicts {
		return nil, errors.Errorf(r.errors)
	}

	changes, err := r.update()
	if err != nil {
		return nil, err
	}
	result := make(map[span.URI][]protocol.TextEdit)
	for uri, edits := range changes {
		// These edits should really be associated with FileHandles for maximal correctness.
		// For now, this is good enough.
		fh, err := s.GetFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		data, err := fh.Read()
		if err != nil {
			return nil, err
		}
		converter := span.NewContentConverter(uri.Filename(), data)
		m := &protocol.ColumnMapper{
			URI:       uri,
			Converter: converter,
			Content:   data,
		}
		// Sort the edits first.
		diff.SortTextEdits(edits)
		protocolEdits, err := ToProtocolEdits(m, edits)
		if err != nil {
			return nil, err
		}
		result[uri] = protocolEdits
	}
	return result, nil
}

// Rename all references to the identifier.
func (r *renamer) update() (map[span.URI][]diff.TextEdit, error) {
	result := make(map[span.URI][]diff.TextEdit)
	seen := make(map[span.Span]bool)

	docRegexp, err := regexp.Compile(`\b` + r.from + `\b`)
	if err != nil {
		return nil, err
	}
	for _, ref := range r.refs {
		refSpan, err := ref.spanRange.Span()
		if err != nil {
			return nil, err
		}
		if seen[refSpan] {
			continue
		}
		seen[refSpan] = true

		// Renaming a types.PkgName may result in the addition or removal of an identifier,
		// so we deal with this separately.
		if pkgName, ok := ref.obj.(*types.PkgName); ok && ref.isDeclaration {
			edit, err := r.updatePkgName(pkgName)
			if err != nil {
				return nil, err
			}
			result[refSpan.URI()] = append(result[refSpan.URI()], *edit)
			continue
		}

		// Replace the identifier with r.to.
		edit := diff.TextEdit{
			Span:    refSpan,
			NewText: r.to,
		}

		result[refSpan.URI()] = append(result[refSpan.URI()], edit)

		if !ref.isDeclaration || ref.ident == nil { // uses do not have doc comments to update.
			continue
		}

		doc := r.docComment(ref.pkg, ref.ident)
		if doc == nil {
			continue
		}

		// Perform the rename in doc comments declared in the original package.
		for _, comment := range doc.List {
			for _, locs := range docRegexp.FindAllStringIndex(comment.Text, -1) {
				rng := span.NewRange(r.fset, comment.Pos()+token.Pos(locs[0]), comment.Pos()+token.Pos(locs[1]))
				spn, err := rng.Span()
				if err != nil {
					return nil, err
				}
				result[spn.URI()] = append(result[spn.URI()], diff.TextEdit{
					Span:    spn,
					NewText: r.to,
				})
			}
		}
	}

	return result, nil
}

// docComment returns the doc for an identifier.
func (r *renamer) docComment(pkg Package, id *ast.Ident) *ast.CommentGroup {
	_, nodes, _ := pathEnclosingInterval(r.fset, pkg, id.Pos(), id.End())
	for _, node := range nodes {
		switch decl := node.(type) {
		case *ast.FuncDecl:
			return decl.Doc
		case *ast.Field:
			return decl.Doc
		case *ast.GenDecl:
			return decl.Doc
		// For {Type,Value}Spec, if the doc on the spec is absent,
		// search for the enclosing GenDecl
		case *ast.TypeSpec:
			if decl.Doc != nil {
				return decl.Doc
			}
		case *ast.ValueSpec:
			if decl.Doc != nil {
				return decl.Doc
			}
		case *ast.Ident:
		default:
			return nil
		}
	}
	return nil
}

// updatePkgName returns the updates to rename a pkgName in the import spec
func (r *renamer) updatePkgName(pkgName *types.PkgName) (*diff.TextEdit, error) {
	// Modify ImportSpec syntax to add or remove the Name as needed.
	pkg := r.packages[pkgName.Pkg()]
	_, path, _ := pathEnclosingInterval(r.fset, pkg, pkgName.Pos(), pkgName.Pos())
	if len(path) < 2 {
		return nil, errors.Errorf("no path enclosing interval for %s", pkgName.Name())
	}
	spec, ok := path[1].(*ast.ImportSpec)
	if !ok {
		return nil, errors.Errorf("failed to update PkgName for %s", pkgName.Name())
	}

	var astIdent *ast.Ident // will be nil if ident is removed
	if pkgName.Imported().Name() != r.to {
		// ImportSpec.Name needed
		astIdent = &ast.Ident{NamePos: spec.Path.Pos(), Name: r.to}
	}

	// Make a copy of the ident that just has the name and path.
	updated := &ast.ImportSpec{
		Name:   astIdent,
		Path:   spec.Path,
		EndPos: spec.EndPos,
	}

	rng := span.NewRange(r.fset, spec.Pos(), spec.End())
	spn, err := rng.Span()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	format.Node(&buf, r.fset, updated)
	newText := buf.String()

	return &diff.TextEdit{
		Span:    spn,
		NewText: newText,
	}, nil
}
