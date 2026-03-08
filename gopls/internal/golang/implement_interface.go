// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/golang/stubmethods"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	internalastutil "golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/packagepath"
	"golang.org/x/tools/internal/typesinternal"
)

// ImplementInterface generates workspace edits to add method stubs, making the
// package-level type at the given location implement the target interface.
//
// The ifaceStr must be "error" or a fully qualified name (e.g.,
// "example.com/pkg.Type").
func ImplementInterface(ctx context.Context, snapshot *cache.Snapshot, loc protocol.Location, ifaceStr string) (changes []protocol.DocumentChange, _ error) {
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, loc.URI)
	if err != nil {
		return nil, err
	}

	metadataPkgForPath := func(pkgPath string) (*metadata.Package, error) {
		mps, ok := snapshot.MetadataGraph().ForPackagePath[metadata.PackagePath(pkgPath)]
		if !ok {
			return nil, fmt.Errorf("package %q is not in the workspace", pkgPath)
		}

		if len(mps) == 0 {
			return nil, fmt.Errorf("no package metadata for package %q", pkgPath)
		}

		return mps[0], nil
	}

	var (
		iface    *types.TypeName
		ifacePkg *cache.Package
	)
	{
		if ifaceStr == "error" {
			iface = types.Universe.Lookup("error").(*types.TypeName)
		} else {
			lastDot := strings.LastIndex(ifaceStr, ".")
			if lastDot == -1 {
				return nil, fmt.Errorf(
					`invalid interface type name (want string of form "example.com/pkg.Type"`)
			} else {
				// Do not assume the target package is already imported or type-checked
				// in the current context. Look it up globally via the metadata graph.
				pkgPath := ifaceStr[:lastDot]
				symName := ifaceStr[lastDot+1:]

				mp, err := metadataPkgForPath(pkgPath)
				if err != nil {
					return nil, err
				}

				pkgs, err := snapshot.TypeCheck(ctx, mp.ID)
				if err != nil {
					return nil, err
				}

				ifacePkg = pkgs[0]

				obj := ifacePkg.Types().Scope().Lookup(symName)
				if obj == nil {
					return nil, fmt.Errorf("symbol %q not found in package %q", symName, pkgPath)
				}

				var ok bool
				iface, ok = obj.(*types.TypeName)
				if !ok {
					return nil, fmt.Errorf("%s.%s is a %s, not a type", pkgPath, symName, typesinternal.ObjectKind(obj))
				}

				if !types.IsInterface(iface.Type()) {
					return nil, fmt.Errorf("%s.%s is not an interface", pkgPath, symName)
				}
			}
		}
	}

	var (
		named    *types.Named
		namedPkg *metadata.Package
	)
	{
		start, end, err := pgf.RangePos(loc.Range)
		if err != nil {
			return nil, err
		}
		cur, _, _, _ := internalastutil.Select(pgf.Cursor(), start, end) // can't fail: pgf contains pos

		spec, curSpec := cursorutil.FirstEnclosing[*ast.TypeSpec](cur)
		if spec == nil {
			return nil, fmt.Errorf("no enclosing type declaration")
		}

		// Only package level.
		if curSpec.Parent().Parent().Node() != pgf.File {
			return nil, fmt.Errorf("enclosing type %s is not at package level", spec.Name.Name)
		}

		t, ok := types.Unalias(pkg.TypesInfo().TypeOf(spec.Name)).(*types.Named)
		if !ok {
			return nil, fmt.Errorf("enclosing type is not a named type")
		}

		if is[*types.Pointer](t.Underlying()) {
			return nil, fmt.Errorf("cannot declare concrete methods on a pointer type %s", t.Obj().Name())
		}

		if types.IsInterface(t) {
			return nil, fmt.Errorf("cannot declare concrete methods on a interface type %s", t.Obj().Name())
		}

		named = t
		namedPkgPath := t.Obj().Pkg().Path()

		namedPkg, err = metadataPkgForPath(namedPkgPath)
		if err != nil {
			return nil, err
		}
	}

	// Reject cases that would add cycle-forming or disallowed internal imports
	// for types mentioned in the added methods.
	if ifaceStr != "error" && namedPkg != ifacePkg.Metadata() {
		// extraPackages maps each referenced package to the method that introduced it.
		extraPackages := make(map[*types.Package]*types.Func)
		dependingOnX := snapshot.MetadataGraph().ReverseReflexiveTransitiveClosure(namedPkg.ID)
		for m := range iface.Type().Underlying().(*types.Interface).Methods() {
			if !m.Exported() {
				return nil, fmt.Errorf("cannot add unexported method %s from package %s to type %s", m.Name(), namedPkg.Name, named.Obj().Name())
			}
			// Extract all packages referenced in the method signature.
			_ = types.TypeString(m.Type(), func(p *types.Package) string {
				extraPackages[p] = m
				return ""
			})
		}
		for p, method := range extraPackages {
			mp, err := metadataPkgForPath(p.Path())
			if err != nil {
				return nil, err
			}

			if _, ok := dependingOnX[mp.ID]; ok {
				return nil, fmt.Errorf("adding method %s to type %s would create an import cycle", method.Name(), named.Obj().Name())
			}

			if !packagepath.CanImport(namedPkg.String(), p.Path()) {
				return nil, fmt.Errorf("can not import package %s", p.Path())
			}
		}
	}

	// TODO(hxjiang): if the package contains the interface is visible and
	// importable from the package contains the named type, consider add:
	//    var _ Interface = (*Type)(nil)
	si := stubmethods.IfaceStubInfo{
		Fset:      pkg.FileSet(),
		Interface: iface,
		Concrete:  named,
		// TODO(hxjiang): consider make it question and let the user decide
		// whether to use pointer receiver or not.
		Pointer: true, // by default, use pointer receiver
	}

	// TODO(hxjiang): fix the comment position after insert the methods, see test
	// result in testdata/codeaction/implement_interface.txt basic/good/good.go
	fixFset, suggestion, err := insertDeclsAfter(ctx, snapshot, pkg.Metadata(), si.Fset, si.Concrete.Obj(), si.Emit)
	if err != nil {
		return nil, err
	}

	return suggestedFixToDocumentChange(ctx, snapshot, fixFset, suggestion)
}
