// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// TODO(adonovan):
//
// - method of generic concrete type -> arbitrary instances of same
//
// - make satisfy work across packages.
//
// - tests, tests, tests:
//   - play with renamings in the k8s tree.
//   - generics
//   - error cases (e.g. conflicts)
//   - renaming a symbol declared in the module cache
//     (currently proceeds with half of the renaming!)
//   - make sure all tests have both a local and a cross-package analogue.
//   - look at coverage
//   - special cases: embedded fields, interfaces, test variants,
//     function-local things with uppercase names;
//     packages with type errors (currently 'satisfy' rejects them),
//     package with missing imports;
//
// - measure performance in k8s.
//
// - The original gorename tool assumed well-typedness, but the gopls feature
//   does no such check (which actually makes it much more useful).
//   Audit to ensure it is safe on ill-typed code.
//
// - Generics support was no doubt buggy before but incrementalization
//   may have exacerbated it. If the problem were just about objects,
//   defs and uses it would be fairly simple, but type assignability
//   comes into play in the 'satisfy' check for method renamings.
//   De-instantiating Vector[int] to Vector[T] changes its type.
//   We need to come up with a theory for the satisfy check that
//   works with generics, and across packages. We currently have no
//   simple way to pass types between packages (think: objectpath for
//   types), though presumably exportdata could be pressed into service.
//
// - FileID-based de-duplication of edits to different URIs for the same file.

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/typesinternal"
	"golang.org/x/tools/refactor/satisfy"
)

// A renamer holds state of a single call to renameObj, which renames
// an object (or several coupled objects) within a single type-checked
// syntax package.
type renamer struct {
	pkg                *cache.Package        // the syntax package in which the renaming is applied
	objsToUpdate       map[types.Object]bool // records progress of calls to check
	conflicts          []string
	from, to           string
	satisfyConstraints map[satisfy.Constraint]bool
	msets              typeutil.MethodSetCache
	changeMethods      bool
}

// A PrepareItem holds the result of a "prepare rename" operation:
// the source range and value of a selected identifier.
type PrepareItem struct {
	Range protocol.Range
	Text  string
}

// PrepareRename searches for a valid renaming at position pp.
//
// The returned usererr is intended to be displayed to the user to explain why
// the prepare fails. Probably we could eliminate the redundancy in returning
// two errors, but for now this is done defensively.
func PrepareRename(ctx context.Context, snapshot *cache.Snapshot, f file.Handle, pp protocol.Position) (_ *PrepareItem, usererr, err error) {
	ctx, done := event.Start(ctx, "golang.PrepareRename")
	defer done()

	// Is the cursor within the package name declaration?
	if pgf, inPackageName, err := parsePackageNameDecl(ctx, snapshot, f, pp); err != nil {
		return nil, err, err
	} else if inPackageName {
		item, err := prepareRenamePackageName(ctx, snapshot, pgf)
		return item, err, err
	}

	// Ordinary (non-package) renaming.
	//
	// Type-check the current package, locate the reference at the position,
	// validate the object, and report its name and range.
	//
	// TODO(adonovan): in all cases below, we return usererr=nil,
	// which means we return (nil, nil) at the protocol
	// layer. This seems like a bug, or at best an exploitation of
	// knowledge of VSCode-specific behavior. Can we avoid that?
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, f.URI())
	if err != nil {
		return nil, nil, err
	}
	pos, err := pgf.PositionPos(pp)
	if err != nil {
		return nil, nil, err
	}
	targets, node, err := objectsAt(pkg.TypesInfo(), pgf.File, pos)
	if err != nil {
		return nil, nil, err
	}
	var obj types.Object
	for obj = range targets {
		break // pick one arbitrarily
	}
	if err := checkRenamable(obj); err != nil {
		return nil, nil, err
	}
	rng, err := pgf.NodeRange(node)
	if err != nil {
		return nil, nil, err
	}
	if _, isImport := node.(*ast.ImportSpec); isImport {
		// We're not really renaming the import path.
		rng.End = rng.Start
	}
	return &PrepareItem{
		Range: rng,
		Text:  obj.Name(),
	}, nil, nil
}

func prepareRenamePackageName(ctx context.Context, snapshot *cache.Snapshot, pgf *parsego.File) (*PrepareItem, error) {
	// Does the client support file renaming?
	fileRenameSupported := false
	for _, op := range snapshot.Options().SupportedResourceOperations {
		if op == protocol.Rename {
			fileRenameSupported = true
			break
		}
	}
	if !fileRenameSupported {
		return nil, errors.New("can't rename package: LSP client does not support file renaming")
	}

	// Check validity of the metadata for the file's containing package.
	meta, err := NarrowestMetadataForFile(ctx, snapshot, pgf.URI)
	if err != nil {
		return nil, err
	}
	if meta.Name == "main" {
		return nil, fmt.Errorf("can't rename package \"main\"")
	}
	if strings.HasSuffix(string(meta.Name), "_test") {
		return nil, fmt.Errorf("can't rename x_test packages")
	}
	if meta.Module == nil {
		return nil, fmt.Errorf("can't rename package: missing module information for package %q", meta.PkgPath)
	}
	if meta.Module.Path == string(meta.PkgPath) {
		return nil, fmt.Errorf("can't rename package: package path %q is the same as module path %q", meta.PkgPath, meta.Module.Path)
	}

	// Return the location of the package declaration.
	rng, err := pgf.NodeRange(pgf.File.Name)
	if err != nil {
		return nil, err
	}
	return &PrepareItem{
		Range: rng,
		Text:  string(meta.Name),
	}, nil
}

func checkRenamable(obj types.Object) error {
	switch obj := obj.(type) {
	case *types.Var:
		if obj.Embedded() {
			return fmt.Errorf("can't rename embedded fields: rename the type directly or name the field")
		}
	case *types.Builtin, *types.Nil:
		return fmt.Errorf("%s is built in and cannot be renamed", obj.Name())
	}
	if obj.Pkg() == nil || obj.Pkg().Path() == "unsafe" {
		// e.g. error.Error, unsafe.Pointer
		return fmt.Errorf("%s is built in and cannot be renamed", obj.Name())
	}
	if obj.Name() == "_" {
		return errors.New("can't rename \"_\"")
	}
	return nil
}

// Rename returns a map of TextEdits for each file modified when renaming a
// given identifier within a package and a boolean value of true for renaming
// package and false otherwise.
func Rename(ctx context.Context, snapshot *cache.Snapshot, f file.Handle, pp protocol.Position, newName string) (map[protocol.DocumentURI][]protocol.TextEdit, bool, error) {
	ctx, done := event.Start(ctx, "golang.Rename")
	defer done()

	if !isValidIdentifier(newName) {
		return nil, false, fmt.Errorf("invalid identifier to rename: %q", newName)
	}

	// Cursor within package name declaration?
	_, inPackageName, err := parsePackageNameDecl(ctx, snapshot, f, pp)
	if err != nil {
		return nil, false, err
	}

	var editMap map[protocol.DocumentURI][]diff.Edit
	if inPackageName {
		editMap, err = renamePackageName(ctx, snapshot, f, PackageName(newName))
	} else {
		editMap, err = renameOrdinary(ctx, snapshot, f, pp, newName)
	}
	if err != nil {
		return nil, false, err
	}

	// Convert edits to protocol form.
	result := make(map[protocol.DocumentURI][]protocol.TextEdit)
	for uri, edits := range editMap {
		// Sort and de-duplicate edits.
		//
		// Overlapping edits may arise in local renamings (due
		// to type switch implicits) and globals ones (due to
		// processing multiple package variants).
		//
		// We assume renaming produces diffs that are all
		// replacements (no adjacent insertions that might
		// become reordered) and that are either identical or
		// non-overlapping.
		diff.SortEdits(edits)
		filtered := edits[:0]
		for i, edit := range edits {
			if i == 0 || edit != filtered[len(filtered)-1] {
				filtered = append(filtered, edit)
			}
		}
		edits = filtered

		// TODO(adonovan): the logic above handles repeat edits to the
		// same file URI (e.g. as a member of package p and p_test) but
		// is not sufficient to handle file-system level aliasing arising
		// from symbolic or hard links. For that, we should use a
		// robustio-FileID-keyed map.
		// See https://go.dev/cl/457615 for example.
		// This really occurs in practice, e.g. kubernetes has
		// vendor/k8s.io/kubectl -> ../../staging/src/k8s.io/kubectl.
		fh, err := snapshot.ReadFile(ctx, uri)
		if err != nil {
			return nil, false, err
		}
		data, err := fh.Content()
		if err != nil {
			return nil, false, err
		}
		m := protocol.NewMapper(uri, data)
		protocolEdits, err := protocol.EditsFromDiffEdits(m, edits)
		if err != nil {
			return nil, false, err
		}
		result[uri] = protocolEdits
	}

	return result, inPackageName, nil
}

// renameOrdinary renames an ordinary (non-package) name throughout the workspace.
func renameOrdinary(ctx context.Context, snapshot *cache.Snapshot, f file.Handle, pp protocol.Position, newName string) (map[protocol.DocumentURI][]diff.Edit, error) {
	// Type-check the referring package and locate the object(s).
	//
	// Unlike NarrowestPackageForFile, this operation prefers the
	// widest variant as, for non-exported identifiers, it is the
	// only package we need. (In case you're wondering why
	// 'references' doesn't also want the widest variant: it
	// computes the union across all variants.)
	var targets map[types.Object]ast.Node
	var pkg *cache.Package
	{
		mps, err := snapshot.MetadataForFile(ctx, f.URI())
		if err != nil {
			return nil, err
		}
		metadata.RemoveIntermediateTestVariants(&mps)
		if len(mps) == 0 {
			return nil, fmt.Errorf("no package metadata for file %s", f.URI())
		}
		widest := mps[len(mps)-1] // widest variant may include _test.go files
		pkgs, err := snapshot.TypeCheck(ctx, widest.ID)
		if err != nil {
			return nil, err
		}
		pkg = pkgs[0]
		pgf, err := pkg.File(f.URI())
		if err != nil {
			return nil, err // "can't happen"
		}
		pos, err := pgf.PositionPos(pp)
		if err != nil {
			return nil, err
		}
		objects, _, err := objectsAt(pkg.TypesInfo(), pgf.File, pos)
		if err != nil {
			return nil, err
		}
		targets = objects
	}

	// Pick a representative object arbitrarily.
	// (All share the same name, pos, and kind.)
	var obj types.Object
	for obj = range targets {
		break
	}
	if obj.Name() == newName {
		return nil, fmt.Errorf("old and new names are the same: %s", newName)
	}
	if err := checkRenamable(obj); err != nil {
		return nil, err
	}

	// Find objectpath, if object is exported ("" otherwise).
	var declObjPath objectpath.Path
	if obj.Exported() {
		// objectpath.For requires the origin of a generic function or type, not an
		// instantiation (a bug?).
		//
		// Note that unlike Funcs, TypeNames are always canonical (they are "left"
		// of the type parameters, unlike methods).
		switch obj.(type) { // avoid "obj :=" since cases reassign the var
		case *types.TypeName:
			if _, ok := aliases.Unalias(obj.Type()).(*types.TypeParam); ok {
				// As with capitalized function parameters below, type parameters are
				// local.
				goto skipObjectPath
			}
		case *types.Func:
			obj = obj.(*types.Func).Origin()
		case *types.Var:
			// TODO(adonovan): do vars need the origin treatment too? (issue #58462)

			// Function parameter and result vars that are (unusually)
			// capitalized are technically exported, even though they
			// cannot be referenced, because they may affect downstream
			// error messages. But we can safely treat them as local.
			//
			// This is not merely an optimization: the renameExported
			// operation gets confused by such vars. It finds them from
			// objectpath, the classifies them as local vars, but as
			// they came from export data they lack syntax and the
			// correct scope tree (issue #61294).
			if !obj.(*types.Var).IsField() && !isPackageLevel(obj) {
				goto skipObjectPath
			}
		}
		if path, err := objectpath.For(obj); err == nil {
			declObjPath = path
		}
	skipObjectPath:
	}

	// Nonexported? Search locally.
	if declObjPath == "" {
		var objects []types.Object
		for obj := range targets {
			objects = append(objects, obj)
		}
		editMap, _, err := renameObjects(newName, pkg, objects...)
		return editMap, err
	}

	// Exported: search globally.
	//
	// For exported package-level var/const/func/type objects, the
	// search scope is just the direct importers.
	//
	// For exported fields and methods, the scope is the
	// transitive rdeps. (The exportedness of the field's struct
	// or method's receiver is irrelevant.)
	transitive := false
	switch obj := obj.(type) {
	case *types.TypeName:
		// Renaming an exported package-level type
		// requires us to inspect all transitive rdeps
		// in the event that the type is embedded.
		//
		// TODO(adonovan): opt: this is conservative
		// but inefficient. Instead, expand the scope
		// of the search only if we actually encounter
		// an embedding of the type, and only then to
		// the rdeps of the embedding package.
		if obj.Parent() == obj.Pkg().Scope() {
			transitive = true
		}

	case *types.Var:
		if obj.IsField() {
			transitive = true // field
		}

		// TODO(adonovan): opt: process only packages that
		// contain a reference (xrefs) to the target field.

	case *types.Func:
		if obj.Type().(*types.Signature).Recv() != nil {
			transitive = true // method
		}

		// It's tempting to optimize by skipping
		// packages that don't contain a reference to
		// the method in the xrefs index, but we still
		// need to apply the satisfy check to those
		// packages to find assignment statements that
		// might expands the scope of the renaming.
	}

	// Type-check all the packages to inspect.
	declURI := protocol.URIFromPath(pkg.FileSet().File(obj.Pos()).Name())
	pkgs, err := typeCheckReverseDependencies(ctx, snapshot, declURI, transitive)
	if err != nil {
		return nil, err
	}

	// Apply the renaming to the (initial) object.
	declPkgPath := PackagePath(obj.Pkg().Path())
	return renameExported(pkgs, declPkgPath, declObjPath, newName)
}

// typeCheckReverseDependencies returns the type-checked packages for
// the reverse dependencies of all packages variants containing
// file declURI. The packages are in some topological order.
//
// It includes all variants (even intermediate test variants) for the
// purposes of computing reverse dependencies, but discards ITVs for
// the actual renaming work.
//
// (This neglects obscure edge cases where a _test.go file changes the
// selectors used only in an ITV, but life is short. Also sin must be
// punished.)
func typeCheckReverseDependencies(ctx context.Context, snapshot *cache.Snapshot, declURI protocol.DocumentURI, transitive bool) ([]*cache.Package, error) {
	variants, err := snapshot.MetadataForFile(ctx, declURI)
	if err != nil {
		return nil, err
	}
	// variants must include ITVs for the reverse dependency
	// computation, but they are filtered out before we typecheck.
	allRdeps := make(map[PackageID]*metadata.Package)
	for _, variant := range variants {
		rdeps, err := snapshot.ReverseDependencies(ctx, variant.ID, transitive)
		if err != nil {
			return nil, err
		}
		allRdeps[variant.ID] = variant // include self
		for id, meta := range rdeps {
			allRdeps[id] = meta
		}
	}
	var ids []PackageID
	for id, meta := range allRdeps {
		if meta.IsIntermediateTestVariant() {
			continue
		}
		ids = append(ids, id)
	}

	// Sort the packages into some topological order of the
	// (unfiltered) metadata graph.
	metadata.SortPostOrder(snapshot, ids)

	// Dependencies must be visited first since they can expand
	// the search set. Ideally we would process the (filtered) set
	// of packages in the parallel postorder of the snapshot's
	// (unfiltered) metadata graph, but this is quite tricky
	// without a good graph abstraction.
	//
	// For now, we visit packages sequentially in order of
	// ascending height, like an inverted breadth-first search.
	//
	// Type checking is by far the dominant cost, so
	// overlapping it with renaming may not be worthwhile.
	return snapshot.TypeCheck(ctx, ids...)
}

// renameExported renames the object denoted by (pkgPath, objPath)
// within the specified packages, along with any other objects that
// must be renamed as a consequence. The slice of packages must be
// topologically ordered.
func renameExported(pkgs []*cache.Package, declPkgPath PackagePath, declObjPath objectpath.Path, newName string) (map[protocol.DocumentURI][]diff.Edit, error) {

	// A target is a name for an object that is stable across types.Packages.
	type target struct {
		pkg PackagePath
		obj objectpath.Path
	}

	// Populate the initial set of target objects.
	// This set may grow as we discover the consequences of each renaming.
	//
	// TODO(adonovan): strictly, each cone of reverse dependencies
	// of a single variant should have its own target map that
	// monotonically expands as we go up the import graph, because
	// declarations in test files can alter the set of
	// package-level names and change the meaning of field and
	// method selectors. So if we parallelize the graph
	// visitation (see above), we should also compute the targets
	// as a union of dependencies.
	//
	// Or we could decide that the logic below is fast enough not
	// to need parallelism. In small measurements so far the
	// type-checking step is about 95% and the renaming only 5%.
	targets := map[target]bool{{declPkgPath, declObjPath}: true}

	// Apply the renaming operation to each package.
	allEdits := make(map[protocol.DocumentURI][]diff.Edit)
	for _, pkg := range pkgs {

		// Resolved target objects within package pkg.
		var objects []types.Object
		for t := range targets {
			p := pkg.DependencyTypes(t.pkg)
			if p == nil {
				continue // indirect dependency of no consequence
			}
			obj, err := objectpath.Object(p, t.obj)
			if err != nil {
				// Possibly a method or an unexported type
				// that is not reachable through export data?
				// See https://github.com/golang/go/issues/60789.
				//
				// TODO(adonovan): it seems unsatisfactory that Object
				// should return an error for a "valid" path. Perhaps
				// we should define such paths as invalid and make
				// objectpath.For compute reachability?
				// Would that be a compatible change?
				continue
			}
			objects = append(objects, obj)
		}
		if len(objects) == 0 {
			continue // no targets of consequence to this package
		}

		// Apply the renaming.
		editMap, moreObjects, err := renameObjects(newName, pkg, objects...)
		if err != nil {
			return nil, err
		}

		// It is safe to concatenate the edits as they are non-overlapping
		// (or identical, in which case they will be de-duped by Rename).
		for uri, edits := range editMap {
			allEdits[uri] = append(allEdits[uri], edits...)
		}

		// Expand the search set?
		for obj := range moreObjects {
			objpath, err := objectpath.For(obj)
			if err != nil {
				continue // not exported
			}
			target := target{PackagePath(obj.Pkg().Path()), objpath}
			targets[target] = true

			// TODO(adonovan): methods requires dynamic
			// programming of the product targets x
			// packages as any package might add a new
			// target (from a forward dep) as a
			// consequence, and any target might imply a
			// new set of rdeps. See golang/go#58461.
		}
	}

	return allEdits, nil
}

// renamePackageName renames package declarations, imports, and go.mod files.
func renamePackageName(ctx context.Context, s *cache.Snapshot, f file.Handle, newName PackageName) (map[protocol.DocumentURI][]diff.Edit, error) {
	// Rename the package decl and all imports.
	renamingEdits, err := renamePackage(ctx, s, f, newName)
	if err != nil {
		return nil, err
	}

	// Update the last component of the file's enclosing directory.
	oldBase := filepath.Dir(f.URI().Path())
	newPkgDir := filepath.Join(filepath.Dir(oldBase), string(newName))

	// Update any affected replace directives in go.mod files.
	// TODO(adonovan): extract into its own function.
	//
	// Get all workspace modules.
	// TODO(adonovan): should this operate on all go.mod files,
	// irrespective of whether they are included in the workspace?
	modFiles := s.View().ModFiles()
	for _, m := range modFiles {
		fh, err := s.ReadFile(ctx, m)
		if err != nil {
			return nil, err
		}
		pm, err := s.ParseMod(ctx, fh)
		if err != nil {
			return nil, err
		}

		modFileDir := filepath.Dir(pm.URI.Path())
		affectedReplaces := []*modfile.Replace{}

		// Check if any replace directives need to be fixed
		for _, r := range pm.File.Replace {
			if !strings.HasPrefix(r.New.Path, "/") && !strings.HasPrefix(r.New.Path, "./") && !strings.HasPrefix(r.New.Path, "../") {
				continue
			}

			replacedPath := r.New.Path
			if strings.HasPrefix(r.New.Path, "./") || strings.HasPrefix(r.New.Path, "../") {
				replacedPath = filepath.Join(modFileDir, r.New.Path)
			}

			// TODO: Is there a risk of converting a '\' delimited replacement to a '/' delimited replacement?
			if !strings.HasPrefix(filepath.ToSlash(replacedPath)+"/", filepath.ToSlash(oldBase)+"/") {
				continue // not affected by the package renaming
			}

			affectedReplaces = append(affectedReplaces, r)
		}

		if len(affectedReplaces) == 0 {
			continue
		}
		copied, err := modfile.Parse("", pm.Mapper.Content, nil)
		if err != nil {
			return nil, err
		}

		for _, r := range affectedReplaces {
			replacedPath := r.New.Path
			if strings.HasPrefix(r.New.Path, "./") || strings.HasPrefix(r.New.Path, "../") {
				replacedPath = filepath.Join(modFileDir, r.New.Path)
			}

			suffix := strings.TrimPrefix(replacedPath, oldBase)

			newReplacedPath, err := filepath.Rel(modFileDir, newPkgDir+suffix)
			if err != nil {
				return nil, err
			}

			newReplacedPath = filepath.ToSlash(newReplacedPath)

			if !strings.HasPrefix(newReplacedPath, "/") && !strings.HasPrefix(newReplacedPath, "../") {
				newReplacedPath = "./" + newReplacedPath
			}

			if err := copied.AddReplace(r.Old.Path, "", newReplacedPath, ""); err != nil {
				return nil, err
			}
		}

		copied.Cleanup()
		newContent, err := copied.Format()
		if err != nil {
			return nil, err
		}

		// Calculate the edits to be made due to the change.
		edits := diff.Bytes(pm.Mapper.Content, newContent)
		renamingEdits[pm.URI] = append(renamingEdits[pm.URI], edits...)
	}

	return renamingEdits, nil
}

// renamePackage computes all workspace edits required to rename the package
// described by the given metadata, to newName, by renaming its package
// directory.
//
// It updates package clauses and import paths for the renamed package as well
// as any other packages affected by the directory renaming among all packages
// known to the snapshot.
func renamePackage(ctx context.Context, s *cache.Snapshot, f file.Handle, newName PackageName) (map[protocol.DocumentURI][]diff.Edit, error) {
	if strings.HasSuffix(string(newName), "_test") {
		return nil, fmt.Errorf("cannot rename to _test package")
	}

	// We need metadata for the relevant package and module paths.
	// These should be the same for all packages containing the file.
	meta, err := NarrowestMetadataForFile(ctx, s, f.URI())
	if err != nil {
		return nil, err
	}

	oldPkgPath := meta.PkgPath
	if meta.Module == nil {
		return nil, fmt.Errorf("cannot rename package: missing module information for package %q", meta.PkgPath)
	}
	modulePath := PackagePath(meta.Module.Path)
	if modulePath == oldPkgPath {
		return nil, fmt.Errorf("cannot rename package: module path %q is the same as the package path, so renaming the package directory would have no effect", modulePath)
	}

	newPathPrefix := path.Join(path.Dir(string(oldPkgPath)), string(newName))

	// We must inspect all packages, not just direct importers,
	// because we also rename subpackages, which may be unrelated.
	// (If the renamed package imports a subpackage it may require
	// edits to both its package and import decls.)
	allMetadata, err := s.AllMetadata(ctx)
	if err != nil {
		return nil, err
	}

	// Rename package and import declarations in all relevant packages.
	edits := make(map[protocol.DocumentURI][]diff.Edit)
	for _, mp := range allMetadata {
		// Special case: x_test packages for the renamed package will not have the
		// package path as a dir prefix, but still need their package clauses
		// renamed.
		if mp.PkgPath == oldPkgPath+"_test" {
			if err := renamePackageClause(ctx, mp, s, newName+"_test", edits); err != nil {
				return nil, err
			}
			continue
		}

		// Subtle: check this condition before checking for valid module info
		// below, because we should not fail this operation if unrelated packages
		// lack module info.
		if !strings.HasPrefix(string(mp.PkgPath)+"/", string(oldPkgPath)+"/") {
			continue // not affected by the package renaming
		}

		if mp.Module == nil {
			// This check will always fail under Bazel.
			return nil, fmt.Errorf("cannot rename package: missing module information for package %q", mp.PkgPath)
		}

		if modulePath != PackagePath(mp.Module.Path) {
			continue // don't edit imports if nested package and renaming package have different module paths
		}

		// Renaming a package consists of changing its import path and package name.
		suffix := strings.TrimPrefix(string(mp.PkgPath), string(oldPkgPath))
		newPath := newPathPrefix + suffix

		pkgName := mp.Name
		if mp.PkgPath == oldPkgPath {
			pkgName = newName

			if err := renamePackageClause(ctx, mp, s, newName, edits); err != nil {
				return nil, err
			}
		}

		imp := ImportPath(newPath) // TODO(adonovan): what if newPath has vendor/ prefix?
		if err := renameImports(ctx, s, mp, imp, pkgName, edits); err != nil {
			return nil, err
		}
	}

	return edits, nil
}

// renamePackageClause computes edits renaming the package clause of files in
// the package described by the given metadata, to newName.
//
// Edits are written into the edits map.
func renamePackageClause(ctx context.Context, mp *metadata.Package, snapshot *cache.Snapshot, newName PackageName, edits map[protocol.DocumentURI][]diff.Edit) error {
	// Rename internal references to the package in the renaming package.
	for _, uri := range mp.CompiledGoFiles {
		fh, err := snapshot.ReadFile(ctx, uri)
		if err != nil {
			return err
		}
		f, err := snapshot.ParseGo(ctx, fh, parsego.Header)
		if err != nil {
			return err
		}
		if f.File.Name == nil {
			continue // no package declaration
		}

		edit, err := posEdit(f.Tok, f.File.Name.Pos(), f.File.Name.End(), string(newName))
		if err != nil {
			return err
		}
		edits[f.URI] = append(edits[f.URI], edit)
	}

	return nil
}

// renameImports computes the set of edits to imports resulting from renaming
// the package described by the given metadata, to a package with import path
// newPath and name newName.
//
// Edits are written into the edits map.
func renameImports(ctx context.Context, snapshot *cache.Snapshot, mp *metadata.Package, newPath ImportPath, newName PackageName, allEdits map[protocol.DocumentURI][]diff.Edit) error {
	rdeps, err := snapshot.ReverseDependencies(ctx, mp.ID, false) // find direct importers
	if err != nil {
		return err
	}

	// Pass 1: rename import paths in import declarations.
	needsTypeCheck := make(map[PackageID][]protocol.DocumentURI)
	for _, rdep := range rdeps {
		if rdep.IsIntermediateTestVariant() {
			continue // for renaming, these variants are redundant
		}

		for _, uri := range rdep.CompiledGoFiles {
			fh, err := snapshot.ReadFile(ctx, uri)
			if err != nil {
				return err
			}
			f, err := snapshot.ParseGo(ctx, fh, parsego.Header)
			if err != nil {
				return err
			}
			if f.File.Name == nil {
				continue // no package declaration
			}
			for _, imp := range f.File.Imports {
				if rdep.DepsByImpPath[metadata.UnquoteImportPath(imp)] != mp.ID {
					continue // not the import we're looking for
				}

				// If the import does not explicitly specify
				// a local name, then we need to invoke the
				// type checker to locate references to update.
				//
				// TODO(adonovan): is this actually true?
				// Renaming an import with a local name can still
				// cause conflicts: shadowing of built-ins, or of
				// package-level decls in the same or another file.
				if imp.Name == nil {
					needsTypeCheck[rdep.ID] = append(needsTypeCheck[rdep.ID], uri)
				}

				// Create text edit for the import path (string literal).
				edit, err := posEdit(f.Tok, imp.Path.Pos(), imp.Path.End(), strconv.Quote(string(newPath)))
				if err != nil {
					return err
				}
				allEdits[uri] = append(allEdits[uri], edit)
			}
		}
	}

	// If the imported package's name hasn't changed,
	// we don't need to rename references within each file.
	if newName == mp.Name {
		return nil
	}

	// Pass 2: rename local name (types.PkgName) of imported
	// package throughout one or more files of the package.
	ids := make([]PackageID, 0, len(needsTypeCheck))
	for id := range needsTypeCheck {
		ids = append(ids, id)
	}
	pkgs, err := snapshot.TypeCheck(ctx, ids...)
	if err != nil {
		return err
	}
	for i, id := range ids {
		pkg := pkgs[i]
		for _, uri := range needsTypeCheck[id] {
			f, err := pkg.File(uri)
			if err != nil {
				return err
			}
			for _, imp := range f.File.Imports {
				if imp.Name != nil {
					continue // has explicit local name
				}
				if rdeps[id].DepsByImpPath[metadata.UnquoteImportPath(imp)] != mp.ID {
					continue // not the import we're looking for
				}

				pkgname := pkg.TypesInfo().Implicits[imp].(*types.PkgName)

				pkgScope := pkg.Types().Scope()
				fileScope := pkg.TypesInfo().Scopes[f.File]

				localName := string(newName)
				try := 0

				// Keep trying with fresh names until one succeeds.
				//
				// TODO(adonovan): fix: this loop is not sufficient to choose a name
				// that is guaranteed to be conflict-free; renameObj may still fail.
				// So the retry loop should be around renameObj, and we shouldn't
				// bother with scopes here.
				for fileScope.Lookup(localName) != nil || pkgScope.Lookup(localName) != nil {
					try++
					localName = fmt.Sprintf("%s%d", newName, try)
				}

				// renameObj detects various conflicts, including:
				// - new name conflicts with a package-level decl in this file;
				// - new name hides a package-level decl in another file that
				//   is actually referenced in this file;
				// - new name hides a built-in that is actually referenced
				//   in this file;
				// - a reference in this file to the old package name would
				//   become shadowed by an intervening declaration that
				//   uses the new name.
				// It returns the edits if no conflict was detected.
				editMap, _, err := renameObjects(localName, pkg, pkgname)
				if err != nil {
					return err
				}

				// If the chosen local package name matches the package's
				// new name, delete the change that would have inserted
				// an explicit local name, which is always the lexically
				// first change.
				if localName == string(newName) {
					edits, ok := editMap[uri]
					if !ok {
						return fmt.Errorf("internal error: no changes for %s", uri)
					}
					diff.SortEdits(edits)
					editMap[uri] = edits[1:]
				}
				for uri, edits := range editMap {
					allEdits[uri] = append(allEdits[uri], edits...)
				}
			}
		}
	}
	return nil
}

// renameObjects computes the edits to the type-checked syntax package pkg
// required to rename a set of target objects to newName.
//
// It also returns the set of objects that were found (due to
// corresponding methods and embedded fields) to require renaming as a
// consequence of the requested renamings.
//
// It returns an error if the renaming would cause a conflict.
func renameObjects(newName string, pkg *cache.Package, targets ...types.Object) (map[protocol.DocumentURI][]diff.Edit, map[types.Object]bool, error) {
	r := renamer{
		pkg:          pkg,
		objsToUpdate: make(map[types.Object]bool),
		from:         targets[0].Name(),
		to:           newName,
	}

	// A renaming initiated at an interface method indicates the
	// intention to rename abstract and concrete methods as needed
	// to preserve assignability.
	// TODO(adonovan): pull this into the caller.
	for _, obj := range targets {
		if obj, ok := obj.(*types.Func); ok {
			recv := obj.Type().(*types.Signature).Recv()
			if recv != nil && types.IsInterface(recv.Type().Underlying()) {
				r.changeMethods = true
				break
			}
		}
	}

	// Check that the renaming of the identifier is ok.
	for _, obj := range targets {
		r.check(obj)
		if len(r.conflicts) > 0 {
			// Stop at first error.
			return nil, nil, fmt.Errorf("%s", strings.Join(r.conflicts, "\n"))
		}
	}

	editMap, err := r.update()
	if err != nil {
		return nil, nil, err
	}

	// Remove initial targets so that only 'consequences' remain.
	for _, obj := range targets {
		delete(r.objsToUpdate, obj)
	}
	return editMap, r.objsToUpdate, nil
}

// Rename all references to the target objects.
func (r *renamer) update() (map[protocol.DocumentURI][]diff.Edit, error) {
	result := make(map[protocol.DocumentURI][]diff.Edit)

	// shouldUpdate reports whether obj is one of (or an
	// instantiation of one of) the target objects.
	shouldUpdate := func(obj types.Object) bool {
		return containsOrigin(r.objsToUpdate, obj)
	}

	// Find all identifiers in the package that define or use a
	// renamed object. We iterate over info as it is more efficient
	// than calling ast.Inspect for each of r.pkg.CompiledGoFiles().
	type item struct {
		node  ast.Node // Ident, ImportSpec (obj=PkgName), or CaseClause (obj=Var)
		obj   types.Object
		isDef bool
	}
	var items []item
	info := r.pkg.TypesInfo()
	for id, obj := range info.Uses {
		if shouldUpdate(obj) {
			items = append(items, item{id, obj, false})
		}
	}
	for id, obj := range info.Defs {
		if shouldUpdate(obj) {
			items = append(items, item{id, obj, true})
		}
	}
	for node, obj := range info.Implicits {
		if shouldUpdate(obj) {
			switch node.(type) {
			case *ast.ImportSpec, *ast.CaseClause:
				items = append(items, item{node, obj, true})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].node.Pos() < items[j].node.Pos()
	})

	// Update each identifier, and its doc comment if it is a declaration.
	for _, item := range items {
		pgf, ok := enclosingFile(r.pkg, item.node.Pos())
		if !ok {
			bug.Reportf("edit does not belong to syntax of package %q", r.pkg)
			continue
		}

		// Renaming a types.PkgName may result in the addition or removal of an identifier,
		// so we deal with this separately.
		if pkgName, ok := item.obj.(*types.PkgName); ok && item.isDef {
			edit, err := r.updatePkgName(pgf, pkgName)
			if err != nil {
				return nil, err
			}
			result[pgf.URI] = append(result[pgf.URI], edit)
			continue
		}

		// Workaround the unfortunate lack of a Var object
		// for x in "switch x := expr.(type) {}" by adjusting
		// the case clause to the switch ident.
		// This may result in duplicate edits, but we de-dup later.
		if _, ok := item.node.(*ast.CaseClause); ok {
			path, _ := astutil.PathEnclosingInterval(pgf.File, item.obj.Pos(), item.obj.Pos())
			item.node = path[0].(*ast.Ident)
		}

		// Replace the identifier with r.to.
		edit, err := posEdit(pgf.Tok, item.node.Pos(), item.node.End(), r.to)
		if err != nil {
			return nil, err
		}

		result[pgf.URI] = append(result[pgf.URI], edit)

		if !item.isDef { // uses do not have doc comments to update.
			continue
		}

		doc := docComment(pgf, item.node.(*ast.Ident))
		if doc == nil {
			continue
		}

		// Perform the rename in doc comments declared in the original package.
		// go/parser strips out \r\n returns from the comment text, so go
		// line-by-line through the comment text to get the correct positions.
		docRegexp := regexp.MustCompile(`\b` + r.from + `\b`) // valid identifier => valid regexp
		for _, comment := range doc.List {
			if isDirective(comment.Text) {
				continue
			}
			// TODO(adonovan): why are we looping over lines?
			// Just run the loop body once over the entire multiline comment.
			lines := strings.Split(comment.Text, "\n")
			tokFile := pgf.Tok
			commentLine := safetoken.Line(tokFile, comment.Pos())
			uri := protocol.URIFromPath(tokFile.Name())
			for i, line := range lines {
				lineStart := comment.Pos()
				if i > 0 {
					lineStart = tokFile.LineStart(commentLine + i)
				}
				for _, locs := range docRegexp.FindAllIndex([]byte(line), -1) {
					edit, err := posEdit(tokFile, lineStart+token.Pos(locs[0]), lineStart+token.Pos(locs[1]), r.to)
					if err != nil {
						return nil, err // can't happen
					}
					result[uri] = append(result[uri], edit)
				}
			}
		}
	}

	docLinkEdits, err := r.updateCommentDocLinks()
	if err != nil {
		return nil, err
	}
	for uri, edits := range docLinkEdits {
		result[uri] = append(result[uri], edits...)
	}

	return result, nil
}

// updateCommentDocLinks updates each doc comment in the package
// that refers to one of the renamed objects using a doc link
// (https://golang.org/doc/comment#doclinks) such as "[pkg.Type.Method]".
func (r *renamer) updateCommentDocLinks() (map[protocol.DocumentURI][]diff.Edit, error) {
	result := make(map[protocol.DocumentURI][]diff.Edit)
	var docRenamers []*docLinkRenamer
	for obj := range r.objsToUpdate {
		if _, ok := obj.(*types.PkgName); ok {
			// The dot package name will not be referenced
			if obj.Name() == "." {
				continue
			}

			docRenamers = append(docRenamers, &docLinkRenamer{
				isDep:       false,
				isPkgOrType: true,
				file:        r.pkg.FileSet().File(obj.Pos()),
				regexp:      docLinkPattern("", "", obj.Name(), true),
				to:          r.to,
			})
			continue
		}
		if !obj.Exported() {
			continue
		}
		recvName := ""
		// Doc links can reference only exported package-level objects
		// and methods of exported package-level named types.
		if !isPackageLevel(obj) {
			obj, isFunc := obj.(*types.Func)
			if !isFunc {
				continue
			}
			recv := obj.Type().(*types.Signature).Recv()
			if recv == nil {
				continue
			}
			_, named := typesinternal.ReceiverNamed(recv)
			if named == nil {
				continue
			}
			// Doc links can't reference interface methods.
			if types.IsInterface(named.Underlying()) {
				continue
			}
			name := named.Origin().Obj()
			if !name.Exported() || !isPackageLevel(name) {
				continue
			}
			recvName = name.Name()
		}

		// Qualify objects from other packages.
		pkgName := ""
		if r.pkg.Types() != obj.Pkg() {
			pkgName = obj.Pkg().Name()
		}
		_, isTypeName := obj.(*types.TypeName)
		docRenamers = append(docRenamers, &docLinkRenamer{
			isDep:       r.pkg.Types() != obj.Pkg(),
			isPkgOrType: isTypeName,
			packagePath: obj.Pkg().Path(),
			packageName: pkgName,
			recvName:    recvName,
			objName:     obj.Name(),
			regexp:      docLinkPattern(pkgName, recvName, obj.Name(), isTypeName),
			to:          r.to,
		})
	}
	for _, pgf := range r.pkg.CompiledGoFiles() {
		for _, d := range docRenamers {
			edits, err := d.update(pgf)
			if err != nil {
				return nil, err
			}
			if len(edits) > 0 {
				result[pgf.URI] = append(result[pgf.URI], edits...)
			}
		}
	}
	return result, nil
}

// docLinkPattern returns a regular expression that matches doclinks in comments.
// It has one submatch that indicates the symbol to be updated.
func docLinkPattern(pkgName, recvName, objName string, isPkgOrType bool) *regexp.Regexp {
	// The doc link may contain a leading star, e.g. [*bytes.Buffer].
	pattern := `\[\*?`
	if pkgName != "" {
		pattern += pkgName + `\.`
	}
	if recvName != "" {
		pattern += recvName + `\.`
	}
	// The first submatch is object name.
	pattern += `(` + objName + `)`
	// If the object is a *types.TypeName or *types.PkgName, also need
	// match the objects referenced by them, so add `(\.\w+)*`.
	if isPkgOrType {
		pattern += `(?:\.\w+)*`
	}
	// There are two type of link in comments:
	//   1. url link. e.g. [text]: url
	//   2. doc link. e.g. [pkg.Name]
	// in order to only match the doc link, add `([^:]|$)` in the end.
	pattern += `\](?:[^:]|$)`

	return regexp.MustCompile(pattern)
}

// A docLinkRenamer renames doc links of forms such as these:
//
//	[Func]
//	[pkg.Func]
//	[RecvType.Method]
//	[*Type]
//	[*pkg.Type]
//	[*pkg.RecvType.Method]
type docLinkRenamer struct {
	isDep       bool // object is from a dependency package
	isPkgOrType bool // object is *types.PkgName or *types.TypeName
	packagePath string
	packageName string // e.g. "pkg"
	recvName    string // e.g. "RecvType"
	objName     string // e.g. "Func", "Type", "Method"
	to          string // new name
	regexp      *regexp.Regexp

	file *token.File // enclosing file, if renaming *types.PkgName
}

// update updates doc links in the package level comments.
func (r *docLinkRenamer) update(pgf *parsego.File) (result []diff.Edit, err error) {
	if r.file != nil && r.file != pgf.Tok {
		return nil, nil
	}
	pattern := r.regexp
	// If the object is in dependency package,
	// the imported name in the file may be different from the original package name
	if r.isDep {
		for _, spec := range pgf.File.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			if importPath == r.packagePath {
				// Ignore blank imports
				if spec.Name == nil || spec.Name.Name == "_" || spec.Name.Name == "." {
					continue
				}
				if spec.Name.Name != r.packageName {
					pattern = docLinkPattern(spec.Name.Name, r.recvName, r.objName, r.isPkgOrType)
				}
				break
			}
		}
	}

	var edits []diff.Edit
	updateDocLinks := func(doc *ast.CommentGroup) error {
		if doc != nil {
			for _, c := range doc.List {
				for _, locs := range pattern.FindAllStringSubmatchIndex(c.Text, -1) {
					// The first submatch is the object name, so the locs[2:4] is the index of object name.
					edit, err := posEdit(pgf.Tok, c.Pos()+token.Pos(locs[2]), c.Pos()+token.Pos(locs[3]), r.to)
					if err != nil {
						return err
					}
					edits = append(edits, edit)
				}
			}
		}
		return nil
	}

	// Update package doc comments.
	err = updateDocLinks(pgf.File.Doc)
	if err != nil {
		return nil, err
	}
	for _, decl := range pgf.File.Decls {
		var doc *ast.CommentGroup
		switch decl := decl.(type) {
		case *ast.GenDecl:
			doc = decl.Doc
		case *ast.FuncDecl:
			doc = decl.Doc
		}
		err = updateDocLinks(doc)
		if err != nil {
			return nil, err
		}
	}
	return edits, nil
}

// docComment returns the doc for an identifier within the specified file.
func docComment(pgf *parsego.File, id *ast.Ident) *ast.CommentGroup {
	nodes, _ := astutil.PathEnclosingInterval(pgf.File, id.Pos(), id.End())
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
		case *ast.AssignStmt:
			// *ast.AssignStmt doesn't have an associated comment group.
			// So, we try to find a comment just before the identifier.

			// Try to find a comment group only for short variable declarations (:=).
			if decl.Tok != token.DEFINE {
				return nil
			}

			identLine := safetoken.Line(pgf.Tok, id.Pos())
			for _, comment := range nodes[len(nodes)-1].(*ast.File).Comments {
				if comment.Pos() > id.Pos() {
					// Comment is after the identifier.
					continue
				}

				lastCommentLine := safetoken.Line(pgf.Tok, comment.End())
				if lastCommentLine+1 == identLine {
					return comment
				}
			}
		default:
			return nil
		}
	}
	return nil
}

// updatePkgName returns the updates to rename a pkgName in the import spec by
// only modifying the package name portion of the import declaration.
func (r *renamer) updatePkgName(pgf *parsego.File, pkgName *types.PkgName) (diff.Edit, error) {
	// Modify ImportSpec syntax to add or remove the Name as needed.
	path, _ := astutil.PathEnclosingInterval(pgf.File, pkgName.Pos(), pkgName.Pos())
	if len(path) < 2 {
		return diff.Edit{}, fmt.Errorf("no path enclosing interval for %s", pkgName.Name())
	}
	spec, ok := path[1].(*ast.ImportSpec)
	if !ok {
		return diff.Edit{}, fmt.Errorf("failed to update PkgName for %s", pkgName.Name())
	}

	newText := ""
	if pkgName.Imported().Name() != r.to {
		newText = r.to + " "
	}

	// Replace the portion (possibly empty) of the spec before the path:
	//     local "path"      or      "path"
	//   ->      <-                -><-
	return posEdit(pgf.Tok, spec.Pos(), spec.Path.Pos(), newText)
}

// parsePackageNameDecl is a convenience function that parses and
// returns the package name declaration of file fh, and reports
// whether the position ppos lies within it.
//
// Note: also used by references.
func parsePackageNameDecl(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, ppos protocol.Position) (*parsego.File, bool, error) {
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Header)
	if err != nil {
		return nil, false, err
	}
	// Careful: because we used parsego.Header,
	// pgf.Pos(ppos) may be beyond EOF => (0, err).
	pos, _ := pgf.PositionPos(ppos)
	return pgf, pgf.File.Name.Pos() <= pos && pos <= pgf.File.Name.End(), nil
}

// enclosingFile returns the CompiledGoFile of pkg that contains the specified position.
func enclosingFile(pkg *cache.Package, pos token.Pos) (*parsego.File, bool) {
	for _, pgf := range pkg.CompiledGoFiles() {
		if pgf.File.Pos() <= pos && pos <= pgf.File.End() {
			return pgf, true
		}
	}
	return nil, false
}

// posEdit returns an edit to replace the (start, end) range of tf with 'new'.
func posEdit(tf *token.File, start, end token.Pos, new string) (diff.Edit, error) {
	startOffset, endOffset, err := safetoken.Offsets(tf, start, end)
	if err != nil {
		return diff.Edit{}, err
	}
	return diff.Edit{Start: startOffset, End: endOffset, New: new}, nil
}
