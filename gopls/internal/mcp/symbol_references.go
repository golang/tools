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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
)

// symbolReferencesParams defines the parameters for the "go_symbol_references"
// tool.
type symbolReferencesParams struct {
	File   string `json:"file" jsonschema:"the absolute path to the file containing the symbol"`
	Symbol string `json:"symbol" jsonschema:"the symbol or qualified symbol"`
}

// symbolReferencesHandler is the handler for the "go_symbol_references" tool.
// It finds all references to the requested symbol and describes their
// locations.
func (h *handler) symbolReferencesHandler(ctx context.Context, req *mcp.CallToolRequest, params symbolReferencesParams) (*mcp.CallToolResult, any, error) {
	countGoSymbolReferencesMCP.Inc()
	fh, snapshot, release, err := h.fileOf(ctx, params.File)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	if snapshot.FileKind(fh) != file.Go {
		return nil, nil, fmt.Errorf("can't provide references for non-Go files")
	}

	loc, err := symbolLocation(ctx, snapshot, fh.URI(), params.Symbol)
	if err != nil {
		return nil, nil, err
	}
	declFH, err := snapshot.ReadFile(ctx, loc.URI)
	if err != nil {
		return nil, nil, err
	}
	refs, err := golang.References(ctx, snapshot, declFH, loc.Range.Start, true)
	if err != nil {
		return nil, nil, err
	}
	formatted, err := formatReferences(ctx, snapshot, refs)
	return formatted, nil, err
}

// symbolLocation returns the protocol.Location of the given symbol within the file uri, or an error if it cannot be located.
func symbolLocation(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, symbol string) (protocol.Location, error) {
	// Parse and extract names before type checking, to fail fast in the case of
	// invalid inputs.
	e, err := parser.ParseExpr(symbol)
	if err != nil {
		return protocol.Location{}, fmt.Errorf("\"symbol\" failed to parse: %v", err)
	}
	path, err := extractPath(e)
	if err != nil {
		return protocol.Location{}, err
	}

	pkg, pgf, err := golang.NarrowestPackageForFile(ctx, snapshot, uri)
	if err != nil {
		return protocol.Location{}, err
	}

	target, err := resolveSymbol(path, pkg, pgf)
	if err != nil {
		return protocol.Location{}, err
	}

	loc, err := golang.ObjectLocation(ctx, pkg.FileSet(), snapshot, target)
	if err != nil {
		return protocol.Location{}, fmt.Errorf("finding symbol location: %v", err)
	}
	return loc, nil
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
		if target == nil {
			return nil, fmt.Errorf("failed to resolve name %q", path[0])
		}
		return target, nil
	case 2:
		switch _, obj := fileScope.LookupParent(path[0], token.NoPos); obj := obj.(type) {
		case *types.PkgName:
			target := obj.Imported().Scope().Lookup(path[1])
			if target == nil {
				return nil, fmt.Errorf("failed to resolve member %q of %q", path[1], path[0])
			}
			return target, nil
		case nil:
			return nil, fmt.Errorf("failed to resolve name %q", path[0])
		default:
			target, _, _ := types.LookupFieldOrMethod(obj.Type(), true, pkg.Types(), path[1])
			if target == nil {
				return nil, fmt.Errorf("failed to resolve member %q of %q", path[1], path[0])
			}
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
		if target == nil {
			return nil, fmt.Errorf("failed to resolve member %q of %q", path[2], path[1])
		}
		return target, nil
	}
	panic("unreachable")
}
