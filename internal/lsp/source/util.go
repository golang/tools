// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/span"
)

type mappedRange struct {
	spanRange span.Range
	m         *protocol.ColumnMapper

	// protocolRange is the result of converting the spanRange using the mapper.
	// It is computed on-demand.
	protocolRange *protocol.Range
}

func (s mappedRange) Range() (protocol.Range, error) {
	if s.protocolRange == nil {
		spn, err := s.spanRange.Span()
		if err != nil {
			return protocol.Range{}, err
		}
		prng, err := s.m.Range(spn)
		if err != nil {
			return protocol.Range{}, err
		}
		s.protocolRange = &prng
	}
	return *s.protocolRange, nil
}

func (s mappedRange) URI() span.URI {
	return s.m.URI
}

func IsGenerated(ctx context.Context, view View, uri span.URI) bool {
	f, err := view.GetFile(ctx, uri)
	if err != nil {
		return false
	}
	ph := view.Session().Cache().ParseGoHandle(f.Handle(ctx), ParseHeader)
	parsed, err := ph.Parse(ctx)
	if parsed == nil {
		return false
	}
	tok := view.Session().Cache().FileSet().File(parsed.Pos())
	if tok == nil {
		return false
	}
	for _, commentGroup := range parsed.Comments {
		for _, comment := range commentGroup.List {
			if matched := generatedRx.MatchString(comment.Text); matched {
				// Check if comment is at the beginning of the line in source.
				if pos := tok.Position(comment.Slash); pos.Column == 1 {
					return true
				}
			}
		}
	}
	return false
}

// Matches cgo generated comment as well as the proposed standard:
//	https://golang.org/s/generatedcode
var generatedRx = regexp.MustCompile(`// .*DO NOT EDIT\.?`)

func DetectLanguage(langID, filename string) FileKind {
	switch langID {
	case "go":
		return Go
	case "go.mod":
		return Mod
	case "go.sum":
		return Sum
	}
	// Fallback to detecting the language based on the file extension.
	switch filepath.Ext(filename) {
	case ".mod":
		return Mod
	case ".sum":
		return Sum
	default: // fallback to Go
		return Go
	}
}

func (k FileKind) String() string {
	switch k {
	case Mod:
		return "go.mod"
	case Sum:
		return "go.sum"
	default:
		return "go"
	}
}

// indexExprAtPos returns the index of the expression containing pos.
func indexExprAtPos(pos token.Pos, args []ast.Expr) int {
	for i, expr := range args {
		if expr.Pos() <= pos && pos <= expr.End() {
			return i
		}
	}
	return len(args)
}

func exprAtPos(pos token.Pos, args []ast.Expr) ast.Expr {
	for _, expr := range args {
		if expr.Pos() <= pos && pos <= expr.End() {
			return expr
		}
	}
	return nil
}

// fieldSelections returns the set of fields that can
// be selected from a value of type T.
func fieldSelections(T types.Type) (fields []*types.Var) {
	// TODO(adonovan): this algorithm doesn't exclude ambiguous
	// selections that match more than one field/method.
	// types.NewSelectionSet should do that for us.

	seen := make(map[*types.Var]bool) // for termination on recursive types

	var visit func(T types.Type)
	visit = func(T types.Type) {
		if T, ok := deref(T).Underlying().(*types.Struct); ok {
			for i := 0; i < T.NumFields(); i++ {
				f := T.Field(i)
				if seen[f] {
					continue
				}
				seen[f] = true
				fields = append(fields, f)
				if f.Anonymous() {
					visit(f.Type())
				}
			}
		}
	}
	visit(T)

	return fields
}

// resolveInvalid traverses the node of the AST that defines the scope
// containing the declaration of obj, and attempts to find a user-friendly
// name for its invalid type. The resulting Object and its Type are fake.
func resolveInvalid(obj types.Object, node ast.Node, info *types.Info) types.Object {
	// Construct a fake type for the object and return a fake object with this type.
	formatResult := func(expr ast.Expr) types.Object {
		var typename string
		switch t := expr.(type) {
		case *ast.SelectorExpr:
			typename = fmt.Sprintf("%s.%s", t.X, t.Sel)
		case *ast.Ident:
			typename = t.String()
		default:
			return nil
		}
		typ := types.NewNamed(types.NewTypeName(token.NoPos, obj.Pkg(), typename, nil), types.Typ[types.Invalid], nil)
		return types.NewVar(obj.Pos(), obj.Pkg(), obj.Name(), typ)
	}
	var resultExpr ast.Expr
	ast.Inspect(node, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.ValueSpec:
			for _, name := range n.Names {
				if info.Defs[name] == obj {
					resultExpr = n.Type
				}
			}
			return false
		case *ast.Field: // This case handles parameters and results of a FuncDecl or FuncLit.
			for _, name := range n.Names {
				if info.Defs[name] == obj {
					resultExpr = n.Type
				}
			}
			return false
		// TODO(rstambler): Handle range statements.
		default:
			return true
		}
	})
	return formatResult(resultExpr)
}

func lookupBuiltinDecl(v View, name string) interface{} {
	builtinPkg := v.BuiltinPackage()
	if builtinPkg == nil || builtinPkg.Scope == nil {
		return nil
	}
	obj := builtinPkg.Scope.Lookup(name)
	if obj == nil {
		return nil
	}
	return obj.Decl
}

func isPointer(T types.Type) bool {
	_, ok := T.(*types.Pointer)
	return ok
}

// deref returns a pointer's element type; otherwise it returns typ.
func deref(typ types.Type) types.Type {
	if p, ok := typ.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	return typ
}

func isTypeName(obj types.Object) bool {
	_, ok := obj.(*types.TypeName)
	return ok
}

func isFunc(obj types.Object) bool {
	_, ok := obj.(*types.Func)
	return ok
}

// typeConversion returns the type being converted to if call is a type
// conversion expression.
func typeConversion(call *ast.CallExpr, info *types.Info) types.Type {
	var ident *ast.Ident
	switch expr := call.Fun.(type) {
	case *ast.Ident:
		ident = expr
	case *ast.SelectorExpr:
		ident = expr.Sel
	default:
		return nil
	}

	// Type conversion (e.g. "float64(foo)").
	if fun, _ := info.ObjectOf(ident).(*types.TypeName); fun != nil {
		return fun.Type()
	}

	return nil
}

func formatParams(tup *types.Tuple, variadic bool, qf types.Qualifier) []string {
	params := make([]string, 0, tup.Len())
	for i := 0; i < tup.Len(); i++ {
		el := tup.At(i)
		typ := types.TypeString(el.Type(), qf)

		// Handle a variadic parameter (can only be the final parameter).
		if variadic && i == tup.Len()-1 {
			typ = strings.Replace(typ, "[]", "...", 1)
		}

		if el.Name() == "" {
			params = append(params, typ)
		} else {
			params = append(params, el.Name()+" "+typ)
		}
	}
	return params
}

func formatResults(tup *types.Tuple, qf types.Qualifier) ([]string, bool) {
	var writeResultParens bool
	results := make([]string, 0, tup.Len())
	for i := 0; i < tup.Len(); i++ {
		if i >= 1 {
			writeResultParens = true
		}
		el := tup.At(i)
		typ := types.TypeString(el.Type(), qf)

		if el.Name() == "" {
			results = append(results, typ)
		} else {
			if i == 0 {
				writeResultParens = true
			}
			results = append(results, el.Name()+" "+typ)
		}
	}
	return results, writeResultParens
}

// formatType returns the detail and kind for an object of type *types.TypeName.
func formatType(typ types.Type, qf types.Qualifier) (detail string, kind CompletionItemKind) {
	if types.IsInterface(typ) {
		detail = "interface{...}"
		kind = InterfaceCompletionItem
	} else if _, ok := typ.(*types.Struct); ok {
		detail = "struct{...}"
		kind = StructCompletionItem
	} else if typ != typ.Underlying() {
		detail, kind = formatType(typ.Underlying(), qf)
	} else {
		detail = types.TypeString(typ, qf)
		kind = TypeCompletionItem
	}
	return detail, kind
}

func formatFunction(params []string, results []string, writeResultParens bool) string {
	var detail strings.Builder

	detail.WriteByte('(')
	for i, p := range params {
		if i > 0 {
			detail.WriteString(", ")
		}
		detail.WriteString(p)
	}
	detail.WriteByte(')')

	// Add space between parameters and results.
	if len(results) > 0 {
		detail.WriteByte(' ')
	}

	if writeResultParens {
		detail.WriteByte('(')
	}
	for i, p := range results {
		if i > 0 {
			detail.WriteString(", ")
		}
		detail.WriteString(p)
	}
	if writeResultParens {
		detail.WriteByte(')')
	}

	return detail.String()
}
