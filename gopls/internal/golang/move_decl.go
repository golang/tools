// Copyright 2026 The Go Authors. All rights reserved.
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
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/moreiters"
	"golang.org/x/tools/internal/refactor"
	"golang.org/x/tools/internal/typesinternal"
)

var (
	// MoveDeclarationFileQuestion asks for the destination file for a moved declaration,
	// preferring the client's native file picker and falling back to a plain string input.
	MoveDeclarationFileQuestion = formQuestion[string, protocol.DocumentURI]{
		ID:          "file",
		Description: "destination file uri or absolute path for the moved declaration, e.g. file:///path/to/file.go or /path/to/file.go",
		Required:    true,
		Types: []any{
			protocol.FormFieldTypeFile{
				Kind:    protocol.FormFieldKindFile,
				Filters: []string{"go"}, // only move to a go file
			},
			protocol.FormFieldTypeString{
				Kind: protocol.FormFieldKindString,
			},
		},
		convert: func(dest string) (protocol.DocumentURI, error) {
			path := dest
			if strings.HasPrefix(dest, "file:") {
				uri, err := protocol.ParseDocumentURI(dest)
				if err != nil {
					return "", err
				}
				path = uri.Path()
			}
			if !filepath.IsAbs(path) {
				return "", fmt.Errorf("destination must be a file URI or an absolute path: %q", dest)
			}
			// Clean the path (e.g. remove ".." elements) explicitly.
			destURI := protocol.URIFromPath(filepath.Clean(path))
			if filepath.Ext(destURI.Path()) != ".go" {
				return "", fmt.Errorf("destination file must be a go file")
			}
			return destURI, nil
		},
	}

	moveDeclarationQuestions = []question{MoveDeclarationFileQuestion}
)

// moveDest is the destination package of a move. pkg is nil if the package
// does not exist yet, in which case the move creates it.
type moveDest struct {
	pkg     *cache.Package
	pkgPath metadata.PackagePath
	pkgName metadata.PackageName
}

// TODO(mkalil): Find a way to notify users which additional declarations will need to be moved.
func MoveDeclaration(ctx context.Context, snapshot *cache.Snapshot, srcFH file.Handle, destURI protocol.DocumentURI, loc protocol.Location) ([]protocol.DocumentChange, protocol.Location, error) {
	srcPkg, srcPGF, err := NarrowestPackageForFile(ctx, snapshot, srcFH.URI())
	if err != nil {
		return nil, protocol.Location{}, err
	}
	// Reject destinations that the go command ignores (directories starting
	// with ".", "_", or containing "testdata"), since the moved declarations
	// would not belong to any package. Also reject vendor directories and src
	// packages that do not have a module.
	srcMod := srcPkg.Metadata().Module
	if srcMod == nil || snapshot.IgnoredFile(destURI) || inVendorDir(srcMod, destURI) {
		return nil, protocol.Location{}, fmt.Errorf("cannot move declaration into %s: location is ignored by the go command or vendored", destURI.Path())
	}
	var (
		dest    moveDest
		destFH  file.Handle
		destPGF *parsego.File
	)
	{
		destFH, err = snapshot.ReadFile(ctx, destURI)
		if err != nil {
			return nil, protocol.Location{}, err
		}
		if _, err := destFH.Content(); err == nil { // destination file exists
			dest.pkg, destPGF, err = NarrowestPackageForFile(ctx, snapshot, destURI)
			if err != nil {
				return nil, protocol.Location{}, err
			}
			if !destPGF.File.Package.IsValid() {
				// NarrowestPackageForFile succeeds (err == nil) even if the
				// file has a missing or malformed package clause. The
				// destination file can have type errors that don't effect the
				// move, but it must have a package clause.
				return nil, protocol.Location{}, fmt.Errorf("destination file %s has no package clause", destURI.Path())
			}
		} else if errors.Is(err, os.ErrNotExist) { // destination file does not exist
			// TODO(mkalil): narrowestPackageForDir always returns the non-test
			// package, but if the dest URI is ".../_test.go", we should consider
			// asking the user whether to use package "dest_test" or package
			// "dest" through dynamic questions.
			dest.pkg = narrowestPackageForDir(ctx, snapshot, destURI.DirPath()) // if nil, will create a new package below
		} else { // unknown error
			return nil, protocol.Location{}, err
		}

		if dest.pkg != nil {
			dest.pkgPath, dest.pkgName = dest.pkg.Metadata().PkgPath, dest.pkg.Metadata().Name
		} else if dest.pkgPath, dest.pkgName, err = newPackage(srcMod, destURI); err != nil { // create package if it doesn't exist
			return nil, protocol.Location{}, err
		} else if dest.pkgPath == srcPkg.Metadata().PkgPath {
			// The destination directory has no non-test package, but it is the
			// source package's own directory (e.g. a directory containing only
			// _test.go files). This is a same-package move.
			dest.pkg, dest.pkgName = srcPkg, srcPkg.Metadata().Name
		}
	}

	start, end, err := srcPGF.RangePos(loc.Range)
	if err != nil {
		return nil, protocol.Location{}, err
	}

	cur, ok := srcPGF.Cursor().FindByPos(start, end)
	if !ok {
		return nil, protocol.Location{}, fmt.Errorf("no AST selection found at cursor")
	}
	srcInfo := srcPkg.TypesInfo()
	_, _, targetObj := moveDeclTarget(srcInfo, cur)
	if targetObj == nil {
		return nil, protocol.Location{}, fmt.Errorf("could not resolve target declaration")
	}
	graph := buildSymbolRefGraph(srcPkg)
	moving := computeMovingSet(srcPkg, dest.pkg, graph, targetObj)
	if err := canMove(snapshot, srcPkg, dest, destURI, graph, moving); err != nil {
		return nil, protocol.Location{}, err
	}
	// Generate protocol.DocumentChanges necessary for moving the declarations.
	var (
		changes []protocol.DocumentChange
		edits   = make(map[protocol.DocumentURI][]protocol.TextEdit)
		show    protocol.Range // Location where the decls are added. We'll redirect the editor view to here.
	)
	// Extract the ranges of the moving declarations.
	// TODO(mkalil): make the type map[uri][]movingRange to group ranges by URI so
	// we can more easily calculate imports inside updateSrcDestImports.
	movingRanges, err := extractMovingDeclarations(srcPkg, moving)
	if err != nil {
		return nil, protocol.Location{}, err
	}
	declRanges := ranges(movingRanges)

	// Dest: create the file and write header if the file does not exist.
	if destPGF == nil {
		changes = append(changes, protocol.DocumentChangeCreate(destFH.URI()))
		newFileEdits, err := writeNewDestFile(srcPkg, srcPGF, dest)
		if err != nil {
			return nil, protocol.Location{}, err
		}
		edits[destFH.URI()] = newFileEdits
	}

	// Source and dest: update imports based on packages referenced within the
	// moving decls. This must come after the new file header edit, since both
	// insert at the start of a new dest file and are applied in order.
	if err := updateSrcDestImports(srcPkg, dest, destPGF, destFH, movingRanges, edits); err != nil {
		return nil, protocol.Location{}, err
	}

	// Dest: determine file range to add moving declarations.
	var (
		endRange protocol.Range
		sep      string // separates the moving decls from the end of an existing dest file
	)
	if destPGF != nil {
		// File exists - add to the end of the file, after one blank line.
		endRange, err = destPGF.PosRange(destPGF.File.FileEnd, destPGF.File.FileEnd)
		if err != nil {
			return nil, protocol.Location{}, err
		}
		switch src := destPGF.Src; {
		case bytes.HasSuffix(src, []byte("\n\n")):
			sep = ""
		case bytes.HasSuffix(src, []byte("\n")):
			sep = "\n"
		default:
			sep = "\n\n"
		}
		// Line where the moving declarations start. We'll redirect the editor view to here.
		line := endRange.End.Line + uint32(strings.Count(sep, "\n"))
		show = protocol.Range{
			Start: protocol.Position{Line: line, Character: 0},
			End:   protocol.Position{Line: line, Character: 0},
		}
	}
	// Dest: write the declarations at the bottom of the file.
	var declsText strings.Builder
	destPkgPath := string(dest.pkgPath)
	for i, r := range movingRanges {
		text, err := r.text(srcInfo, destPkgPath)
		if err != nil {
			return nil, protocol.Location{}, err
		}
		if i > 0 {
			declsText.WriteString("\n\n") // blank line between moving decls
		}
		declsText.WriteString(text)
	}
	declsText.WriteString("\n")
	edits[destFH.URI()] = append(edits[destFH.URI()], protocol.TextEdit{
		Range:   endRange,
		NewText: sep + strings.TrimRight(declsText.String(), "\n") + "\n", // ensure decl text ends with one newline
	})

	// Source: remove the moving decls. Moving decls may be in any file of srcPkg,
	// not just srcPGF.
	for _, r := range movingRanges {
		// Also delete trailing empty lines, to avoid leaving blocks of whitespace
		// in the src files.
		// TODO(mkalil): address the skewed indentation when removing single specs
		// from a GenDecl.
		end := r.end
		offset, err := safetoken.Offset(r.pgf.Tok, end)
		if err != nil {
			return nil, protocol.Location{}, err
		}
		rest := r.pgf.Src[offset:]
		end += token.Pos(len(rest) - len(bytes.TrimLeft(rest, " \t\n")))
		rng, err := r.pgf.PosRange(r.start, end)
		if err != nil {
			return nil, protocol.Location{}, err
		}
		edits[r.pgf.URI] = append(edits[r.pgf.URI], protocol.TextEdit{Range: rng})
	}

	// Rewrite references to moving decls in all packages and update imports as
	// necessary.
	if err := updateRefsToMoving(ctx, snapshot, srcPkg, dest, srcPGF, moving, declRanges, edits); err != nil {
		return nil, protocol.Location{}, err
	}
	docChanges, err := editsToDocChanges(ctx, snapshot, edits)
	if err != nil {
		return nil, protocol.Location{}, err
	}
	changes = append(changes, docChanges...)
	// TODO(mkalil): Handle edge cases involving interfaces.
	return changes, protocol.Location{URI: destURI, Range: show}, nil
}

// narrowestPackageForDir finds the non-test package corresponding to dir,
// assuming that the file at dir does not yet exist.
//
// By filtering out packages with mp.ForTest != "", it skips test variants
// (e.g. "p [p.test]") and external test packages (e.g. "p_test [p.test]"),
// ensuring that only the ordinary non-test package for the directory is returned.
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

// newPackage returns the path and name of a new package in the directory
// of destURI, which must be within the module mod.
func newPackage(mod *packages.Module, destURI protocol.DocumentURI) (metadata.PackagePath, metadata.PackageName, error) {
	if mod == nil {
		return "", "", fmt.Errorf("cannot create a new package: source package is not in a module")
	}
	rel, err := filepath.Rel(mod.Dir, destURI.DirPath())
	if err != nil || !filepath.IsLocal(rel) {
		return "", "", fmt.Errorf("cannot create a new package: %s is not in module %s", destURI.DirPath(), mod.Path)
	}
	pkgPath := path.Join(mod.Path, filepath.ToSlash(rel))
	// Use the pkgName that importers would assume based on the package path.
	pkgName := imports.ImportPathToAssumedName(pkgPath)
	if !token.IsIdentifier(pkgName) {
		return "", "", fmt.Errorf("cannot create a new package: no valid package name for %s", pkgPath)
	}
	return metadata.PackagePath(pkgPath), metadata.PackageName(pkgName), nil
}

// inVendorDir reports whether destURI is within a vendor directory of the
// module mod. We don't allow moving into a vendor directory.
// TODO(mkalil): consolidate with other functions in cache/snapshot.go?
func inVendorDir(mod *packages.Module, destURI protocol.DocumentURI) bool {
	if mod == nil {
		return false
	}
	rel, err := filepath.Rel(mod.Dir, destURI.DirPath())
	if err != nil || !filepath.IsLocal(rel) {
		return false
	}
	return slices.Contains(strings.Split(filepath.ToSlash(rel), "/"), "vendor")
}

// moveDeclTarget returns the cursor of the declaration to be moved, based on
// curSel. The moving declaration is the innermost TypeSpec, ValueSpec, or
// FuncDecl enclosing curSel, if any. It must be a package-level decl, and a
// ValueSpec must declare a single name. It also returns the corresponding name
// and types.Object.
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
			// Only support moving package-level decls that declare a single
			// name. Moving one name of a multi-name spec (e.g. "var a, b = 1, 2"
			// or "var x, y = f()") is not supported.
			if cur.Parent().ParentEdgeKind() == edge.File_Decls && len(n.Names) == 1 {
				if obj := info.Defs[n.Names[0]]; obj != nil {
					return cur, n.Names[0].Name, obj
				}
			}
		}
	}
	return inspector.Cursor{}, "", nil
}

// declGraph records, for each top-level symbol of a package, the other
// top-level symbols that must move along with it.
type declGraph struct {
	// If explicit[u][v] is true, u refers to v by name in its declaration.
	explicit map[types.Object]map[types.Object]bool

	// If implicit[u][v] is true, u does not refer to v, but v must move
	// whenever u moves.
	implicit map[types.Object]map[types.Object]bool
}

type graphBuilder struct {
	pkg  *types.Package
	info *types.Info

	graph *declGraph
}

// collect traverses node "root" and for every top-level package symbol "to"
// used inside "root", records a directed dependency edge from -> to in the graph.
func (gb *graphBuilder) collect(from types.Object, root ast.Node) {
	if from == nil || root == nil {
		return
	}
	// All references to symbols are a plain ident, a dotted selection, or
	// use of a field in an unkeyed struct literal.
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if sel, ok := gb.info.Selections[n]; ok {
				gb.addExplicit(from, sel.Obj())
				ast.Inspect(n.X, visit)
				// No need to visit n.Sel, since we already
				// recorded a ref via info.Selections above.
				return false
			}
		case *ast.Ident:
			gb.addExplicit(from, gb.info.Uses[n])
		case *ast.CompositeLit:
			// An unkeyed struct literal (e.g. T{1, 2}) refers to every field.
			if t := gb.info.TypeOf(n); t != nil && len(n.Elts) > 0 {
				if _, keyed := n.Elts[0].(*ast.KeyValueExpr); !keyed {
					if st, ok := typesinternal.Unpointer(t).Underlying().(*types.Struct); ok {
						for f := range st.Fields() {
							gb.addExplicit(from, f)
						}
					}
				}
			}
		}
		return true
	}
	ast.Inspect(root, visit)
}

// addExplicit records a reference (directed edge) from "from" to "to".
func (gb *graphBuilder) addExplicit(from, to types.Object) {
	if from == nil || to == nil || from == to {
		return
	}
	if to.Pkg() != gb.pkg {
		return // cross-package reference
	}
	// Un-instantiate methods and fields if necessary (e.g. F[int].M -> F[T].M).
	switch obj := to.(type) {
	case *types.Func:
		to = obj.Origin()
	case *types.Var:
		to = obj.Origin()
	}
	explicit := gb.graph.explicit
	if explicit[to] == nil {
		return // not a top-level symbol
	}
	// Because we do a pass to initialize an edge set for every symbol,
	// explicit[from] is guaranteed to be non-nil.
	explicit[from][to] = true
}

// addImplicit records that "to" must move whenever "from" moves.
func (gb *graphBuilder) addImplicit(from, to types.Object) {
	if from == nil || to == nil || from == to {
		return
	}
	implicit := gb.graph.implicit
	if implicit[from] == nil {
		implicit[from] = make(map[types.Object]bool)
	}
	implicit[from][to] = true
}

// buildSymbolRefGraph computes the declaration graph for the src package.
// Go through each top-level declaration. Create a directed edge (u, v) if
// a decl u references decl v, or if v must move whenever u does.
func buildSymbolRefGraph(src *cache.Package) *declGraph {
	gb := &graphBuilder{
		pkg:  src.Types(),
		info: src.TypesInfo(),
		graph: &declGraph{
			explicit: make(map[types.Object]map[types.Object]bool),
			implicit: make(map[types.Object]map[types.Object]bool),
		},
	}
	explicit := gb.graph.explicit

	// addMembers records every struct field and interface method declared
	// within the syntax of owner's declaration (in a named, nested, or
	// anonymous type) as a symbol that moves with owner. This lets canMove
	// reject remaining references to unexported members (e.g. x.f, T{f: 1},
	// x.m()).
	addMembers := func(owner types.Object, decl ast.Node) {
		ast.Inspect(decl, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				obj := gb.info.Defs[id]
				switch o := obj.(type) {
				case *types.Var:
					if !o.IsField() {
						return true
					}
				case *types.Func:
					// Skip everything besides abstract (interface) methods. (recv == nil means it's a plain function)
					if recv := o.Signature().Recv(); recv == nil || !types.IsInterface(recv.Type()) {
						return true
					}
				default:
					return true
				}
				explicit[obj] = make(map[types.Object]bool)
				// Link owner and member in both directions, so that one never
				// moves without the other. Code that reaches a member already
				// reaches its owner through some other edge (it must obtain a
				// value of the owner's type somehow), so the reverse edge is a
				// safety net: it makes the invariant explicit in the graph
				// instead of relying on that indirect reasoning.
				gb.addImplicit(owner, obj)
				gb.addImplicit(obj, owner)
			}
			return true
		})
	}

	// First pass: collect all top-level symbols in the source package.
	for _, pgf := range src.CompiledGoFiles() {
		for _, decl := range pgf.File.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if fn, ok := gb.info.Defs[decl.Name].(*types.Func); ok {
					explicit[fn] = make(map[types.Object]bool)
					addMembers(fn, decl)
				}
			case *ast.GenDecl:
				switch decl.Tok {
				case token.CONST, token.VAR:
					for _, spec := range decl.Specs {
						for _, id := range spec.(*ast.ValueSpec).Names {
							if obj := gb.info.Defs[id]; obj != nil {
								explicit[obj] = make(map[types.Object]bool)
								addMembers(obj, spec)
							}
						}
					}
				case token.TYPE:
					for _, spec := range decl.Specs {
						if obj := gb.info.Defs[spec.(*ast.TypeSpec).Name]; obj != nil {
							explicit[obj] = make(map[types.Object]bool)
							addMembers(obj, spec)
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
					if decl.Tok == token.CONST {
						// Constants in an iota group must all move together.
						// Link them in a cycle, so that reaching any one of
						// them reaches the rest.
						if objs, ok := usesIota(gb.info, decl); ok {
							for i, obj := range objs {
								gb.addImplicit(obj, objs[(i+1)%len(objs)])
							}
						}
					}
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
							// A type is coupled to its methods. We only move
							// methods declared in the source package, not those
							// inherited from embedded types in other packages
							// (so we can't use types.NewMethodSet).
							// TODO(mkalil): Should we move functions whose signature represents a constructor for this type?
							if named, ok := obj.Type().(*types.Named); ok {
								for m := range named.Methods() {
									gb.addImplicit(obj, m)
								}
							}
						}
					}
				}
			}
		}
	}
	return gb.graph
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
//
// TODO(mkalil): handle same-package moves between test and non-test files. A
// package and its test variant are distinct *cache.Packages with the same path,
// so the srcPkg == destPkg check below treats them as different packages.
// (Currently, dialog_move_decl_tests.txt lacks complete tests for moves
// involving internal test files).
func computeMovingSet(srcPkg, destPkg *cache.Package, graph *declGraph, targetObj types.Object) map[types.Object]bool {
	visited := map[types.Object]bool{targetObj: true}
	if srcPkg == destPkg {
		// If the move is within the same package, we only need to move the
		// declaration itself.
		return visited
	}

	queue := []types.Object{targetObj}
	enqueue := func(obj types.Object) {
		if visited[obj] {
			return // visited
		}

		visited[obj] = true
		queue = append(queue, obj)
	}
	for len(queue) > 0 {
		current := queue[0] // dequeue
		queue = queue[1:]
		for v := range graph.explicit[current] {
			enqueue(v)
		}
		for v := range graph.implicit[current] {
			enqueue(v)
		}
	}
	return visited
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
func remainingReferencesMoving(graph *declGraph, moving map[types.Object]bool) (names []string, refExisting bool) {
	var unexported []types.Object
	seen := make(map[types.Object]bool)
	// Only explicit edges can cross the boundary: the moving set is closed
	// under implicit edges, so a coupling class always moves as a whole.
	for obj, deps := range graph.explicit {
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
func canMove(snapshot *cache.Snapshot, srcPkg *cache.Package, dest moveDest, destURI protocol.DocumentURI, graph *declGraph, moving map[types.Object]bool) error {
	var (
		srcMeta    = srcPkg.Metadata()
		destPkg    = dest.pkg // nil if dest is a new package
		srcPkgPath = srcMeta.PkgPath
		srcName    = srcMeta.Name
		destName   = dest.pkgName
		// Test variants have different IDs but share the same package path.
		// A move from a normal package to a test variant is considered
		// a same package move.
		isCrossPkg = srcMeta.PkgPath != dest.pkgPath
	)
	// We don't need to check for import cycles or package-level symbols conflicts
	// if the move is in the same package.
	if isCrossPkg {
		if unexported, referencesMoving := remainingReferencesMoving(graph, moving); referencesMoving {
			// Check for internal import rule violations.
			if !metadata.IsValidImport(srcPkgPath, dest.pkgPath, true) {
				return fmt.Errorf("illegal import: the move requires source package %s to import destination package %s, which violates internal import rules", srcName, destName)
			}
			if len(unexported) > 0 {
				// TODO(mkalil): obj.Name() is not unique between symbols, so we may want to change this error message.
				return fmt.Errorf("illegal reference: symbols moving to the destination package (%s) are unexported and referenced in source package %s", strings.Join(unexported, ", "), srcName)
			}
			// Src must import dest. To avoid an import cycle, we must check that
			// dest does not already directly or transitively import src.
			if destPkg != nil && pkgTransitivelyImports(snapshot.MetadataGraph(), destPkg.Metadata(), srcMeta) {
				return fmt.Errorf("import cycle: the move requires source package %s to import destination package %s, which already imports %s", srcName, destName, srcName)
			}
		}
		if destPkg == nil {
			return nil // a new package has no existing declarations to conflict with
		}
		// Check destPkg's package block for conflicts, since the moving
		// declarations become package-level declarations. Fields and methods
		// in the moving set are skipped: they are scoped to their type, not
		// the package block.
		for obj := range moving {
			if typesinternal.IsPackageLevel(obj) && destPkg.Types().Scope().Lookup(obj.Name()) != nil {
				return fmt.Errorf("cannot move %s: %s is already declared in package %s",
					obj.Name(), obj.Name(), destName)
			}
		}
		// For each file in destPkg, check its file scope for conflicts.
		// Dot imports in any file can cause shadowing.
		for _, file := range destPkg.Syntax() {
			if fileScope := destPkg.TypesInfo().Scopes[file]; fileScope != nil {
				for obj := range moving {
					if typesinternal.IsPackageLevel(obj) && fileScope.Lookup(obj.Name()) != nil {
						return fmt.Errorf("cannot move %s: conflicts with symbol in the destination package",
							obj.Name())
					}
				}

			}
		}

		// TODO(mkalil): block moves that would separate an interface type from its methods' implementations.

	} else {
		// Check the destination file block for conflicts, since package-level
		// declarations cannot share a name with a file-level import in the same file.
		if destPGF, err := destPkg.File(destURI); err == nil {
			if fileScope := destPkg.TypesInfo().Scopes[destPGF.File]; fileScope != nil {
				for obj := range moving {
					if typesinternal.IsPackageLevel(obj) && fileScope.Lookup(obj.Name()) != nil {
						return fmt.Errorf("cannot move %s: conflicts with symbol in destination file",
							obj.Name())
					}
				}
			}
		}
	}
	return nil
}

// A movingRange is a decl in srcPkg that moves to the destination.
type movingRange struct {
	pgf *parsego.File
	// Span of the moving decl.
	start, end token.Pos
	// Keyword (e.g. var, const, type) if the range is a single spec of a GenDecl;
	// token.ILLEGAL if it is a whole decl.
	tok  token.Token
	node ast.Node // the moving *ast.FuncDecl, *ast.GenDecl, or ast.Spec
}

// Range returns the span of r in its source file.
func (r movingRange) Range() astutil.Range { return astutil.RangeOf(r.start, r.end) }

// text returns the source text of the movingRange r, including its doc comment
// and any trailing comments, to insert in the destination file. If r is a spec,
// its keyword is prepended to make it a declaration, e.g. "var" in "var x = 1".
// References to destPkg within r lose their qualifier, e.g. "dest.F()" becomes "F()".
func (r movingRange) text(info *types.Info, destPkgPath string) (string, error) {
	startOff, endOff, err := safetoken.Offsets(r.pgf.Tok, r.start, r.end)
	if err != nil {
		return "", err
	}
	// Delete the qualifier of each reference to destPkg.
	// Edit offsets are relative to startOff.
	var edits []diff.Edit
	if curNode, ok := r.pgf.Cursor().FindNode(r.node); ok {
		for curSel := range curNode.Preorder((*ast.SelectorExpr)(nil)) {
			sel := curSel.Node().(*ast.SelectorExpr)
			if id, ok := sel.X.(*ast.Ident); ok {
				if pkgName, ok := info.Uses[id].(*types.PkgName); ok && pkgName.Imported().Path() == destPkgPath {
					idOff, selOff, err := safetoken.Offsets(r.pgf.Tok, id.Pos(), sel.Sel.Pos())
					if err != nil {
						return "", err
					}
					edits = append(edits, diff.Edit{Start: idOff - startOff, End: selOff - startOff})
				}
			}
		}
	}
	src := r.pgf.Src[startOff:endOff]
	text, err := diff.Apply(string(src), edits)
	if err != nil {
		return "", err
	}
	if r.tok != token.ILLEGAL {
		// Prepend a keyword ("var", "const", or "type") to a single spec, after its
		// doc comment, if any.
		nodeOff, err := safetoken.Offset(r.pgf.Tok, r.node.Pos())
		if err != nil {
			return "", err
		}
		docCommentLen := nodeOff - startOff
		return text[:docCommentLen] + r.tok.String() + " " + text[docCommentLen:], nil
	}
	return text, nil
}

// ranges returns the source spans of the given moving ranges.
func ranges(movingRanges []movingRange) []astutil.Range {
	rngs := make([]astutil.Range, len(movingRanges))
	for i, r := range movingRanges {
		rngs[i] = r.Range()
	}
	return rngs
}

// extractMovingDeclarations returns the declarations to move, in file order.
// If all specs of a GenDecl are moving, its range covers the whole GenDecl;
// otherwise each moving spec gets its own range.
func extractMovingDeclarations(srcPkg *cache.Package, moving map[types.Object]bool) ([]movingRange, error) {
	var (
		info         = srcPkg.TypesInfo()
		movingRanges []movingRange
	)
	isMoving := func(id *ast.Ident) bool {
		obj := info.Defs[id]
		return obj != nil && moving[obj]
	}
	for _, pgf := range srcPkg.CompiledGoFiles() {
		addMovingRange := func(n ast.Node, tok token.Token) {
			// Include the decl's doc comment.
			// TODO(hxjiang): consolidate this logic with the logic in hover.go that
			// finds the comments associated with a node.
			start := n.Pos()
			if doc := astutil.DocComment(n); doc != nil {
				start = doc.Pos()
			}
			// Include a trailing comment on the node's last line.
			end := n.End()
			comments := pgf.File.Comments
			// Find the first comment that is located after the end of the node.
			i := sort.Search(len(comments), func(i int) bool { return comments[i].Pos() >= end })
			if i < len(comments) && safetoken.Line(pgf.Tok, comments[i].Pos()) == safetoken.Line(pgf.Tok, end) {
				end = comments[i].End()
			}
			movingRanges = append(movingRanges, movingRange{pgf: pgf, node: n, start: start, end: end, tok: tok})
		}
		for _, decl := range pgf.File.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if isMoving(decl.Name) {
					addMovingRange(decl, token.ILLEGAL)
				}
			case *ast.GenDecl:
				var movingSpecs []ast.Spec
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if isMoving(spec.Name) {
							movingSpecs = append(movingSpecs, spec)
						}
					case *ast.ValueSpec:
						// Moving only some of the names in a spec, as in moving
						// "a" from "var a, b = 1, 2", is not supported.
						if i := slices.IndexFunc(spec.Names, isMoving); i >= 0 {
							if j := slices.IndexFunc(spec.Names, func(id *ast.Ident) bool {
								return id.Name != "_" && !isMoving(id)
							}); j >= 0 {
								return nil, fmt.Errorf("cannot move %s: it is declared together with %s, which is not moving",
									spec.Names[i].Name, spec.Names[j].Name)
							}
							movingSpecs = append(movingSpecs, spec)
						}
					}
				}
				if len(movingSpecs) == 0 {
					continue
				}
				if len(movingSpecs) == len(decl.Specs) {
					addMovingRange(decl, token.ILLEGAL) // the whole declaration moves
				} else {
					for _, spec := range movingSpecs {
						addMovingRange(spec, decl.Tok)
					}
				}
			}
		}
	}
	return movingRanges, nil
}

// writeNewDestFile reports edits needed to write a header with copyright, build
// constraints, and package declaration to a new file.
func writeNewDestFile(srcPkg *cache.Package, srcPGF *parsego.File, dest moveDest) ([]protocol.TextEdit, error) {
	var (
		edits     []protocol.TextEdit
		headerBuf bytes.Buffer
	)
	// Add copyright and build constraints if same package move.
	if dest.pkg != nil && srcPkg.Metadata().ID == dest.pkg.Metadata().ID {
		if c := CopyrightComment(srcPGF.File); c != nil {
			text, err := srcPGF.NodeText(c)
			if err != nil {
				return nil, err
			}
			headerBuf.Write(text)
			headerBuf.WriteString("\n\n")
		}

		if c := buildConstraintComment(srcPGF.File); c != nil {
			text, err := srcPGF.NodeText(c)
			if err != nil {
				return nil, err
			}
			headerBuf.Write(text)
			headerBuf.WriteString("\n\n")
		}
	}
	// Add package clause
	fmt.Fprintf(&headerBuf, "package %s\n\n", dest.pkgName)
	edits = append(edits, protocol.TextEdit{
		Range:   protocol.Range{},
		NewText: headerBuf.String(),
	})
	return edits, nil
}

// updateSrcDestImports records edits to remove imports that become unused
// in each source file, and to add the imports needed by the moving
// declarations to the destination file, destURI.
// It only handles packages that are used by the moving declarations.
// Note: destPGF may be nil if the target destination file is a new file.
func updateSrcDestImports(srcPkg *cache.Package, dest moveDest, destPGF *parsego.File, destFH file.Handle, movingRanges []movingRange, edits map[protocol.DocumentURI][]protocol.TextEdit) error {
	// Imports belong to a file, so find the import changes separately for each
	// source file that has moving declarations.
	destURI := destFH.URI()
	var adds []*ast.ImportSpec
	type importKey struct{ name, path string }
	seen := make(map[importKey]bool) // imports already in adds
	for _, curPGF := range srcPkg.CompiledGoFiles() {
		var curFileRanges []astutil.Range
		for _, r := range movingRanges {
			if r.pgf == curPGF {
				curFileRanges = append(curFileRanges, r.Range())
			}
		}
		if len(curFileRanges) == 0 {
			// Not moving anything from current file.
			continue
		}
		destAdds, srcDeletes, err := findImportChanges(curPGF.File, srcPkg.TypesInfo(), curFileRanges...)
		if err != nil {
			return err
		}
		if len(srcDeletes) > 0 {
			edits[curPGF.URI] = append(edits[curPGF.URI], importDeletesEdits(curPGF, srcDeletes)...)
		}
		// Several source files may import the same package but we should add it only once.
		for _, spec := range destAdds {
			key := importKey{path: spec.Path.Value}
			if spec.Name != nil {
				key.name = spec.Name.Name
			}
			if !seen[key] {
				seen[key] = true
				// Do not add a self-import of destPkg to the destination file.
				// (findImportChanges may return a dest import if the dest package is referenced
				// as part of some moving declaration.)
				if pkgName := srcPkg.TypesInfo().PkgNameOf(spec); pkgName != nil &&
					pkgName.Imported().Path() == string(dest.pkgPath) {
					continue
				}
				adds = append(adds, spec)
			}
		}
	}
	if destPGF != nil {
		// Destination file already exists: calculate text edits via addImport,
		// which wraps refactor.AddImport.
		for _, spec := range adds {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			name := ""
			if spec.Name != nil {
				name = spec.Name.Name
			}
			_, impEdits, err := addImport(dest.pkg.TypesInfo(), destPGF, name, path, "", destPGF.File.FileEnd-1)
			if err != nil {
				return err
			}
			edits[destURI] = append(edits[destURI], impEdits...)
		}
	} else if len(adds) > 0 {
		// Destination file is new: insert imports at start of file.
		var buf bytes.Buffer
		buf.WriteString("import (\n")
		for _, spec := range adds {
			if spec.Name != nil {
				fmt.Fprintf(&buf, "\t%s %s\n", spec.Name.Name, spec.Path.Value)
			} else {
				fmt.Fprintf(&buf, "\t%s\n", spec.Path.Value)
			}
		}
		buf.WriteString(")\n\n")
		edits[destURI] = append(edits[destURI], protocol.TextEdit{
			Range:   protocol.Range{},
			NewText: buf.String(),
		})
	}

	return nil
}

// addImport wraps [refactor.AddImport] and converts the resulting edits to [protocol.TextEdit]s.
func addImport(info *types.Info, pgf *parsego.File, preferredName, pkgPath, member string, poses ...token.Pos) (string, []protocol.TextEdit, error) {
	prefix, impEdits := refactor.AddImport(info, pgf.File, preferredName, pkgPath, member, poses...)
	pedits, err := pgf.ProtocolTextEdits(impEdits)
	if err != nil {
		return "", nil, err
	}
	return prefix, pedits, nil
}

// updateRefsToMoving records edits to update references to moving symbols
// across the source package, the destination package, and any other packages
// that import the source package.
// src pkg: MovingFoo -> dest.MovingFoo
// dest pkg: src.MovingFoo -> MovingFoo
// other pkg: src.MovingFoo -> dest.MovingFoo
// It also adds imports for the dest package in files that reference a moving
// decl and removes imports for the source package in files whose only
// reference to the source package is a reference to a moving decl.
func updateRefsToMoving(ctx context.Context, snapshot *cache.Snapshot, srcPkg *cache.Package, dest moveDest, srcPGF *parsego.File, moving map[types.Object]bool, movingDeclsRanges []astutil.Range, edits map[protocol.DocumentURI][]protocol.TextEdit,
) error {
	var (
		srcPkgPath  = string(srcPkg.Metadata().PkgPath)
		destPkgPath = string(dest.pkgPath)
		destPkgName = string(dest.pkgName)
	)
	// For a same-package move, we don't need to update any references.
	if srcPkgPath == destPkgPath {
		return nil
	}

	movingNames := make(map[string]bool)
	for obj := range moving {
		// Only rewrite references to package-level symbols (i.e. not methods).
		if typesinternal.IsPackageLevel(obj) {
			movingNames[obj.Name()] = true
		}
	}
	if len(movingNames) == 0 {
		return nil
	}

	// isMovingObj reports whether obj is a package-level symbol in the moving set.
	//
	// Each package we parse (including test variants of srcPkg) has its own
	// types.Object for a given symbol, so moving[obj] won't work. Instead, match
	// by package path and name, which uniquely identify a package-level object.
	isMovingObj := func(obj types.Object) bool {
		return obj != nil &&
			obj.Pkg() != nil &&
			obj.Pkg().Path() == srcPkgPath &&
			typesinternal.IsPackageLevel(obj) &&
			movingNames[obj.Name()]
	}

	// Rewrite references to objects in the moving set that are located
	// in the source package or any of its direct reverse dependencies.
	pkgs, err := typeCheckReverseDependencies(ctx, snapshot, srcPGF.URI, false)
	if err != nil {
		return err
	}

	// Process srcPkg first so that its files are handled using the same parsed
	// files that movingDeclsRanges came from: token.Pos values are only
	// comparable within a single parse, and other variants of srcPkg (e.g. "p
	// [p.test]") may have re-parsed those files.
	pkgs = append([]*cache.Package{srcPkg}, pkgs...)

	// Track seen files because pkgs may include both an ordinary package (e.g. "p")
	// and its internal test variant (e.g. "p [p.test]"), which have the same non-test
	// CompiledGoFiles. Processing a file more than once would produce duplicate edits.
	seenFiles := make(map[protocol.DocumentURI]bool)
	for _, curPkg := range pkgs {
		var (
			curInfo   = curPkg.TypesInfo()
			curPath   = string(curPkg.Metadata().PkgPath)
			isSrcPkg  = curPath == srcPkgPath
			isDestPkg = curPath == destPkgPath
		)

		for _, curPgf := range curPkg.CompiledGoFiles() {
			if seenFiles[curPgf.URI] {
				continue
			}
			seenFiles[curPgf.URI] = true

			var fileEdits []protocol.TextEdit
			if isSrcPkg {
				// Update references in the src package, skipping moving declarations.
				fileEdits, err = updateSrcFileRefs(curInfo, curPgf, destPkgName, destPkgPath, movingDeclsRanges, isMovingObj)
			} else {
				// External or test package.
				fileEdits, err = updatePkgQualifiedRefs(curInfo, curPgf, srcPkgPath, destPkgName, destPkgPath, isDestPkg, isMovingObj)
			}
			if err != nil {
				return err
			}
			if len(fileEdits) > 0 {
				edits[curPgf.URI] = append(edits[curPgf.URI], fileEdits...)
			}
		}
	}

	return nil
}

// updateSrcFileRefs updates unqualified references to moving symbols in a file
// belonging to srcPkg (e.g. Foo -> dest.Foo) and adds an import of destPkg if needed.
// Any references located within skipRanges (the moving declarations themselves) are ignored.
func updateSrcFileRefs(info *types.Info, pgf *parsego.File, destPkgName, destPkgPath string, skipRanges []astutil.Range, isMovingObj func(types.Object) bool,
) ([]protocol.TextEdit, error) {
	inSkipRanges := func(pos token.Pos) bool {
		for _, r := range skipRanges {
			if r.ContainsPos(pos) {
				return true
			}
		}
		return false
	}

	var positions []token.Pos // start poses of identifiers, re"()Foo"
	for cur := range pgf.Cursor().Preorder((*ast.Ident)(nil)) {
		id := cur.Node().(*ast.Ident)
		if inSkipRanges(id.Pos()) || !isMovingObj(info.Uses[id]) {
			continue
		}
		positions = append(positions, id.Pos())
	}
	if len(positions) == 0 { // no reference of moved symbols
		return nil, nil
	}

	prefix, edits, err := addImport(info, pgf, destPkgName, destPkgPath, "", positions...)
	if err != nil {
		return nil, err
	}
	// Insert prefix "dest." in front of the moved idents. Foo -> dest.Foo
	for _, pos := range positions {
		rng, err := pgf.PosRange(pos, pos)
		if err != nil {
			return nil, err
		}
		edits = append(edits, protocol.TextEdit{
			Range:   rng,
			NewText: prefix,
		})
	}
	return edits, nil
}

// updatePkgQualifiedRefs updates package-qualified references (src.Foo) to moving
// symbols in a file outside srcPkg.
//   - If isDestPkg is true, the package qualifier is dropped (src.Foo -> Foo).
//   - Otherwise, the qualifier is rewritten to destPkg (src.Foo -> dest.Foo) and
//     destPkg is imported if needed.
//
// If all uses of srcPkg in pgf are removed by this transformation, the unused
// srcPkg import is also deleted.
//
// TODO(mkalil): This doesn't handle references via dot imports. But they are rare. Maybe
// we should just reject if the srcPkg is dot-imported?
func updatePkgQualifiedRefs(info *types.Info, pgf *parsego.File, srcPkgPath, destPkgName, destPkgPath string, isDestPkg bool, isMovingObj func(types.Object) bool,
) ([]protocol.TextEdit, error) {
	// Count remaining uses of srcPkg's PkgName(s) in pgf so we can detect if
	// its import becomes unused after rewriting references to moving symbols.
	//
	// Uses are counted per PkgName (i.e. per import spec) rather than with a
	// single counter because pgf may import srcPkg more than once under different
	// names. Each import spec can only be deleted if all of its own uses are
	// removed.
	var (
		starts           []token.Pos // start poses of the qualifier, re"()src."
		ends             []token.Pos // end poses of the qualifier, re"src.()"
		remainingPkgUses = make(map[*types.PkgName]int)
	)
	for cur := range pgf.Cursor().Preorder((*ast.SelectorExpr)(nil)) {
		sel := cur.Node().(*ast.SelectorExpr)
		xID, ok := sel.X.(*ast.Ident)
		if !ok {
			continue
		}
		pkgName, ok := info.Uses[xID].(*types.PkgName)
		if !ok || pkgName.Imported().Path() != srcPkgPath {
			continue
		}

		if isMovingObj(info.Uses[sel.Sel]) {
			starts = append(starts, sel.Pos())
			ends = append(ends, sel.Sel.Pos())
		} else {
			remainingPkgUses[pkgName]++
		}
	}
	if len(starts) == 0 {
		return nil, nil // no moving references in this file; nothing to rewrite or delete
	}

	var (
		edits  []protocol.TextEdit
		prefix string
	)
	if isDestPkg { // Inside destPkg: drop the package qualifier (src.Foo -> Foo).
		prefix = ""
	} else { // In an external package: rewrite qualifier (src.Foo -> dest.Foo) and add import of destPkg.
		destPrefix, impEdits, err := addImport(info, pgf, destPkgName, destPkgPath, "", starts...)
		if err != nil {
			return nil, err
		}
		prefix = destPrefix
		edits = append(edits, impEdits...)
	}

	// Replace the package qualifier span [start, end) (e.g. "src.") with
	// prefix (e.g. "dest." in external packages, or "" inside destPkg).
	for i := range starts {
		rng, err := pgf.PosRange(starts[i], ends[i])
		if err != nil {
			return nil, err
		}
		edits = append(edits, protocol.TextEdit{
			Range:   rng,
			NewText: prefix,
		})
	}

	// If an import of srcPkg has no remaining uses in pgf, remove its spec.
	var unusedSpecs []*ast.ImportSpec
	for _, spec := range pgf.File.Imports {
		if pkgName := info.PkgNameOf(spec); pkgName != nil &&
			pkgName.Imported().Path() == srcPkgPath &&
			remainingPkgUses[pkgName] == 0 {
			unusedSpecs = append(unusedSpecs, spec)
		}
	}
	if len(unusedSpecs) > 0 {
		edits = append(edits, importDeletesEdits(pgf, unusedSpecs)...)
	}

	return edits, nil
}
