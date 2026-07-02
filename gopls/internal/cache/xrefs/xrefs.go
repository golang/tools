// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package xrefs defines the serializable index of cross-package
// references that is computed during type checking.
//
// See ../references.go for the 'references' query.
package xrefs

import (
	"go/ast"
	"go/types"
	"sort"

	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/asm"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/frob"
	"golang.org/x/tools/gopls/internal/util/morestrings"
)

// NewIndex constructs an index of outbound cross-references for the
// specified type-checked package.
//
// Callers indexing many packages should share one objectpath Encoder
// so that a heavily referenced package's object paths are encoded
// once rather than once per referencing package.
func NewIndex(enc *objectpath.Encoder, pkg *types.Package, info *types.Info, files []*parsego.File, asmFiles []*asm.File) *Index {
	// pkgObjects maps each referenced package Q to a mapping:
	// from each referenced symbol in Q to the ordered list
	// of references to that symbol from this package.
	// A nil types.Object indicates a reference
	// to the package as a whole: an import.
	pkgObjects := make(map[*types.Package]map[types.Object]*gobObject)

	// getObjects returns the object-to-references mapping for a package.
	getObjects := func(pkg *types.Package) map[types.Object]*gobObject {
		objects, ok := pkgObjects[pkg]
		if !ok {
			objects = make(map[types.Object]*gobObject)
			pkgObjects[pkg] = objects
		}
		return objects
	}

	for fileIndex, pgf := range files {
		// Avoid pgf.Cursor() here to prevent materialization
		// of an Inspector during workspace reindexing, which
		// increases peak and retained memory usage.
		for n := range ast.Preorder(pgf.File) {
			switch n := n.(type) {
			case *ast.Ident:
				// Report a reference for each identifier that
				// uses a symbol exported from another package.
				// (The built-in error.Error method has no package.)
				if n.IsExported() {
					if obj, ok := info.Uses[n]; ok &&
						obj.Pkg() != nil &&
						obj.Pkg() != pkg {

						// For instantiations of generic methods,
						// use the generic object (see issue #60622).
						if fn, ok := obj.(*types.Func); ok {
							obj = fn.Origin()
						}

						objects := getObjects(obj.Pkg())
						gobObj, ok := objects[obj]
						if !ok {
							path, err := enc.For(obj)
							if err != nil {
								// Capitalized but not exported
								// (e.g. local const/var/type).
								continue
							}
							gobObj = &gobObject{Path: path}
							objects[obj] = gobObj
						}

						// golang/go#66683: nodes can under/overflow the file.
						// For example, "var _ = x." creates a SelectorExpr(Sel=Ident("_"))
						// that is beyond EOF. (Arguably Ident.Name should be "".)
						if rng, err := pgf.NodeRange(n); err == nil {
							gobObj.Refs = append(gobObj.Refs, gobRef{
								FileIndex: fileIndex,
								Range:     rng,
							})
						}
					}
				}

			case *ast.ImportSpec:
				// Report a reference from each import path
				// string to the imported package.
				pkgname := info.PkgNameOf(n)
				if pkgname == nil {
					continue // missing import
				}
				objects := getObjects(pkgname.Imported())
				gobObj, ok := objects[nil]
				if !ok {
					gobObj = &gobObject{Path: ""}
					objects[nil] = gobObj
				}
				// golang/go#66683: nodes can under/overflow the file.
				if rng, err := pgf.NodeRange(n.Path); err == nil {
					gobObj.Refs = append(gobObj.Refs, gobRef{
						FileIndex: fileIndex,
						Range:     rng,
					})
				} else {
					bug.Reportf("out of bounds import spec %+v", n.Path)
				}
			}
		}
	}

	// For each asm file, record cross-package references.
	// Within-package asm references are found by localReferences
	// scanning syntax, not by the xrefs index.

	// Build a mapping from import path to package, so that each
	// cross-package identifier can be resolved without a linear
	// scan of pkg.Imports() for every identifier.
	importsByPath := make(map[string]*types.Package, len(pkg.Imports()))
	for _, imp := range pkg.Imports() {
		importsByPath[imp.Path()] = imp
	}
	for fileIndex, file := range asmFiles {
		for _, id := range file.Idents {
			if id.Kind != asm.Data && id.Kind != asm.Ref {
				continue
			}
			pkgpath, name, ok := morestrings.CutLast(id.Name, ".")
			if !ok {
				continue
			}
			if pkgpath == "" || pkgpath == pkg.Path() {
				// Within-package reference; skip (handled by localReferences).
				continue
			}
			// Cross-package reference: find the dependency package.
			//
			// TODO(Groot Guo): assembly may legally reference
			// non-dependencies (e.g. sync/atomic calls internal/runtime/atomic).
			// Currently we only search direct imports; see goasm.Definition
			// which searches the full metadata graph.
			depPkg, ok := importsByPath[pkgpath]
			if !ok {
				continue
			}
			obj := depPkg.Scope().Lookup(name)
			if obj == nil {
				continue
			}
			objects := getObjects(depPkg)
			gobObj, ok := objects[obj]
			if !ok {
				path, err := enc.For(obj)
				if err != nil {
					continue
				}
				gobObj = &gobObject{Path: path}
				objects[obj] = gobObj
			}
			if rng, err := file.IdentRange(id); err == nil {
				gobObj.Refs = append(gobObj.Refs, gobRef{
					// FileIndex for asm files is offset by len(files)
					// (i.e. the number of compiledGoFiles).
					// Lookup reverses this by comparing against
					// len(mp.CompiledGoFiles); the two counts must be equal.
					FileIndex: len(files) + fileIndex,
					Range:     rng,
				})
			}
		}
	}

	// Flatten the maps into slices, and sort for determinism.
	var packages []*gobPackage
	for p := range pkgObjects {
		objects := pkgObjects[p]
		gp := &gobPackage{
			PkgPath: metadata.PackagePath(p.Path()),
			Objects: make([]*gobObject, 0, len(objects)),
		}
		for _, gobObj := range objects {
			gp.Objects = append(gp.Objects, gobObj)
		}
		sort.Slice(gp.Objects, func(i, j int) bool {
			return gp.Objects[i].Path < gp.Objects[j].Path
		})
		packages = append(packages, gp)
	}
	sort.Slice(packages, func(i, j int) bool {
		return packages[i].PkgPath < packages[j].PkgPath
	})

	return &Index{packages: packages}
}

// An Index is an index of outbound symbol references for one
// specific package.
type Index struct {
	packages []*gobPackage
}

// Encode encodes the index for storage.
func (idx *Index) Encode() []byte {
	return packageCodec.Encode(idx.packages)
}

// Decode decodes a serialized cross-reference index.
// It is suitable for use as a filecache.Get decoder.
func Decode(data []byte) *Index {
	return &Index{packages: packageCodec.Decode(data)}
}

// Lookup searches the index for references to any object in the target set,
// returning their locations within the package described by mp.
// Each target object is denoted by a pair of (package path, object path).
func (idx *Index) Lookup(mp *metadata.Package, targets map[metadata.PackagePath]map[objectpath.Path]struct{}) (locs []protocol.Location) {
	for _, gp := range idx.packages {
		if objectSet, ok := targets[gp.PkgPath]; ok {
			for _, gobObj := range gp.Objects {
				if _, ok := objectSet[gobObj.Path]; ok {
					for _, ref := range gobObj.Refs {
						var uri protocol.DocumentURI
						if asmIndex := ref.FileIndex - len(mp.CompiledGoFiles); asmIndex < 0 {
							// CompiledGoFile reference.
							// Invariant: len(files) passed to NewIndex
							// equals len(mp.CompiledGoFiles).
							uri = mp.CompiledGoFiles[ref.FileIndex]
						} else {
							uri = mp.AsmFiles[asmIndex]
						}
						locs = append(locs, protocol.Location{
							URI:   uri,
							Range: ref.Range,
						})
					}
				}
			}
		}
	}

	return locs
}

// -- serialized representation --

// The cross-reference index records the location of all references
// from one package to symbols defined in other packages
// (dependencies). It does not record within-package references.
// The index for package P consists of a list of gopPackage records,
// each enumerating references to symbols defined a single dependency, Q.

// TODO(adonovan): opt: choose a more compact encoding.
// The gobRef.Range field is the obvious place to begin.

// (The name says gob but in fact we use frob.)
var packageCodec = frob.CodecFor[[]*gobPackage]()

// A gobPackage records the set of outgoing references from the index
// package to symbols defined in a dependency package.
type gobPackage struct {
	PkgPath metadata.PackagePath // defining package (Q)
	Objects []*gobObject         // set of Q objects referenced by P
}

// A gobObject records all references to a particular symbol.
type gobObject struct {
	Path objectpath.Path // symbol name within package; "" => import of package itself
	Refs []gobRef        // locations of references within P, in lexical order
}

type gobRef struct {
	FileIndex int            // index of enclosing file within P's CompiledGoFiles + AsmFiles
	Range     protocol.Range // source range of reference
}
