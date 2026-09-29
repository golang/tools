// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/golang/stubmethods"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	internalastutil "golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/packagepath"
	"golang.org/x/tools/internal/typesinternal"
)

var (
	// InterfaceQuestion asks which interface to implement, preferring workspace
	// symbol search (LazyEnum) and falling back to a plain string prompt.
	InterfaceQuestion = formQuestion[string, typesinternal.NamedOrAlias]{
		ID:          "interface",
		Description: `fully qualified interface identifier path/to/pkg.interface; e.g., "net.Error"`,
		Required:    true,
		Default:     "error",
		Types: []any{
			protocol.FormFieldTypeLazyEnum{
				Kind:   protocol.FormFieldKindLazyEnum,
				Source: "workspaceSymbol",
				Config: mustMarshal(InteractiveWorkspaceSymbolEnumConfig{
					Kinds: []protocol.SymbolKind{protocol.Interface},
				}),
			},
			protocol.FormFieldTypeString{
				Kind: protocol.FormFieldKindString,
			},
		},
		// convert is set at the call site since type resolution requires a snapshot.
	}

	// TypeParamQuestion asks for a type argument to instantiate a generic interface,
	// preferring workspace symbol search (LazyEnum) and falling back to a plain string prompt.
	//
	// TODO(hxjiang): customize the question description for each type parameter
	// (e.g. include the type parameter name and constraint) so multiple prompts are
	// not identical and confusing to the user.
	TypeParamQuestion = formQuestion[string, types.Type]{
		ID:          "typeParam",
		Description: `type argument for the generic interface; e.g., "int" or "path/to/pkg.Type"`,
		Required:    true,
		Types: []any{
			protocol.FormFieldTypeLazyEnum{
				Kind:   protocol.FormFieldKindLazyEnum,
				Source: "workspaceSymbol",
				Config: mustMarshal(InteractiveWorkspaceSymbolEnumConfig{
					Kinds: []protocol.SymbolKind{
						protocol.Class,     // type Foo int & type Foo = int
						protocol.Struct,    // type Foo struct {}
						protocol.Interface, // type Foo interface {}
					},
				}),
			},
			protocol.FormFieldTypeString{
				Kind: protocol.FormFieldKindString,
			},
		},
		// convert is set at the call site since type resolution requires a snapshot.
	}

	implementInterfaceQuestions = []question{InterfaceQuestion, TypeParamQuestion}
)

// ConvertInterface returns a convert function that resolves and validates an
// interface name in snapshot.
func ConvertInterface(ctx context.Context, snapshot *cache.Snapshot) func(string) (typesinternal.NamedOrAlias, error) {
	return func(name string) (typesinternal.NamedOrAlias, error) {
		typ, err := lookupType(ctx, snapshot, name)
		if err != nil {
			return nil, err
		}
		u, ok := typ.Underlying().(*types.Interface)
		if !ok {
			return nil, fmt.Errorf("%s is not an interface", name)
		}
		if !u.IsMethodSet() { // type-constraint interfaces, "type Foo interface {~int|~int64}".
			return nil, fmt.Errorf("%s is a type constraint, not a method-set interface", name)
		}
		return typ.(typesinternal.NamedOrAlias), nil
	}
}

// ConvertTypeParam returns a convert function that resolves and validates a
// type argument name in snapshot.
func ConvertTypeParam(ctx context.Context, snapshot *cache.Snapshot) func(string) (types.Type, error) {
	return func(name string) (types.Type, error) {
		typ, err := lookupType(ctx, snapshot, name)
		if err != nil {
			return nil, err
		}
		if u, ok := typ.Underlying().(*types.Interface); ok && !u.IsMethodSet() { // type-constraint interfaces, "type Foo interface {~int|~int64}".
			return nil, fmt.Errorf("type constraint %s cannot be used as a type argument", name)
		}
		if na, ok := typ.(typesinternal.NamedOrAlias); ok && na.TypeParams().Len() > 0 { // generic type
			return nil, fmt.Errorf("generic type %s cannot be used as a type argument without instantiation", name)
		}
		// TODO(hxjiang): verify that typ satisfies the constraint of the
		// corresponding type parameter, and return an error if not.
		return typ, nil
	}
}

// lookupType resolves a predeclared type (e.g., "error", "int") or a
// package-qualified type name (e.g., "example.com/pkg.Type") in the workspace.
//
// TODO(aputman): see if this can share code or be refactored together with ResolveTarget.
func lookupType(ctx context.Context, snapshot *cache.Snapshot, name string) (types.Type, error) {
	pkgPath, symName, ok := strings.CutLast(name, ".")
	if !ok {
		obj := types.Universe.Lookup(name)
		if obj == nil {
			return nil, fmt.Errorf(`invalid type name %q: want predeclared type or "example.com/pkg.Type"`, name)
		}
		tn, ok := obj.(*types.TypeName)
		if !ok {
			return nil, fmt.Errorf("%s is a %s, not a type", name, typesinternal.ObjectKind(obj))
		}
		return tn.Type(), nil
	}

	if err := module.CheckImportPath(pkgPath); err != nil {
		return nil, fmt.Errorf("invalid package path %w", err)
	}
	if !token.IsIdentifier(symName) {
		return nil, fmt.Errorf("invalid type name: %q", symName)
	}
	mps, ok := snapshot.MetadataGraph().ForPackagePath[metadata.PackagePath(pkgPath)]
	if !ok {
		return nil, fmt.Errorf("package %q is not in the workspace", pkgPath)
	}
	if len(mps) == 0 {
		return nil, fmt.Errorf("no package metadata for package %q", pkgPath)
	}
	pkgs, err := snapshot.TypeCheck(ctx, mps[0].ID)
	if err != nil {
		return nil, err
	}
	obj := pkgs[0].Types().Scope().Lookup(symName)
	if obj == nil {
		return nil, fmt.Errorf("symbol %q not found in package %q", symName, pkgPath)
	}
	tn, ok := obj.(*types.TypeName)
	if !ok {
		return nil, fmt.Errorf("%s.%s is a %s, not a type", pkgPath, symName, typesinternal.ObjectKind(obj))
	}
	return tn.Type(), nil
}

func mustMarshal(x any) json.RawMessage {
	data, err := json.Marshal(x)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(data)
}

// ImplementInterface generates workspace edits to add method stubs, making the
// package-level type at the given location implement the target interface.
// If iface is a generic interface, it must already be instantiated.
func ImplementInterface(ctx context.Context, snapshot *cache.Snapshot, loc protocol.Location, iface typesinternal.NamedOrAlias) ([]protocol.DocumentChange, error) {
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
	// extraPackages maps each referenced package to the method that introduced it.
	extraPackages := make(map[*types.Package]*types.Func)
	for m := range iface.Underlying().(*types.Interface).Methods() {
		if !m.Exported() && m.Pkg() != named.Obj().Pkg() {
			return nil, fmt.Errorf("cannot add unexported method %s from package %s to type %s", m.Name(), namedPkg.Name, named.Obj().Name())
		}
		// Extract all external packages referenced in the method signature.
		_ = types.TypeString(m.Type(), func(p *types.Package) string {
			if p != nil && p != named.Obj().Pkg() {
				extraPackages[p] = m
			}
			return ""
		})
	}
	dependingOnX := snapshot.MetadataGraph().ReverseReflexiveTransitiveClosure(namedPkg.ID)
	for p, method := range extraPackages {
		mp, err := metadataPkgForPath(p.Path())
		if err != nil {
			return nil, err
		}

		if _, ok := dependingOnX[mp.ID]; ok {
			return nil, fmt.Errorf("adding method %s to type %s would create an import cycle", method.Name(), named.Obj().Name())
		}

		if !packagepath.CanImport(namedPkg.String(), p.Path()) {
			return nil, fmt.Errorf("adding method %s to type %s would require import of inaccessible package %s", method.Name(), named.Obj().Name(), p.Name())
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
