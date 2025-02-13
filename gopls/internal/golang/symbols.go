// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/internal/event"
)

func DocumentSymbols(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.DocumentSymbol, error) {
	ctx, done := event.Start(ctx, "golang.DocumentSymbols")
	defer done()

	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return nil, fmt.Errorf("getting file for DocumentSymbols: %w", err)
	}

	// Build symbols for file declarations. When encountering a declaration with
	// errors (typically because positions are invalid), we skip the declaration
	// entirely. VS Code fails to show any symbols if one of the top-level
	// symbols is missing position information.
	var symbols []protocol.DocumentSymbol
	for _, decl := range pgf.File.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Name.Name == "_" {
				continue
			}
			fs, err := funcSymbol(pgf.Mapper, pgf.Tok, decl)
			if err == nil {
				// If function is a method, prepend the type of the method.
				if decl.Recv != nil && len(decl.Recv.List) > 0 {
					fs.Name = fmt.Sprintf("(%s).%s", types.ExprString(decl.Recv.List[0].Type), fs.Name)
				}
				symbols = append(symbols, fs)
			}
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if spec.Name.Name == "_" {
						continue
					}
					ts, err := typeSymbol(pgf.Mapper, pgf.Tok, spec)
					if err == nil {
						symbols = append(symbols, ts)
					}
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						if name.Name == "_" {
							continue
						}
						vs, err := varSymbol(pgf.Mapper, pgf.Tok, spec, name, decl.Tok == token.CONST)
						if err == nil {
							symbols = append(symbols, vs)
						}
					}
				}
			}
		}
	}
	return symbols, nil
}

// PackageSymbols returns a list of symbols in the narrowest package for the given file (specified
// by its URI).
// Methods with receivers are stored as children under the symbol for their receiver type.
// The PackageSymbol data type contains the same fields as protocol.DocumentSymbol, with
// an additional int field "File" that stores the index of that symbol's file in the
// PackageSymbolsResult.Files.
func PackageSymbols(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) (command.PackageSymbolsResult, error) {
	ctx, done := event.Start(ctx, "source.PackageSymbols")
	defer done()

	pkgFiles := []protocol.DocumentURI{uri}

	// golang/vscode-go#3681: do our best if the file is not in a package.
	// TODO(rfindley): revisit this in the future once there is more graceful
	// handling in VS Code.
	if mp, err := NarrowestMetadataForFile(ctx, snapshot, uri); err == nil {
		pkgFiles = mp.CompiledGoFiles
	}

	var (
		pkgName           string
		symbols           []command.PackageSymbol
		receiverToMethods = make(map[string][]command.PackageSymbol) // receiver name -> methods
		typeSymbolToIdx   = make(map[string]int)                     // type name -> index in symbols
	)
	for fidx, f := range pkgFiles {
		fh, err := snapshot.ReadFile(ctx, f)
		if err != nil {
			return command.PackageSymbolsResult{}, err
		}
		pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
		if err != nil {
			return command.PackageSymbolsResult{}, err
		}
		if pkgName == "" && pgf.File != nil && pgf.File.Name != nil {
			pkgName = pgf.File.Name.Name
		}
		for _, decl := range pgf.File.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Name.Name == "_" {
					continue
				}
				if fs, err := funcSymbol(pgf.Mapper, pgf.Tok, decl); err == nil {
					// If function is a method, prepend the type of the method.
					// Don't add the method as its own symbol; store it so we can
					// add it as a child of the receiver type later
					if decl.Recv != nil && len(decl.Recv.List) > 0 {
						_, rname, _ := astutil.UnpackRecv(decl.Recv.List[0].Type)
						receiverToMethods[rname.String()] = append(receiverToMethods[rname.String()], toPackageSymbol(fidx, fs))
					} else {
						symbols = append(symbols, toPackageSymbol(fidx, fs))
					}
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if spec.Name.Name == "_" {
							continue
						}
						if ts, err := typeSymbol(pgf.Mapper, pgf.Tok, spec); err == nil {
							typeSymbolToIdx[ts.Name] = len(symbols)
							symbols = append(symbols, toPackageSymbol(fidx, ts))
						}
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							if name.Name == "_" {
								continue
							}
							if vs, err := varSymbol(pgf.Mapper, pgf.Tok, spec, name, decl.Tok == token.CONST); err == nil {
								symbols = append(symbols, toPackageSymbol(fidx, vs))
							}
						}
					}
				}
			}
		}
	}
	// Add methods as the child of their receiver type symbol
	for recv, methods := range receiverToMethods {
		if i, ok := typeSymbolToIdx[recv]; ok {
			symbols[i].Children = append(symbols[i].Children, methods...)
		}
	}
	return command.PackageSymbolsResult{
		PackageName: pkgName,
		Files:       pkgFiles,
		Symbols:     symbols,
	}, nil

}

func toPackageSymbol(fileIndex int, s protocol.DocumentSymbol) command.PackageSymbol {
	var res command.PackageSymbol
	res.Name = s.Name
	res.Detail = s.Detail
	res.Kind = s.Kind
	res.Tags = s.Tags
	res.Range = s.Range
	res.SelectionRange = s.SelectionRange

	children := make([]command.PackageSymbol, len(s.Children))
	for i, c := range s.Children {
		children[i] = toPackageSymbol(fileIndex, c)
	}
	res.Children = children

	res.File = fileIndex
	return res
}

func funcSymbol(m *protocol.Mapper, tf *token.File, decl *ast.FuncDecl) (protocol.DocumentSymbol, error) {
	s := protocol.DocumentSymbol{
		Name: decl.Name.Name,
		Kind: protocol.Function,
	}
	if decl.Recv != nil {
		s.Kind = protocol.Method
	}
	var err error
	s.Range, err = m.NodeRange(tf, decl)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.SelectionRange, err = m.NodeRange(tf, decl.Name)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.Detail = types.ExprString(decl.Type)
	return s, nil
}

func typeSymbol(m *protocol.Mapper, tf *token.File, spec *ast.TypeSpec) (protocol.DocumentSymbol, error) {
	s := protocol.DocumentSymbol{
		Name: spec.Name.Name,
	}
	var err error
	s.Range, err = m.NodeRange(tf, spec)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.SelectionRange, err = m.NodeRange(tf, spec.Name)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.Kind, s.Detail, s.Children = typeDetails(m, tf, spec.Type)
	return s, nil
}

func typeDetails(m *protocol.Mapper, tf *token.File, typExpr ast.Expr) (kind protocol.SymbolKind, detail string, children []protocol.DocumentSymbol) {
	switch typExpr := typExpr.(type) {
	case *ast.StructType:
		kind = protocol.Struct
		children = fieldListSymbols(m, tf, typExpr.Fields, protocol.Field)
		if len(children) > 0 {
			detail = "struct{...}"
		} else {
			detail = "struct{}"
		}

		// Find interface methods and embedded types.
	case *ast.InterfaceType:
		kind = protocol.Interface
		children = fieldListSymbols(m, tf, typExpr.Methods, protocol.Method)
		if len(children) > 0 {
			detail = "interface{...}"
		} else {
			detail = "interface{}"
		}

	case *ast.FuncType:
		kind = protocol.Function
		detail = types.ExprString(typExpr)

	default:
		kind = protocol.Class // catch-all, for cases where we don't know the kind syntactically
		detail = types.ExprString(typExpr)
	}
	return
}

func fieldListSymbols(m *protocol.Mapper, tf *token.File, fields *ast.FieldList, fieldKind protocol.SymbolKind) []protocol.DocumentSymbol {
	if fields == nil {
		return nil
	}

	var symbols []protocol.DocumentSymbol
	for _, field := range fields.List {
		detail, children := "", []protocol.DocumentSymbol(nil)
		if field.Type != nil {
			_, detail, children = typeDetails(m, tf, field.Type)
		}
		if len(field.Names) == 0 { // embedded interface or struct field
			// By default, use the formatted type details as the name of this field.
			// This handles potentially invalid syntax, as well as type embeddings in
			// interfaces.
			child := protocol.DocumentSymbol{
				Name:     detail,
				Kind:     protocol.Field, // consider all embeddings to be fields
				Children: children,
			}

			// If the field is a valid embedding, promote the type name to field
			// name.
			selection := field.Type
			if id := embeddedIdent(field.Type); id != nil {
				child.Name = id.Name
				child.Detail = detail
				selection = id
			}

			if rng, err := m.NodeRange(tf, field.Type); err == nil {
				child.Range = rng
			}
			if rng, err := m.NodeRange(tf, selection); err == nil {
				child.SelectionRange = rng
			}

			symbols = append(symbols, child)
		} else {
			for _, name := range field.Names {
				child := protocol.DocumentSymbol{
					Name:     name.Name,
					Kind:     fieldKind,
					Detail:   detail,
					Children: children,
				}

				if rng, err := m.NodeRange(tf, field); err == nil {
					child.Range = rng
				}
				if rng, err := m.NodeRange(tf, name); err == nil {
					child.SelectionRange = rng
				}

				symbols = append(symbols, child)
			}
		}

	}
	return symbols
}

func varSymbol(m *protocol.Mapper, tf *token.File, spec *ast.ValueSpec, name *ast.Ident, isConst bool) (protocol.DocumentSymbol, error) {
	s := protocol.DocumentSymbol{
		Name: name.Name,
		Kind: protocol.Variable,
	}
	if isConst {
		s.Kind = protocol.Constant
	}
	var err error
	s.Range, err = m.NodeRange(tf, spec)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	s.SelectionRange, err = m.NodeRange(tf, name)
	if err != nil {
		return protocol.DocumentSymbol{}, err
	}
	if spec.Type != nil { // type may be missing from the syntax
		_, s.Detail, s.Children = typeDetails(m, tf, spec.Type)
	}
	return s, nil
}
