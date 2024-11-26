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
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/frob"
)

// Index constructs a serializable index of outbound cross-references
// for the specified type-checked package.
func Index(files []*parsego.File, pkg *types.Package, info *types.Info) []byte {
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

	objectpathFor := new(objectpath.Encoder).For

	for fileIndex, pgf := range files {
		ast.Inspect(pgf.File, func(n ast.Node) bool {
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
							path, err := objectpathFor(obj)
							if err != nil {
								// Capitalized but not exported
								// (e.g. local const/var/type).
								return true
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
					return true // missing import
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
			return true
		})
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

	return packageCodec.Encode(packages)
}

// Lookup searches a serialized index produced by an indexPackage
// operation on m, and returns the locations of all references from m
// to any object in the target set. Each object is denoted by a pair
// of (package path, object path).
func Lookup(mp *metadata.Package, data []byte, targets map[metadata.PackagePath]map[objectpath.Path]struct{}) (locs []protocol.Location) {
	var packages []*gobPackage
	packageCodec.Decode(data, &packages)
	for _, gp := range packages {
		if objectSet, ok := targets[gp.PkgPath]; ok {
			for _, gobObj := range gp.Objects {
				if _, ok := objectSet[gobObj.Path]; ok {
					for _, ref := range gobObj.Refs {
						uri := mp.CompiledGoFiles[ref.FileIndex]
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
	FileIndex int            // index of enclosing file within P's CompiledGoFiles
	Range     protocol.Range // source range of reference
}
