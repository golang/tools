// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package mcp

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/internal/mcp"
)

// symbolReferencesParams defines the parameters for the "go_symbol_references"
// tool.
type symbolReferencesParams struct {
	File   string `json:"file"`
	Symbol string `json:"symbol"`
}

// symbolReferencesTool returns a new server tool for finding references to a Go symbol.
func (h *handler) symbolReferencesTool() *mcp.ServerTool {
	desc := `Provides the locations of references to a (possibly qualified)
package-level Go symbol referenced from the current file.

For example, given arguments {"file": "/path/to/foo.go", "name": "Foo"},
go_symbol_references returns references to the symbol "Foo" declared
in the current package.

Similarly, given arguments {"file": "/path/to/foo.go", "name": "lib.Bar"},
go_symbol_references returns references to the symbol "Bar" in the imported lib
package.

Finally, symbol references supporting querying fields and methods: symbol
"T.M" selects the "M" field or method of the "T" type (or value), and "lib.T.M"
does the same for a symbol in the imported package "lib".
`
	return mcp.NewServerTool(
		"go_symbol_references",
		desc,
		h.symbolReferencesHandler,
		mcp.Input(
			mcp.Property("file", mcp.Description("the absolute path to the file containing the symbol")),
			mcp.Property("symbol", mcp.Description("the symbol or qualified symbol (for example \"foo\" or \"pkg.Foo\")")),
		),
	)
}

// symbolReferencesHandler is the handler for the "go_symbol_references" tool.
// It finds all references to the requested symbol and describes their
// locations.
func (h *handler) symbolReferencesHandler(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[symbolReferencesParams]) (*mcp.CallToolResultFor[any], error) {
	fh, snapshot, release, err := h.fileOf(ctx, params.Arguments.File)
	if err != nil {
		return nil, err
	}
	defer release()

	if snapshot.FileKind(fh) != file.Go {
		return nil, fmt.Errorf("can't provide references for non-Go files")
	}

	// Parse and extract names before type checking, to fail fast in the case of
	// invalid inputs.
	e, err := parser.ParseExpr(params.Arguments.Symbol)
	if err != nil {
		return nil, fmt.Errorf("\"symbol\" failed to parse: %v", err)
	}
	path, err := extractPath(e)
	if err != nil {
		return nil, err
	}

	pkg, pgf, err := golang.NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}

	target, err := resolveSymbol(path, pkg, pgf)
	if err != nil {
		return nil, err
	}

	loc, err := golang.ObjectLocation(ctx, pkg.FileSet(), snapshot, target)
	if err != nil {
		return nil, fmt.Errorf("finding symbol location: %v", err)
	}
	declFH, err := snapshot.ReadFile(ctx, loc.URI)
	if err != nil {
		return nil, err
	}
	refs, err := golang.References(ctx, snapshot, declFH, loc.Range.Start, true)
	if err != nil {
		return nil, err
	}
	return formatReferences(ctx, snapshot, refs)
}

// extractPath extracts the 'path' of names from e, which must be of the form
// a, a.b, or a.b.c.
//
// If a nil error is returned, the resulting path is either length 1, 2, or 3.
func extractPath(e ast.Expr) ([]string, error) {
	switch e := e.(type) {
	case *ast.Ident:
		return []string{e.Name}, nil
	case *ast.SelectorExpr:
		switch x := e.X.(type) {
		case *ast.Ident:
			// Qualified identifier 'a.b', where a is a package or receiver.
			return []string{x.Name, e.Sel.Name}, nil
		case *ast.SelectorExpr:
			// Imported field or method a.b.c: a must be a package name.
			if x2, ok := x.X.(*ast.Ident); ok {
				return []string{x2.Name, x.Sel.Name, e.Sel.Name}, nil
			}
		}
	}
	return nil, fmt.Errorf("invalid qualified symbol: expected a.b or a.b.c")
}

// resolveSymbol resolves the types.Object for the given qualified path, which
// must be of length 1, 2, or 3:
//   - For length 1 paths, the symbol is a name in the file scope.
//   - For length 2 paths, the symbol is either field, method, or imported symbol.
//   - For length 3 paths, the symbol is a field or method on an important object.
func resolveSymbol(path []string, pkg *cache.Package, pgf *parsego.File) (types.Object, error) {
	fileScope, ok := pkg.TypesInfo().Scopes[pgf.File]
	if !ok {
		return nil, fmt.Errorf("internal error: no scope for file")
	}

	switch len(path) {
	case 1:
		_, target := fileScope.LookupParent(path[0], token.NoPos)
		return target, nil
	case 2:
		switch _, obj := fileScope.LookupParent(path[0], token.NoPos); obj := obj.(type) {
		case *types.PkgName:
			return obj.Imported().Scope().Lookup(path[1]), nil
		case nil:
			return nil, fmt.Errorf("failed to resolve name %q", path[0])
		default:
			target, _, _ := types.LookupFieldOrMethod(obj.Type(), true, pkg.Types(), path[1])
			return target, nil
		}
	case 3:
		// Imported field or method a.b.c: a must be a package name.
		obj := fileScope.Lookup(path[0])
		p, ok := obj.(*types.PkgName)
		if !ok {
			return nil, fmt.Errorf("invalid qualified symbol: %q must be a package (got %T)", path[0], obj)
		}
		recv := p.Imported().Scope().Lookup(path[1])
		if recv == nil {
			return nil, fmt.Errorf("invalid qualified symbol: could not find %q in package %q", path[1], path[0])
		}
		target, _, _ := types.LookupFieldOrMethod(recv.Type(), true, pkg.Types(), path[2])
		return target, nil
	}
	panic("unreachable")
}
