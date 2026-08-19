// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/moreiters"
)

// TODO(mkalil): Find a way to notify users which additional declarations will need to be moved.
func MoveDeclaration(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, destURI protocol.DocumentURI) ([]protocol.DocumentChange, protocol.Location, error) {
	srcPkg, _, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, protocol.Location{}, err
	}
	_ = buildSymbolRefGraph(srcPkg)
	// TODO(mkalil): next - examine graph edges to determine moving set
	return nil, protocol.Location{}, nil
}

// moveDeclTarget returns the cursor of the declaration to be moved, based on
// curSel. The moving declaration is the innermost TypeSpec, ValueSpec, or
// FuncDecl enclosing curSel, if any. If the cursor is within a multi-value
// ValueSpec, we return the first enclosing identifier, if any.
func moveDeclTarget(curSel inspector.Cursor) (inspector.Cursor, string) {
	if cur, ok := moreiters.First(curSel.Enclosing(
		(*ast.FuncDecl)(nil), (*ast.TypeSpec)(nil), (*ast.ValueSpec)(nil))); ok {
		switch n := cur.Node().(type) {
		case *ast.FuncDecl:
			return cur, n.Name.Name
		case *ast.TypeSpec:
			// Only support moving package-level decls.
			if cur.Parent().ParentEdgeKind() == edge.File_Decls {
				return cur, n.Name.Name
			}
		case *ast.ValueSpec:
			// Only support moving package-level decls.
			if cur.Parent().ParentEdgeKind() == edge.File_Decls {
				if len(n.Names) == 1 {
					return cur, n.Names[0].Name
				}
				if len(n.Values) == 1 { // len(n.Names) > 1
					// Don't allow moving an ident in a multi-assignment with one value,
					// e.g. x, y := f(). The RHS may have side effects, and when moving
					// the variable it will change the number of calls.
					// (This pattern can also occur with indexing a map, type assertions,
					// and channels. We should also not support these types of moves)
					return inspector.Cursor{}, ""
				}
				// For a multi-value ValueSpec, only match if the cursor is directly
				// on one of the declared names (not in the type or value expressions).
				// var a, b = foo, bar <- cursor in foo/bar is ambiguous
				// var a, b MyType <- cursor in MyType is ambiguous
				if curSel.ParentEdgeKind() == edge.ValueSpec_Names {
					return cur, curSel.Node().(*ast.Ident).Name
				}
			}
		}
	}
	return inspector.Cursor{}, ""
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
