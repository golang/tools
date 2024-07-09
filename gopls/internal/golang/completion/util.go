// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/typeparams"
)

// exprAtPos returns the index of the expression containing pos.
func exprAtPos(pos token.Pos, args []ast.Expr) int {
	for i, expr := range args {
		if expr.Pos() <= pos && pos <= expr.End() {
			return i
		}
	}
	return len(args)
}

// eachField invokes fn for each field that can be selected from a
// value of type T.
func eachField(T types.Type, fn func(*types.Var)) {
	// TODO(adonovan): this algorithm doesn't exclude ambiguous
	// selections that match more than one field/method.
	// types.NewSelectionSet should do that for us.

	// for termination on recursive types
	var seen typeutil.Map

	var visit func(T types.Type)
	visit = func(T types.Type) {
		// T may be a Struct, optionally Named, with an optional
		// Pointer (with optional Aliases at every step!):
		// Consider: type T *struct{ f int }; _ = T(nil).f
		if T, ok := typeparams.Deref(T).Underlying().(*types.Struct); ok {
			if seen.At(T) != nil {
				return
			}

			for i := 0; i < T.NumFields(); i++ {
				f := T.Field(i)
				fn(f)
				if f.Anonymous() {
					seen.Set(T, true)
					visit(f.Type())
				}
			}
		}
	}
	visit(T)
}

// typeIsValid reports whether typ doesn't contain any Invalid types.
func typeIsValid(typ types.Type) bool {
	// Check named types separately, because we don't want
	// to call Underlying() on them to avoid problems with recursive types.
	if _, ok := aliases.Unalias(typ).(*types.Named); ok {
		return true
	}

	switch typ := typ.Underlying().(type) {
	case *types.Basic:
		return typ.Kind() != types.Invalid
	case *types.Array:
		return typeIsValid(typ.Elem())
	case *types.Slice:
		return typeIsValid(typ.Elem())
	case *types.Pointer:
		return typeIsValid(typ.Elem())
	case *types.Map:
		return typeIsValid(typ.Key()) && typeIsValid(typ.Elem())
	case *types.Chan:
		return typeIsValid(typ.Elem())
	case *types.Signature:
		return typeIsValid(typ.Params()) && typeIsValid(typ.Results())
	case *types.Tuple:
		for i := 0; i < typ.Len(); i++ {
			if !typeIsValid(typ.At(i).Type()) {
				return false
			}
		}
		return true
	case *types.Struct, *types.Interface:
		// Don't bother checking structs, interfaces for validity.
		return true
	default:
		return false
	}
}

// resolveInvalid traverses the node of the AST that defines the scope
// containing the declaration of obj, and attempts to find a user-friendly
// name for its invalid type. The resulting Object and its Type are fake.
func resolveInvalid(fset *token.FileSet, obj types.Object, node ast.Node, info *types.Info) types.Object {
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
		default:
			return true
		}
	})
	// Construct a fake type for the object and return a fake object with this type.
	typename := golang.FormatNode(fset, resultExpr)
	typ := types.NewNamed(types.NewTypeName(token.NoPos, obj.Pkg(), typename, nil), types.Typ[types.Invalid], nil)
	return types.NewVar(obj.Pos(), obj.Pkg(), obj.Name(), typ)
}

// TODO(adonovan): inline these.
func isVar(obj types.Object) bool      { return is[*types.Var](obj) }
func isTypeName(obj types.Object) bool { return is[*types.TypeName](obj) }
func isFunc(obj types.Object) bool     { return is[*types.Func](obj) }
func isPkgName(obj types.Object) bool  { return is[*types.PkgName](obj) }

// isPointer reports whether T is a Pointer, or an alias of one.
// It returns false for a Named type whose Underlying is a Pointer.
//
// TODO(adonovan): shouldn't this use CoreType(T)?
func isPointer(T types.Type) bool { return is[*types.Pointer](aliases.Unalias(T)) }

// isEmptyInterface whether T is a (possibly Named or Alias) empty interface
// type, such that every type is assignable to T.
//
// isEmptyInterface returns false for type parameters, since they have
// different assignability rules.
func isEmptyInterface(T types.Type) bool {
	if _, ok := T.(*types.TypeParam); ok {
		return false
	}
	intf, _ := T.Underlying().(*types.Interface)
	return intf != nil && intf.Empty()
}

func isUntyped(T types.Type) bool {
	if basic, ok := aliases.Unalias(T).(*types.Basic); ok {
		return basic.Info()&types.IsUntyped > 0
	}
	return false
}

func deslice(T types.Type) types.Type {
	if slice, ok := T.Underlying().(*types.Slice); ok {
		return slice.Elem()
	}
	return nil
}

// isSelector returns the enclosing *ast.SelectorExpr when pos is in the
// selector.
func enclosingSelector(path []ast.Node, pos token.Pos) *ast.SelectorExpr {
	if len(path) == 0 {
		return nil
	}

	if sel, ok := path[0].(*ast.SelectorExpr); ok {
		return sel
	}

	// TODO(adonovan): consider ast.ParenExpr (e.g. (x).name)
	if _, ok := path[0].(*ast.Ident); ok && len(path) > 1 {
		if sel, ok := path[1].(*ast.SelectorExpr); ok && pos >= sel.Sel.Pos() {
			return sel
		}
	}

	return nil
}

// enclosingDeclLHS returns LHS idents from containing value spec or
// assign statement.
func enclosingDeclLHS(path []ast.Node) []*ast.Ident {
	for _, n := range path {
		switch n := n.(type) {
		case *ast.ValueSpec:
			return n.Names
		case *ast.AssignStmt:
			ids := make([]*ast.Ident, 0, len(n.Lhs))
			for _, e := range n.Lhs {
				if id, ok := e.(*ast.Ident); ok {
					ids = append(ids, id)
				}
			}
			return ids
		}
	}

	return nil
}

// exprObj returns the types.Object associated with the *ast.Ident or
// *ast.SelectorExpr e.
func exprObj(info *types.Info, e ast.Expr) types.Object {
	var ident *ast.Ident
	switch expr := e.(type) {
	case *ast.Ident:
		ident = expr
	case *ast.SelectorExpr:
		ident = expr.Sel
	default:
		return nil
	}

	return info.ObjectOf(ident)
}

// typeConversion returns the type being converted to if call is a type
// conversion expression.
func typeConversion(call *ast.CallExpr, info *types.Info) types.Type {
	// Type conversion (e.g. "float64(foo)").
	if fun, _ := exprObj(info, call.Fun).(*types.TypeName); fun != nil {
		return fun.Type()
	}

	return nil
}

// fieldsAccessible returns whether s has at least one field accessible by p.
func fieldsAccessible(s *types.Struct, p *types.Package) bool {
	for i := 0; i < s.NumFields(); i++ {
		f := s.Field(i)
		if f.Exported() || f.Pkg() == p {
			return true
		}
	}
	return false
}

// prevStmt returns the statement that precedes the statement containing pos.
// For example:
//
//	foo := 1
//	bar(1 + 2<>)
//
// If "<>" is pos, prevStmt returns "foo := 1"
func prevStmt(pos token.Pos, path []ast.Node) ast.Stmt {
	var blockLines []ast.Stmt
	for i := 0; i < len(path) && blockLines == nil; i++ {
		switch n := path[i].(type) {
		case *ast.BlockStmt:
			blockLines = n.List
		case *ast.CommClause:
			blockLines = n.Body
		case *ast.CaseClause:
			blockLines = n.Body
		}
	}

	for i := len(blockLines) - 1; i >= 0; i-- {
		if blockLines[i].End() < pos {
			return blockLines[i]
		}
	}

	return nil
}

// formatZeroValue produces Go code representing the zero value of T. It
// returns the empty string if T is invalid.
func formatZeroValue(T types.Type, qf types.Qualifier) string {
	switch u := T.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Info()&types.IsNumeric > 0:
			return "0"
		case u.Info()&types.IsString > 0:
			return `""`
		case u.Info()&types.IsBoolean > 0:
			return "false"
		default:
			return ""
		}
	case *types.Pointer, *types.Interface, *types.Chan, *types.Map, *types.Slice, *types.Signature:
		return "nil"
	default:
		return types.TypeString(T, qf) + "{}"
	}
}

// isBasicKind returns whether t is a basic type of kind k.
func isBasicKind(t types.Type, k types.BasicInfo) bool {
	b, _ := t.Underlying().(*types.Basic)
	return b != nil && b.Info()&k > 0
}

func (c *completer) editText(from, to token.Pos, newText string) ([]protocol.TextEdit, error) {
	start, end, err := safetoken.Offsets(c.tokFile, from, to)
	if err != nil {
		return nil, err // can't happen: from/to came from c
	}
	return protocol.EditsFromDiffEdits(c.mapper, []diff.Edit{{
		Start: start,
		End:   end,
		New:   newText,
	}})
}

// assignableTo is like types.AssignableTo, but returns false if
// either type is invalid.
func assignableTo(x, to types.Type) bool {
	if aliases.Unalias(x) == types.Typ[types.Invalid] ||
		aliases.Unalias(to) == types.Typ[types.Invalid] {
		return false
	}

	return types.AssignableTo(x, to)
}

// convertibleTo is like types.ConvertibleTo, but returns false if
// either type is invalid.
func convertibleTo(x, to types.Type) bool {
	if aliases.Unalias(x) == types.Typ[types.Invalid] ||
		aliases.Unalias(to) == types.Typ[types.Invalid] {
		return false
	}

	return types.ConvertibleTo(x, to)
}
