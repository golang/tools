// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/moreiters"
)

// moveDeclarationFormFile asks where to move a declaration, through the file
// picker of the client.
//
// TODO(hxjiang): move form validation logic to here.
var moveDeclarationFormFile = []protocol.FormField{
	{
		ID:          "file",
		Description: "destination file for the moved declaration",
		Type: protocol.FormFieldTypeFile{
			Kind: "file",
		},
		Required: true,
	},
}

// moveDeclarationFormString asks where to move a declaration, as plain text,
// for clients that have no file picker.
var moveDeclarationFormString = []protocol.FormField{
	{
		ID:          "file",
		Description: "destination file uri for the moved declaration, e.g. file:///path/to/file.go",
		Type: protocol.FormFieldTypeFile{
			Kind: "string",
		},
		Required: true,
	},
}

// TODO(mkalil): Find a way to notify users which additional declarations will need to be moved.
func MoveDeclaration(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, destURI protocol.DocumentURI, loc protocol.Location) ([]protocol.DocumentChange, protocol.Location, error) {
	srcPkg, srcPGF, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, protocol.Location{}, err
	}
	var destPkg *cache.Package
	if fh.URI().DirPath() == destURI.DirPath() {
		// Moving within the same package.
		destPkg = srcPkg
	} else {
		destPkg, _, _ = NarrowestPackageForFile(ctx, snapshot, destURI)
		if destPkg == nil {
			// Destination file is new. Find an existing package in the same directory
			// as the destination file.
			destPkg = narrowestPackageForDir(ctx, snapshot, destURI.DirPath())
		}
	}
	if destPkg == nil {
		// TODO: support destination file in a new package (destPkg == nil).
		return nil, protocol.Location{}, fmt.Errorf("could not resolve destination package")
	}

	start, end, err := srcPGF.RangePos(loc.Range)
	if err != nil {
		return nil, protocol.Location{}, err
	}

	cur, ok := srcPGF.Cursor().FindByPos(start, end)
	if !ok {
		return nil, protocol.Location{}, fmt.Errorf("no AST selection found at cursor")
	}
	_, _, targetObj := moveDeclTarget(srcPkg.TypesInfo(), cur)
	if targetObj == nil {
		return nil, protocol.Location{}, fmt.Errorf("could not resolve target declaration")
	}
	graph := buildSymbolRefGraph(srcPkg)
	moving := computeMovingSet(srcPkg, destPkg, graph, targetObj)
	if err := canMove(snapshot, srcPkg, destPkg, destURI, graph, moving, targetObj); err != nil {
		return nil, protocol.Location{}, err
	}
	return nil, protocol.Location{}, nil
}

// narrowestPackageForDir finds the package corresponding to dir, assuming that the file
// at dir does not yet exist.
func narrowestPackageForDir(ctx context.Context, snapshot *cache.Snapshot, dir string) *cache.Package {
	metas, err := snapshot.WorkspaceMetadata(ctx)
	if err != nil {
		return nil
	}
	dir = filepath.Clean(dir)
	for _, mp := range metas {
		if mp.ForTest != "" {
			continue // skip test variants and test packages
		}
		for _, f := range mp.CompiledGoFiles {
			if filepath.Clean(f.DirPath()) == dir {
				pkgs, err := snapshot.TypeCheck(ctx, mp.ID)
				if err == nil && len(pkgs) > 0 {
					return pkgs[0]
				}
				return nil
			}
		}
	}
	return nil
}

// moveDeclTarget returns the cursor of the declaration to be moved, based on
// curSel. The moving declaration is the innermost TypeSpec, ValueSpec, or
// FuncDecl enclosing curSel, if any. It must be a package-level decl. If the
// cursor is within a multi-value ValueSpec, we return the first enclosing
// identifier, if any. It also returns the corresponding name and types.Object.
func moveDeclTarget(info *types.Info, curSel inspector.Cursor) (inspector.Cursor, string, types.Object) {
	if cur, ok := moreiters.First(curSel.Enclosing(
		(*ast.FuncDecl)(nil), (*ast.TypeSpec)(nil), (*ast.ValueSpec)(nil))); ok {
		switch n := cur.Node().(type) {
		case *ast.FuncDecl:
			if obj := info.Defs[n.Name]; obj != nil {
				return cur, n.Name.Name, obj
			}
		case *ast.TypeSpec:
			// Only support moving package-level decls.
			if cur.Parent().ParentEdgeKind() == edge.File_Decls {
				if obj := info.Defs[n.Name]; obj != nil {
					return cur, n.Name.Name, obj
				}
			}
		case *ast.ValueSpec:
			// Only support moving package-level decls.
			if cur.Parent().ParentEdgeKind() == edge.File_Decls {
				if len(n.Values) > 0 && len(n.Values) != len(n.Names) {
					// Don't allow moving an ident in a multi-assignment with one value,
					// e.g. x, y := f(). The RHS may have side effects, and when moving
					// the variable it will change the number of calls.
					// (This pattern can also occur with indexing a map, type assertions,
					// and channels. We should also not support these types of moves)
					return inspector.Cursor{}, "", nil
				}
				if len(n.Names) == 1 {
					id := n.Names[0]
					if obj := info.Defs[id]; obj != nil {
						return cur, n.Names[0].Name, obj
					}
				} else {
					// For a multi-value ValueSpec, only match if the cursor is directly
					// on one of the declared names (not in the type or value expressions).
					// var a, b = foo, bar <- cursor in foo/bar is ambiguous
					// var a, b MyType <- cursor in MyType is ambiguous
					if curSel.ParentEdgeKind() == edge.ValueSpec_Names {
						id := curSel.Node().(*ast.Ident)
						if obj := info.Defs[id]; obj != nil {
							return cur, id.Name, obj
						}
					}
				}
			}
		}
	}
	return inspector.Cursor{}, "", nil
}

// declGraph is a directed graph where an edge (u, v) means top-level
// symbol u references top-level symbol v in the same package.
// declGraph[u][v] == true, then u depends on v.
type declGraph map[types.Object]map[types.Object]bool

type graphBuilder struct {
	pkg  *types.Package
	info *types.Info

	graph declGraph
}

// collect traverses node "root" and for every top-level package symbol "to"
// used inside "root", records a directed dependency edge from -> to in the graph.
func (gb *graphBuilder) collect(from types.Object, root ast.Node) {
	if from == nil || root == nil {
		return
	}
	// All references to symbols are either a plain ident or a dotted selection.
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if sel, ok := gb.info.Selections[n]; ok {
				gb.addRef(from, sel.Obj())
				ast.Inspect(n.X, visit)
				// No need to visit n.Sel, since we already
				// recorded a ref via info.Selections above.
				return false
			}
		case *ast.Ident:
			gb.addRef(from, gb.info.Uses[n])
		}
		return true
	}
	ast.Inspect(root, visit)
}

// addRef records a reference (directed edge) from "from" to "to".
func (gb *graphBuilder) addRef(from, to types.Object) {
	if from == nil || to == nil || from == to {
		return
	}
	if to.Pkg() != gb.pkg {
		return // cross-package reference
	}
	// Un-instantiate methods if necessary (e.g. F[int].M -> F[T].M).
	if fn, ok := to.(*types.Func); ok {
		to = fn.Origin()
	}
	if gb.graph[to] == nil {
		return // not a top-level symbol
	}
	// Because we do a pass to initialize an edge set for every symbol,
	// gb.graph[from] is guaranteed to be non-nil.
	gb.graph[from][to] = true
}

// Compute the symbol reference graph for the src package.
// Go through each top-level declaration. Create a directed edge (u, v) if
// a decl u references decl v.
func buildSymbolRefGraph(src *cache.Package) declGraph {
	gb := &graphBuilder{
		pkg:   src.Types(),
		info:  src.TypesInfo(),
		graph: make(declGraph),
	}

	// First pass: collect all top-level symbols in the source package.
	for _, pgf := range src.CompiledGoFiles() {
		for _, decl := range pgf.File.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if fn, ok := gb.info.Defs[decl.Name].(*types.Func); ok {
					gb.graph[fn] = make(map[types.Object]bool)
				}
			case *ast.GenDecl:
				switch decl.Tok {
				case token.CONST, token.VAR:
					for _, spec := range decl.Specs {
						for _, id := range spec.(*ast.ValueSpec).Names {
							if obj := gb.info.Defs[id]; obj != nil {
								gb.graph[obj] = make(map[types.Object]bool)
							}
						}
					}
				case token.TYPE:
					for _, spec := range decl.Specs {
						if obj := gb.info.Defs[spec.(*ast.TypeSpec).Name]; obj != nil {
							gb.graph[obj] = make(map[types.Object]bool)
						}
					}
				}
			}
		}
	}

	// Second pass: traverse top-level symbols and collect references.
	for _, pgf := range src.CompiledGoFiles() {
		for _, decl := range pgf.File.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if fn, ok := gb.info.Defs[decl.Name].(*types.Func); ok {
					gb.collect(fn, decl)
				}
			case *ast.GenDecl:
				switch decl.Tok {
				case token.CONST, token.VAR:
					for _, spec := range decl.Specs {
						vspec := spec.(*ast.ValueSpec)
						for i, id := range vspec.Names {
							if obj := gb.info.Defs[id]; obj != nil {
								// Every variable in the spec references the explicit type.
								// var x, y MyType
								gb.collect(obj, vspec.Type)
								switch len(vspec.Values) {
								case len(vspec.Names):
									// var x, y = a, b
									gb.collect(obj, vspec.Values[i])
								case 1:
									// var x, y = f()
									gb.collect(obj, vspec.Values[0])
								case 0:
									// var x T
									// (no value provided, skip)
								}
							}
						}
					}
				case token.TYPE:
					for _, spec := range decl.Specs {
						tspec := spec.(*ast.TypeSpec)
						if obj := gb.info.Defs[tspec.Name]; obj != nil {
							// e.g: type A int - edge from A to int
							gb.collect(obj, tspec.Type)
							// e.g: type A[T int] struct{} - edge from A to int
							if tspec.TypeParams != nil {
								gb.collect(obj, tspec.TypeParams)
							}
						}
					}
				}
			}
		}
	}
	return gb.graph
}

// implicitDependencies returns all package-level symbols that must move
// together with obj due to language coupling rules. These are ths symbols
// that the given obj has an implicit dependency on.
// Rules:
//   - Type: all methods in its method set.
//   - Method: receiver type declaration and all other methods on that receiver.
//   - Const group using iota: all constants in the entire const (...) block.
//
// Also returns true if the dependency added is from an iota const group,
// as we should avoid doing duplicate work on these objects.
func implicitDependencies(pkg *cache.Package, obj types.Object) (coupled []types.Object, isIota bool) {
	switch obj := obj.(type) {
	case *types.TypeName:
		if named, ok := obj.Type().(*types.Named); ok {
			// We only move methods declared in the source package, not methods inherited
			// from embedded types in other packages (so we can't use types.NewMethodSet).
			// TODO(mkalil): Should we move functions whose signature represents a constructor for this type?
			for m := range named.Methods() {
				coupled = append(coupled, m)
			}
		}
	case *types.Const:
		// If the declaration is inside a const group using iota, add all constants in the entire block.
		if pgf, err := pkg.FileEnclosing(obj.Pos()); err == nil {
			if cur, ok := pgf.Cursor().FindByPos(obj.Pos(), obj.Pos()); ok {
				if declCur, ok := moreiters.First(cur.Enclosing((*ast.GenDecl)(nil))); ok {
					genDecl := declCur.Node().(*ast.GenDecl)
					if objs, ok := usesIota(pkg.TypesInfo(), genDecl); ok {
						isIota = true
						coupled = append(coupled, objs...)
					}
				}
			}
		}
	}
	return coupled, isIota
}

// usesIota reports whether the GenDecl contains a use of iota. It returns
// a list of the objects corresponding to specs in the decl block.
func usesIota(info *types.Info, decl *ast.GenDecl) ([]types.Object, bool) {
	var (
		objs    []types.Object
		hasIota = false
	)
	for _, spec := range decl.Specs {
		if vspec, ok := spec.(*ast.ValueSpec); ok {
			for _, name := range vspec.Names {
				if obj := info.Defs[name]; obj != nil {
					objs = append(objs, obj)
				}
			}
			for _, val := range vspec.Values {
				// Need to inspect the entire expression because iota may be used like:
				// const (
				//		a = 1 << iota
				//		b
				// 		...
				// )
				ast.Inspect(val, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok {
						if info.Uses[id] == builtinIota {
							hasIota = true
							return false // stop descending
						}
					}
					return true
				})
			}
		}
	}
	return objs, hasIota
}

// computeMovingSet determines the set of objects that must move with the
// targetObj in order to produce valid code.
// For a declaration A, the initial moving set includes A and all coupled symbols of A.
// For each of these symbols, we also include their coupled symbols, recursively.
// For each symbol's dependencies (as specified by edges in the declGraph), we add
// the symbol to the moving set.
func computeMovingSet(srcPkg, destPkg *cache.Package, graph declGraph, targetObj types.Object) map[types.Object]bool {
	moving := make(map[types.Object]bool)
	if srcPkg == destPkg {
		// If the move is within the same package, we only need to move the
		// declaration itself.
		moving[targetObj] = true
		return moving
	}

	var queue []types.Object
	addToMoving := func(obj types.Object) {
		if obj == nil || moving[obj] {
			return
		}
		moving[obj] = true
		queue = append(queue, obj)
		deps, isIota := implicitDependencies(srcPkg, obj)
		for _, c := range deps {
			if !moving[c] {
				moving[c] = true
				// We will have already added all necessary dependencies of the const group
				// to the moving set, so we don't need to explore them again.
				if !isIota {
					queue = append(queue, c)
				}
			}
		}
	}
	addToMoving(targetObj)

	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		for v := range graph[u] {
			addToMoving(v)
		}
	}
	return moving
}

// pkgTransitivelyImports reports whether fromPkg imports targetPkg directly or transitively.
func pkgTransitivelyImports(g *metadata.Graph, fromPkg, targetPkg *metadata.Package) bool {
	targetPath := targetPkg.PkgPath
	if _, ok := fromPkg.DepsByPkgPath[targetPath]; ok {
		// Direct dependency
		return true
	}
	for dep := range g.ForwardReflexiveTransitiveClosure(fromPkg.ID) {
		if dep.PkgPath == targetPath {
			return true
		}
	}
	return false
}

// remainingReferencesMoving reports whether any decl in the graph that is not in the
// moving set depends on any object in the moving set. It also reports a list of the names of unexported
// objects in the moving set that are referenced by an object in the remaining set.
func remainingReferencesMoving(graph declGraph, moving map[types.Object]bool) (names []string, refExisting bool) {
	var unexported []types.Object
	seen := make(map[types.Object]bool)
	for obj, deps := range graph {
		if !moving[obj] {
			for n := range deps {
				if moving[n] {
					refExisting = true
					if !n.Exported() && !seen[n] {
						seen[n] = true
						unexported = append(unexported, n)
					}
				}
			}
		}
	}
	// Sort the unexported symbols so we can report them to the user in a standard order.
	slices.SortFunc(unexported, func(a, b types.Object) int {
		if c := strings.Compare(a.Name(), b.Name()); c != 0 {
			return c
		}
		return int(a.Pos() - b.Pos())
	})
	for _, obj := range unexported {
		names = append(names, obj.Name())
	}
	return names, refExisting
}

// canMove reports whether the specified moving set and declGraph constitutes a legal move.
// It checks for:
//   - import cycles
//   - illegal imports (i.e. internal imports)
//   - shadowing and name conflicts in the destination package
func canMove(snapshot *cache.Snapshot, srcPkg, destPkg *cache.Package, destURI protocol.DocumentURI, graph declGraph, moving map[types.Object]bool, moveTarget types.Object) error {
	var (
		srcMeta    = srcPkg.Metadata()
		destMeta   = destPkg.Metadata()
		srcPkgPath = srcMeta.PkgPath
		srcName    = srcMeta.Name
		destName   = destMeta.Name
		isCrossPkg = srcMeta.ID != destMeta.ID
	)
	// We don't need to check for import cycles or package-level symbols conflicts
	// if the move is in the same package.
	if isCrossPkg {
		if unexported, referencesMoving := remainingReferencesMoving(graph, moving); referencesMoving {
			// Check for internal import rule violations.
			if !metadata.IsValidImport(srcPkgPath, destMeta.PkgPath, true) {
				return fmt.Errorf("illegal import: the move requires source package %s to import destination package %s, which violates internal import rules", srcName, destName)
			}
			if len(unexported) > 0 {
				// TODO(mkalil): obj.Name() is not unique between symbols, so we may want to change this error message.
				return fmt.Errorf("illegal reference: symbols moving to the destination package (%s) are unexported and referenced in source package %s", strings.Join(unexported, ", "), srcName)
			}
			// Src must import dest. To avoid an import cycle, we must check that
			// dest does not already directly or transitively import src.
			if pkgTransitivelyImports(snapshot.MetadataGraph(), destMeta, srcMeta) {
				return fmt.Errorf("import cycle: the move requires source package %s to import destination package %s, which already imports %s", srcName, destName, srcName)
			}
		}
		// Check destPkg's package block for conflicts, since the moving
		// declarations become package-level declarations.
		for obj := range moving {
			if destPkg.Types().Scope().Lookup(obj.Name()) != nil {
				return fmt.Errorf("cannot move %s: %s is already declared in package %s",
					obj.Name(), obj.Name(), destName)
			}
		}
		// For each file in destPkg, check its file scope for conflicts.
		// Dot imports in any file can cause shadowing.
		for _, file := range destPkg.Syntax() {
			if fileScope := destPkg.TypesInfo().Scopes[file]; fileScope != nil {
				for obj := range moving {
					if fileScope.Lookup(obj.Name()) != nil {
						return fmt.Errorf("cannot move %s: conflicts with symbol in the destination package",
							obj.Name())
					}
				}

			}
		}

	} else {
		// Check the destination file block for conflicts, since package-level
		// declarations cannot share a name with a file-level import in the same file.
		if destPGF, err := destPkg.File(destURI); err == nil {
			if fileScope := destPkg.TypesInfo().Scopes[destPGF.File]; fileScope != nil {
				for obj := range moving {
					if fileScope.Lookup(obj.Name()) != nil {
						return fmt.Errorf("cannot move %s: conflicts with symbol in destination file",
							obj.Name())
					}
				}
			}
		}
	}
	return nil
}
