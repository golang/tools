// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package symbols defines the serializable index of package symbols extracted
// from parsed package files.
package symbols

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/gopls/internal/util/frob"
)

// Symbol holds a precomputed symbol value. This is a subset of the information
// in the full protocol.SymbolInformation struct to reduce the size of each
// symbol.
type Symbol struct {
	Name  string
	Kind  protocol.SymbolKind
	Range protocol.Range
}

// A Package holds information about symbols declared by each file of a
// package.
//
// The symbols included are: package-level declarations, and fields and methods
// of type declarations.
type Package struct {
	Files   []protocol.DocumentURI // package files
	Symbols [][]Symbol             // symbols in each file
}

var codec = frob.CodecFor[Package]()

// Decode decodes data from [Package.Encode].
func Decode(data []byte) *Package {
	var pkg Package
	codec.Decode(data, &pkg)
	return &pkg
}

// Encode encodes the package.
func (pkg *Package) Encode() []byte {
	return codec.Encode(*pkg)
}

// New returns a new [Package] summarizing symbols in the given files.
func New(files []*parsego.File) *Package {
	var (
		uris    []protocol.DocumentURI
		symbols [][]Symbol
	)
	for _, pgf := range files {
		uris = append(uris, pgf.URI)
		syms := symbolizeFile(pgf)
		symbols = append(symbols, syms)
	}
	return &Package{
		Files:   uris,
		Symbols: symbols,
	}
}

// symbolizeFile reads and parses a file and extracts symbols from it.
func symbolizeFile(pgf *parsego.File) []Symbol {
	w := &symbolWalker{
		nodeRange: pgf.NodeRange,
	}

	for _, decl := range pgf.File.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			kind := protocol.Function
			var recv *ast.Ident
			if decl.Recv.NumFields() > 0 {
				kind = protocol.Method
				_, recv, _ = astutil.UnpackRecv(decl.Recv.List[0].Type)
			}
			w.declare(decl.Name.Name, kind, decl.Name, recv)

		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					kind := protocol.Class
					switch spec.Type.(type) {
					case *ast.InterfaceType:
						kind = protocol.Interface
					case *ast.StructType:
						kind = protocol.Struct
					case *ast.FuncType:
						kind = protocol.Function
					}
					w.declare(spec.Name.Name, kind, spec.Name)
					w.walkType(spec.Type, spec.Name)
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						kind := protocol.Variable
						if decl.Tok == token.CONST {
							kind = protocol.Constant
						}
						w.declare(name.Name, kind, name)
					}
				}
			}
		}
	}

	return w.symbols
}

type symbolWalker struct {
	nodeRange func(node ast.Node) (protocol.Range, error) // for computing positions

	symbols []Symbol
}

// declare declares a symbol of the specified name, kind, node location, and enclosing dotted path of identifiers.
func (w *symbolWalker) declare(name string, kind protocol.SymbolKind, node ast.Node, path ...*ast.Ident) {
	var b strings.Builder
	for _, ident := range path {
		if ident != nil {
			b.WriteString(ident.Name)
			b.WriteString(".")
		}
	}
	b.WriteString(name)

	rng, err := w.nodeRange(node)
	if err != nil {
		// TODO(rfindley): establish an invariant that node positions cannot exceed
		// the file. This is not currently the case--for example see
		// golang/go#48300 (this can also happen due to phantom selectors).
		//
		// For now, we have nothing to do with this error.
		return
	}
	sym := Symbol{
		Name:  b.String(),
		Kind:  kind,
		Range: rng,
	}
	w.symbols = append(w.symbols, sym)
}

// walkType processes symbols related to a type expression. path is path of
// nested type identifiers to the type expression.
func (w *symbolWalker) walkType(typ ast.Expr, path ...*ast.Ident) {
	switch st := typ.(type) {
	case *ast.StructType:
		for _, field := range st.Fields.List {
			w.walkField(field, protocol.Field, protocol.Field, path...)
		}
	case *ast.InterfaceType:
		for _, field := range st.Methods.List {
			w.walkField(field, protocol.Interface, protocol.Method, path...)
		}
	}
}

// walkField processes symbols related to the struct field or interface method.
//
// unnamedKind and namedKind are the symbol kinds if the field is resp. unnamed
// or named. path is the path of nested identifiers containing the field.
func (w *symbolWalker) walkField(field *ast.Field, unnamedKind, namedKind protocol.SymbolKind, path ...*ast.Ident) {
	if len(field.Names) == 0 {
		switch typ := field.Type.(type) {
		case *ast.SelectorExpr:
			// embedded qualified type
			w.declare(typ.Sel.Name, unnamedKind, field, path...)
		default:
			w.declare(types.ExprString(field.Type), unnamedKind, field, path...)
		}
	}
	for _, name := range field.Names {
		w.declare(name.Name, namedKind, name, path...)
		w.walkType(field.Type, append(path, name)...)
	}
}
