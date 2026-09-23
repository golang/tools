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
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/moreiters"
	"golang.org/x/tools/internal/refactor"
	"golang.org/x/tools/internal/typesinternal"
)

// moveDeclarationFormFile asks where to move a declaration, through the file
// picker of the client.
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

func resolveMoveDeclaration(options settings.ClientOptions, param *protocol.ExecuteCommandParams) error {
	var a0 command.MoveDeclarationArgs
	if err := command.UnmarshalArgs(param.Arguments, &a0); err != nil {
		return err
	}
	var form []protocol.FormField
	if ok := options.SupportedInteractiveInputTypes[protocol.FormFieldKindFile]; ok {
		form = moveDeclarationFormFile
	} else if ok := options.SupportedInteractiveInputTypes[protocol.FormFieldKindString]; ok {
		form = moveDeclarationFormString
	} else {
		// This should not happen because gopls should not offer this code action if the
		// language client does not support any kind above.
		return fmt.Errorf("internal error: unsupported interactive input types: %v", options.SupportedInteractiveInputTypes)
	}

	// First call, return the empty form.
	if len(param.FormAnswers) == 0 {
		param.FormFields = form
		return nil
	}

	file, err := param.RequiredAnswer[string]("file")
	if err != nil {
		return err
	}
	if _, err := protocol.ParseDocumentURI(file); err != nil {
		return err
	}
	param.FormFields = nil
	return nil
}

// TODO(mkalil): Find a way to notify users which additional declarations will need to be moved.
func MoveDeclaration(ctx context.Context, snapshot *cache.Snapshot, srcFH file.Handle, destURI protocol.DocumentURI, loc protocol.Location) ([]protocol.DocumentChange, protocol.Location, error) {
	srcPkg, srcPGF, err := NarrowestPackageForFile(ctx, snapshot, srcFH.URI())
	if err != nil {
		return nil, protocol.Location{}, err
	}
	var (
		destPkg *cache.Package
		destFH  file.Handle
		destPGF *parsego.File
	)
	{
		destFH, err = snapshot.ReadFile(ctx, destURI)
		if err != nil {
			return nil, protocol.Location{}, err
		}
		if _, err := destFH.Content(); err == nil { // destination file exists
			destPkg, destPGF, err = NarrowestPackageForFile(ctx, snapshot, destURI)
			if err != nil {
				return nil, protocol.Location{}, err
			}
		} else if errors.Is(err, os.ErrNotExist) { // destination file does not exist
			// TODO(mkalil): narrowestPackageForDir always returns the non-test
			// package, but if the dest URI is ".../_test.go", we should consider
			// asking the user whether to use package "dest_test" or package
			// "dest" through dynamic questions.
			destPkg = narrowestPackageForDir(ctx, snapshot, destURI.DirPath())
			if destPkg == nil {
				// TODO: support destination file in a new package (new directory created, destPkg == nil).
				return nil, protocol.Location{}, fmt.Errorf("could not resolve destination package")
			}
		} else { // unknown error
			return nil, protocol.Location{}, err
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
	_, _, targetObj := moveDeclTarget(srcPkg.TypesInfo(), cur)
	if targetObj == nil {
		return nil, protocol.Location{}, fmt.Errorf("could not resolve target declaration")
	}
	graph := buildSymbolRefGraph(srcPkg)
	moving := computeMovingSet(srcPkg, destPkg, graph, targetObj)
	if err := canMove(snapshot, srcPkg, destPkg, destURI, graph, moving, targetObj); err != nil {
		return nil, protocol.Location{}, err
	}
	// Generate protocol.DocumentChanges necessary for moving the declaration.
	declsText, declRanges, err := extractMovingDeclarations(srcPkg, moving)
	if err != nil {
		return nil, protocol.Location{}, err
	}
	var (
		changes []protocol.DocumentChange
		edits   = make(map[protocol.DocumentURI][]protocol.TextEdit)
		show    protocol.Range // Location where the decls are added. We'll redirect the editor view to here.
	)
	// Dest: create the file and write header if the file does not exist.
	if destPGF == nil {
		changes = append(changes, protocol.DocumentChangeCreate(destFH.URI()))
		newFileEdits, err := writeNewDestFile(srcPkg, destPkg, srcPGF)
		if err != nil {
			return nil, protocol.Location{}, err
		}
		edits[destFH.URI()] = newFileEdits
	}

	destAddImportEdits, srcDeleteImportEdits, err := updateSrcDestImports(srcPkg, destPkg, srcPGF, destPGF, declRanges)
	if err != nil {
		return nil, protocol.Location{}, err
	}

	// Source: delete unused imports.
	if len(srcDeleteImportEdits) > 0 {
		edits[srcFH.URI()] = append(edits[srcFH.URI()], srcDeleteImportEdits...)
	}

	// Dest: add new imports.
	if len(destAddImportEdits) > 0 {
		edits[destFH.URI()] = append(edits[destFH.URI()], destAddImportEdits...)
	}

	// Dest: determine file range to add moving declarations.
	endRange := protocol.Range{}
	if destPGF != nil {
		// File exists - add to the end of the file.
		endRange, err = destPGF.PosRange(destPGF.File.FileEnd, destPGF.File.FileEnd)
		if err != nil {
			return nil, protocol.Location{}, err
		}
		// Range where we add the moving declarations (two lines after the current
		// file end to account for new lines)
		show = protocol.Range{
			Start: protocol.Position{Line: endRange.End.Line + 2, Character: 0},
			End:   protocol.Position{Line: endRange.End.Line + 2, Character: 0},
		}
	}
	// Dest: write the declarations at the bottom of the file.
	edits[destFH.URI()] = append(edits[destFH.URI()], protocol.TextEdit{
		Range:   endRange,
		NewText: strings.TrimRight(declsText, "\n") + "\n", // ensure decl text ends with one newline
	})

	// Rewrite references to moving decls in all packages.
	if err := updateRefsToMoving(ctx, snapshot, srcPkg, destPkg, srcPGF, moving, declRanges, edits); err != nil {
		return nil, protocol.Location{}, err
	}
	docChanges, err := editsToDocChanges(ctx, snapshot, edits)
	if err != nil {
		return nil, protocol.Location{}, err
	}
	changes = append(changes, docChanges...)
	// TODO(mkalil):
	// - Handle floating comments.
	// - Remove the decls from the src package.
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
	// All references to symbols are either a plain ident or a dotted selection.
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
	// Un-instantiate methods if necessary (e.g. F[int].M -> F[T].M).
	if fn, ok := to.(*types.Func); ok {
		to = fn.Origin()
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

	// First pass: collect all top-level symbols in the source package.
	for _, pgf := range src.CompiledGoFiles() {
		for _, decl := range pgf.File.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if fn, ok := gb.info.Defs[decl.Name].(*types.Func); ok {
					explicit[fn] = make(map[types.Object]bool)
				}
			case *ast.GenDecl:
				switch decl.Tok {
				case token.CONST, token.VAR:
					for _, spec := range decl.Specs {
						for _, id := range spec.(*ast.ValueSpec).Names {
							if obj := gb.info.Defs[id]; obj != nil {
								explicit[obj] = make(map[types.Object]bool)
							}
						}
					}
				case token.TYPE:
					for _, spec := range decl.Specs {
						if obj := gb.info.Defs[spec.(*ast.TypeSpec).Name]; obj != nil {
							explicit[obj] = make(map[types.Object]bool)
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
func canMove(snapshot *cache.Snapshot, srcPkg, destPkg *cache.Package, destURI protocol.DocumentURI, graph *declGraph, moving map[types.Object]bool, moveTarget types.Object) error {
	var (
		srcMeta    = srcPkg.Metadata()
		destMeta   = destPkg.Metadata()
		srcPkgPath = srcMeta.PkgPath
		srcName    = srcMeta.Name
		destName   = destMeta.Name
		// Test variants have different IDs but share the same package path.
		// A move from a normal package to a test variant is considered
		// a same package move.
		isCrossPkg = srcMeta.PkgPath != destMeta.PkgPath
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

// extractMovingDeclarations returns the ranges and text of the declarations to move.
// TODO(mkalil): Preserve floating comments.
// TODO(mkalil): Refactor to return []declRange instead of text and ranges.
func extractMovingDeclarations(srcPkg *cache.Package, moving map[types.Object]bool) (string, []astutil.Range, error) {
	var (
		info       = srcPkg.TypesInfo()
		fset       = srcPkg.FileSet()
		buf        bytes.Buffer
		declRanges []astutil.Range
	)
	for _, pgf := range srcPkg.CompiledGoFiles() {
		for _, decl := range pgf.File.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if obj, ok := info.Defs[decl.Name].(*types.Func); ok && moving[obj] {
					var declBuf bytes.Buffer
					if err := format.Node(&declBuf, fset, decl); err != nil {
						return "", nil, err
					}
					buf.WriteString(declBuf.String())
					buf.WriteString("\n\n")
					declRanges = append(declRanges, astutil.NodeRange(decl))
				}
			case *ast.GenDecl:
				var movingSpecs []ast.Spec
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if obj, ok := info.Defs[spec.Name]; ok && moving[obj] {
							movingSpecs = append(movingSpecs, spec)
							declRanges = append(declRanges, astutil.NodeRange(spec))
						}
					case *ast.ValueSpec:
						for _, id := range spec.Names {
							if obj, ok := info.Defs[id]; ok && moving[obj] {
								movingSpecs = append(movingSpecs, spec)
								declRanges = append(declRanges, astutil.NodeRange(spec))
							}
						}
					}
				}

				if len(movingSpecs) > 0 {
					var declToPrint ast.Node = decl
					if len(movingSpecs) < len(decl.Specs) {
						// Only a subset of the specs are moving, so wrap them in a new GenDecl.
						declToPrint = &ast.GenDecl{
							Doc:    decl.Doc,
							TokPos: decl.TokPos,
							Tok:    decl.Tok,
							Specs:  movingSpecs,
						}
					}
					var declBuf bytes.Buffer
					if err := format.Node(&declBuf, fset, declToPrint); err != nil {
						return "", nil, err
					}
					buf.WriteString(declBuf.String())
					buf.WriteString("\n\n")
				}
			}
		}
	}
	return buf.String(), declRanges, nil
}

// writeNewDestFile reports edits needed to write a header with copyright, build
// constraints, and package declaration to a new file.
func writeNewDestFile(srcPkg, destPkg *cache.Package, srcPGF *parsego.File) ([]protocol.TextEdit, error) {
	var (
		edits     []protocol.TextEdit
		headerBuf bytes.Buffer
	)
	// Add copyright and build constraints if same package move.
	if srcPkg.Metadata().ID == destPkg.Metadata().ID {
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
	fmt.Fprintf(&headerBuf, "package %s\n\n", destPkg.Types().Name())
	edits = append(edits, protocol.TextEdit{
		Range:   protocol.Range{},
		NewText: headerBuf.String(),
	})
	return edits, nil
}

// updateSrcDestImports returns:
// - destAddImportEdits: text edits to add imports to the destination file
// - srcDeleteImportEdits: text edits to remove unused imports from srcPGF
// It only handles packages that are used by the moving declarations.
// Note: destPGF may be nil if the target destination file is a new file.
func updateSrcDestImports(srcPkg, destPkg *cache.Package, srcPGF, destPGF *parsego.File, declRanges []astutil.Range) (
	destAddImportEdits []protocol.TextEdit,
	srcDeleteImportEdits []protocol.TextEdit,
	err error,
) {
	adds, deletes, err := findImportChanges(srcPGF.File, srcPkg.TypesInfo(), declRanges...)
	if err != nil {
		return nil, nil, err
	}
	// Do not add a self-import of destPkg to the destination file.
	// (findImportEdits may return a dest import if the dest package is referenced
	// as part of some moving declaration.)
	var filteredAdds []*ast.ImportSpec
	for _, spec := range adds {
		if pkgName := srcPkg.TypesInfo().PkgNameOf(spec); pkgName != nil &&
			pkgName.Imported().Path() == string(destPkg.Metadata().PkgPath) {
			continue
		}
		filteredAdds = append(filteredAdds, spec)
	}
	adds = filteredAdds

	srcDeleteImportEdits = importDeletesEdits(srcPGF, deletes)
	if destPGF != nil {
		// Destination file already exists: calculate text edits via addImport,
		// which wraps refactor.AddImport.
		for _, spec := range adds {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, nil, err
			}
			name := ""
			if spec.Name != nil {
				name = spec.Name.Name
			}
			_, impEdits, err := addImport(destPkg.TypesInfo(), destPGF, name, path, "", destPGF.File.FileEnd-1)
			if err != nil {
				return nil, nil, err
			}
			destAddImportEdits = append(destAddImportEdits, impEdits...)
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
		destAddImportEdits = append(destAddImportEdits, protocol.TextEdit{
			Range:   protocol.Range{},
			NewText: buf.String(),
		})
	}

	return destAddImportEdits, srcDeleteImportEdits, nil
}

// addImport wraps [refactor.AddImport] and converts the resulting edits to [protocol.TextEdit]s.
func addImport(info *types.Info, pgf *parsego.File, preferredName, pkgPath, member string, pos token.Pos) (string, []protocol.TextEdit, error) {
	prefix, impEdits := refactor.AddImport(info, pgf.File, preferredName, pkgPath, member, pos)
	var edits []protocol.TextEdit
	for _, edit := range impEdits {
		rng, err := pgf.PosRange(edit.Pos, edit.End)
		if err != nil {
			return "", nil, err
		}
		edits = append(edits, protocol.TextEdit{
			Range:   rng,
			NewText: string(edit.NewText),
		})
	}
	return prefix, edits, nil
}

// updateRefsToMoving records edits to update references to moving symbols
// across the source package, the destination package, and any other packages
// that import the source package.
// src pkg: MovingFoo -> dest.MovingFoo
// dest pkg: src.MovingFoo -> MovingFoo
// other pkg: src.MovingFoo -> dest.MovingFoo
func updateRefsToMoving(ctx context.Context, snapshot *cache.Snapshot, srcPkg, destPkg *cache.Package, srcPGF *parsego.File, moving map[types.Object]bool, movingDeclsRanges []astutil.Range, edits map[protocol.DocumentURI][]protocol.TextEdit,
) error {
	var (
		srcPkgPath  = string(srcPkg.Metadata().PkgPath)
		destPkgPath = string(destPkg.Metadata().PkgPath)
		destPkgName = string(destPkg.Metadata().Name)
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

	var (
		edits           []protocol.TextEdit
		addedDestImport bool
		destPrefix      string
	)
	for cur := range pgf.Cursor().Preorder((*ast.Ident)(nil)) {
		id := cur.Node().(*ast.Ident)
		if inSkipRanges(id.Pos()) || !isMovingObj(info.Uses[id]) {
			continue
		}
		if !addedDestImport {
			prefix, impEdits, err := addImport(info, pgf, destPkgName, destPkgPath, id.Name, id.Pos())
			if err != nil {
				return nil, err
			}
			edits = append(edits, impEdits...)
			addedDestImport = true
			destPrefix = prefix
		}
		idRng, err := pgf.NodeRange(id)
		if err != nil {
			return nil, err
		}
		edits = append(edits, protocol.TextEdit{
			Range:   idRng,
			NewText: destPrefix + id.Name,
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
// TODO(mkalil): This doesn't handle references via dot imports. But they are rare. Maybe
// we should just reject if the srcPkg is dot-imported?
func updatePkgQualifiedRefs(info *types.Info, pgf *parsego.File, srcPkgPath, destPkgName, destPkgPath string, isDestPkg bool, isMovingObj func(types.Object) bool,
) ([]protocol.TextEdit, error) {
	// Count total uses of srcPkg's PkgName(s) in pgf so we can detect if its
	// import becomes unused after rewriting references to moving symbols.
	//
	// Uses are counted per PkgName (i.e. per import spec) rather than with a
	// single counter because pgf may import srcPkg more than once under different
	// names. Each import spec can only be deleted if all of its own uses are
	// removed.
	totalPkgUses := make(map[*types.PkgName]int)
	for cur := range pgf.Cursor().Preorder((*ast.Ident)(nil)) {
		id := cur.Node().(*ast.Ident)
		if pkgName, ok := info.Uses[id].(*types.PkgName); ok && pkgName.Imported().Path() == srcPkgPath {
			totalPkgUses[pkgName]++
		}
	}

	var (
		edits          []protocol.TextEdit
		removedPkgUses = make(map[*types.PkgName]int)
		addedImport    bool
		destPrefix     string
	)
	for cur := range pgf.Cursor().Preorder((*ast.SelectorExpr)(nil)) {
		sel := cur.Node().(*ast.SelectorExpr)
		if !isMovingObj(info.Uses[sel.Sel]) {
			continue
		}
		xId, ok := sel.X.(*ast.Ident)
		if !ok {
			continue
		}
		pkgName, ok := info.Uses[xId].(*types.PkgName)
		if !ok || pkgName.Imported().Path() != srcPkgPath {
			continue
		}
		removedPkgUses[pkgName]++

		rng, err := pgf.NodeRange(sel) // replace the range of the entire selector (src.Foo) with either Foo or dest.Foo
		if err != nil {
			return nil, err
		}

		if isDestPkg {
			// Inside destPkg: drop the package qualifier (src.Foo -> Foo).
			edits = append(edits, protocol.TextEdit{
				Range:   rng,
				NewText: sel.Sel.Name,
			})
		} else {
			// In a non-destPkg package: rewrite qualifier (src.Foo -> dest.Foo)
			// and add an import of destPkg.
			if !addedImport {
				var impEdits []protocol.TextEdit
				destPrefix, impEdits, err = addImport(info, pgf, destPkgName, destPkgPath, sel.Sel.Name, sel.Pos())
				if err != nil {
					return nil, err
				}
				edits = append(edits, impEdits...)
				addedImport = true
			}
			edits = append(edits, protocol.TextEdit{
				Range:   rng,
				NewText: destPrefix + sel.Sel.Name,
			})
		}
	}

	// If srcPkg has no remaining uses in pgf, remove its import spec.
	var unusedSpecs []*ast.ImportSpec
	for _, spec := range pgf.File.Imports {
		if pkgName := info.PkgNameOf(spec); pkgName != nil &&
			removedPkgUses[pkgName] > 0 &&
			removedPkgUses[pkgName] == totalPkgUses[pkgName] {
			unusedSpecs = append(unusedSpecs, spec)
		}
	}
	if len(unusedSpecs) > 0 {
		edits = append(edits, importDeletesEdits(pgf, unusedSpecs)...)
	}

	return edits, nil
}
