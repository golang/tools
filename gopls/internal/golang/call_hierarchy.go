// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/typesinternal"
)

// PrepareCallHierarchy returns an array of CallHierarchyItem for a file and the position within the file.
func PrepareCallHierarchy(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, pp protocol.Position) ([]protocol.CallHierarchyItem, error) {
	ctx, done := event.Start(ctx, "golang.PrepareCallHierarchy")
	defer done()

	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	pos, err := pgf.PositionPos(pp)
	if err != nil {
		return nil, err
	}

	_, obj, _ := referencedObject(pkg, pgf, pos)
	if obj == nil {
		return nil, nil
	}

	if _, ok := obj.Type().Underlying().(*types.Signature); !ok {
		return nil, nil
	}

	declLoc, err := ObjectLocation(ctx, pkg.FileSet(), snapshot, obj)
	if err != nil {
		return nil, err
	}
	rng := declLoc.Range

	callHierarchyItem := protocol.CallHierarchyItem{
		Name:           obj.Name(),
		Kind:           protocol.Function,
		Tags:           []protocol.SymbolTag{},
		Detail:         fmt.Sprintf("%s • %s", obj.Pkg().Path(), declLoc.URI.Base()),
		URI:            declLoc.URI,
		Range:          rng,
		SelectionRange: rng,
	}
	return []protocol.CallHierarchyItem{callHierarchyItem}, nil
}

// IncomingCalls returns an array of CallHierarchyIncomingCall for a file and the position within the file.
func IncomingCalls(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, pos protocol.Position) ([]protocol.CallHierarchyIncomingCall, error) {
	ctx, done := event.Start(ctx, "golang.IncomingCalls")
	defer done()

	refs, err := references(ctx, snapshot, fh, pos, false)
	if err != nil {
		if errors.Is(err, ErrNoIdentFound) || errors.Is(err, errNoObjectFound) {
			return nil, nil
		}
		return nil, err
	}

	// Group references by their enclosing function declaration.
	incomingCalls := make(map[protocol.Location]*protocol.CallHierarchyIncomingCall)
	for _, ref := range refs {
		callItem, err := enclosingNodeCallItem(ctx, snapshot, ref.pkgPath, ref.location)
		if err != nil {
			event.Error(ctx, fmt.Sprintf("error getting enclosing node for %q", ref.pkgPath), err)
			continue
		}
		loc := callItem.URI.Location(callItem.Range)
		call, ok := incomingCalls[loc]
		if !ok {
			call = &protocol.CallHierarchyIncomingCall{From: callItem}
			incomingCalls[loc] = call
		}
		call.FromRanges = append(call.FromRanges, ref.location.Range)
	}

	// Flatten the map of pointers into a slice of values.
	incomingCallItems := make([]protocol.CallHierarchyIncomingCall, 0, len(incomingCalls))
	for _, callItem := range moremaps.SortedFunc(incomingCalls, protocol.CompareLocation) {
		incomingCallItems = append(incomingCallItems, *callItem)
	}
	return incomingCallItems, nil
}

// enclosingNodeCallItem creates a CallHierarchyItem representing the function call at loc.
func enclosingNodeCallItem(ctx context.Context, snapshot *cache.Snapshot, pkgPath PackagePath, loc protocol.Location) (protocol.CallHierarchyItem, error) {
	// Parse the file containing the reference.
	fh, err := snapshot.ReadFile(ctx, loc.URI)
	if err != nil {
		return protocol.CallHierarchyItem{}, err
	}
	// TODO(adonovan): opt: before parsing, trim the bodies of functions
	// that don't contain the reference, using either a scanner-based
	// implementation such as https://go.dev/play/p/KUrObH1YkX8
	// (~31% speedup), or a byte-oriented implementation (2x speedup).
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return protocol.CallHierarchyItem{}, err
	}
	start, end, err := pgf.RangePos(loc.Range)
	if err != nil {
		return protocol.CallHierarchyItem{}, err
	}

	// Find the enclosing named function, if any.
	//
	// It is tempting to treat anonymous functions as nodes in the
	// call hierarchy, and historically we used to do that,
	// poorly; see #64451. However, it is impossible to track
	// references to anonymous functions without much deeper
	// analysis. Local analysis is tractable, but ultimately it
	// can only detect calls from the outer function to the inner
	// function.
	//
	// It is simpler and clearer to treat the top-level named
	// function and all its nested functions as one entity, and it
	// allows users to recursively expand the tree where, before,
	// the chain would be broken by each lambda.
	//
	// If the selection is in a global var initializer,
	// default to the file's package declaration.
	path, _ := astutil.PathEnclosingInterval(pgf.File, start, end)
	var (
		name = pgf.File.Name.Name
		kind = protocol.Package
	)
	start, end = pgf.File.Name.Pos(), pgf.File.Name.End()
	for _, node := range path {
		switch node := node.(type) {
		case *ast.FuncDecl:
			name = node.Name.Name
			start, end = node.Name.Pos(), node.Name.End()
			kind = protocol.Function

		case *ast.FuncLit:
			// If the call comes from a FuncLit with
			// no enclosing FuncDecl, then use the
			// FuncLit's extent.
			name = "func"
			start, end = node.Pos(), node.Type.End() // signature, sans body
			kind = protocol.Function

		case *ast.ValueSpec:
			// If the call comes from a var (or,
			// theoretically, const) initializer outside
			// any function, then use the ValueSpec.Names span.
			name = "init"
			start, end = node.Names[0].Pos(), node.Names[len(node.Names)-1].End()
			kind = protocol.Variable
		}
	}

	rng, err := pgf.PosRange(start, end)
	if err != nil {
		return protocol.CallHierarchyItem{}, err
	}

	return protocol.CallHierarchyItem{
		Name:           name,
		Kind:           kind,
		Tags:           []protocol.SymbolTag{},
		Detail:         fmt.Sprintf("%s • %s", pkgPath, fh.URI().Base()),
		URI:            loc.URI,
		Range:          rng,
		SelectionRange: rng,
	}, nil
}

// OutgoingCalls returns an array of CallHierarchyOutgoingCall for a file and the position within the file.
func OutgoingCalls(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, pp protocol.Position) ([]protocol.CallHierarchyOutgoingCall, error) {
	ctx, done := event.Start(ctx, "golang.OutgoingCalls")
	defer done()

	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	pos, err := pgf.PositionPos(pp)
	if err != nil {
		return nil, err
	}

	_, obj, _ := referencedObject(pkg, pgf, pos)
	if obj == nil {
		return nil, nil
	}

	if _, ok := obj.Type().Underlying().(*types.Signature); !ok {
		return nil, nil
	}

	if isBuiltin(obj) {
		return nil, nil // built-ins have no position
	}

	declFile := pkg.FileSet().File(obj.Pos())
	if declFile == nil {
		return nil, bug.Errorf("file not found for %d", obj.Pos())
	}

	uri := protocol.URIFromPath(declFile.Name())
	offset, err := safetoken.Offset(declFile, obj.Pos())
	if err != nil {
		return nil, err
	}

	declPkg, declPGF, err := NarrowestPackageForFile(ctx, snapshot, uri)
	if err != nil {
		return nil, err
	}

	declPos, err := safetoken.Pos(declPGF.Tok, offset)
	if err != nil {
		return nil, err
	}

	declNode, _, _ := findDeclInfo([]*ast.File{declPGF.File}, declPos)
	if declNode == nil {
		// TODO(rfindley): why don't we return an error here, or even bug.Errorf?
		return nil, nil
		// return nil, bug.Errorf("failed to find declaration for object %s.%s", obj.Pkg().Path(), obj.Name())
	}

	type callRange struct {
		start, end token.Pos
	}

	// Find calls to known functions/methods, including interface methods.
	var callRanges []callRange
	for n := range ast.Preorder(declNode) {
		if call, ok := n.(*ast.CallExpr); ok &&
			is[*types.Func](typeutil.Callee(pkg.TypesInfo(), call)) {
			id := typesinternal.UsedIdent(pkg.TypesInfo(), call.Fun)
			callRanges = append(callRanges, callRange{
				start: id.NamePos,
				end:   call.Lparen,
			})
		}
	}

	outgoingCalls := make(map[protocol.Location]*protocol.CallHierarchyOutgoingCall)
	for _, callRange := range callRanges {
		_, obj, _ := referencedObject(declPkg, declPGF, callRange.start)
		if obj == nil {
			continue
		}

		loc, err := ObjectLocation(ctx, declPkg.FileSet(), snapshot, obj)
		if err != nil {
			return nil, err
		}

		outgoingCall, ok := outgoingCalls[loc]
		if !ok {
			outgoingCall = &protocol.CallHierarchyOutgoingCall{
				To: protocol.CallHierarchyItem{
					Name:           obj.Name(),
					Kind:           protocol.Function,
					Tags:           []protocol.SymbolTag{},
					Detail:         fmt.Sprintf("%s • %s", obj.Pkg().Path(), loc.URI.Base()),
					URI:            loc.URI,
					Range:          loc.Range,
					SelectionRange: loc.Range,
				},
			}
			outgoingCalls[loc] = outgoingCall
		}

		rng, err := declPGF.PosRange(callRange.start, callRange.end)
		if err != nil {
			return nil, err
		}
		outgoingCall.FromRanges = append(outgoingCall.FromRanges, rng)
	}

	outgoingCallItems := make([]protocol.CallHierarchyOutgoingCall, 0, len(outgoingCalls))
	for _, callItem := range moremaps.SortedFunc(outgoingCalls, protocol.CompareLocation) {
		outgoingCallItems = append(outgoingCallItems, *callItem)
	}
	return outgoingCallItems, nil
}
