package ssa

// This file defines a number of miscellaneous utility functions.

import (
	"fmt"
	"go/ast"
	"io"
	"os"
	"reflect"

	"code.google.com/p/go.tools/go/types"
)

func unreachable() {
	panic("unreachable")
}

//// AST utilities

// noparens returns e with any enclosing parentheses stripped.
func noparens(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			break
		}
		e = p.X
	}
	return e
}

// isBlankIdent returns true iff e is an Ident with name "_".
// They have no associated types.Object, and thus no type.
//
// TODO(gri): consider making typechecker not treat them differently.
// It's one less thing for clients like us to worry about.
//
func isBlankIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "_"
}

//// Type utilities.  Some of these belong in go/types.

// underlyingType returns the underlying type of typ.
// TODO(gri): this is a copy of go/types.underlying; export that function.
//
func underlyingType(typ types.Type) types.Type {
	if typ, ok := typ.(*types.NamedType); ok {
		return typ.Underlying // underlying types are never NamedTypes
	}
	if typ == nil {
		panic("underlyingType(nil)")
	}
	return typ
}

// isPointer returns true for types whose underlying type is a pointer.
func isPointer(typ types.Type) bool {
	if nt, ok := typ.(*types.NamedType); ok {
		typ = nt.Underlying
	}
	_, ok := typ.(*types.Pointer)
	return ok
}

// pointer(typ) returns the type that is a pointer to typ.
func pointer(typ types.Type) *types.Pointer {
	return &types.Pointer{Base: typ}
}

// indirect(typ) assumes that typ is a pointer type,
// or named alias thereof, and returns its base type.
// Panic ensures if it is not a pointer.
//
func indirectType(ptr types.Type) types.Type {
	if v, ok := underlyingType(ptr).(*types.Pointer); ok {
		return v.Base
	}
	// When debugging it is convenient to comment out this line
	// and let it continue to print the (illegal) SSA form.
	panic("indirect() of non-pointer type: " + ptr.String())
	return nil
}

// deref returns a pointer's base type; otherwise it returns typ.
func deref(typ types.Type) types.Type {
	if typ, ok := underlyingType(typ).(*types.Pointer); ok {
		return typ.Base
	}
	return typ
}

// methodIndex returns the method (and its index) named id within the
// method table methods of named or interface type typ.  If not found,
// panic ensues.
//
func methodIndex(typ types.Type, methods []*types.Method, id Id) (i int, m *types.Method) {
	for i, m = range methods {
		if IdFromQualifiedName(m.QualifiedName) == id {
			return
		}
	}
	panic(fmt.Sprint("method not found: ", id, " in interface ", typ))
}

// isSuperinterface returns true if x is a superinterface of y,
// i.e.  x's methods are a subset of y's.
//
func isSuperinterface(x, y *types.Interface) bool {
	if len(y.Methods) < len(x.Methods) {
		return false
	}
	// TODO(adonovan): opt: this is quadratic.
outer:
	for _, xm := range x.Methods {
		for _, ym := range y.Methods {
			if IdFromQualifiedName(xm.QualifiedName) == IdFromQualifiedName(ym.QualifiedName) {
				if !types.IsIdentical(xm.Type, ym.Type) {
					return false // common name but conflicting types
				}
				continue outer
			}
		}
		return false // y doesn't have this method
	}
	return true
}

// objKind returns the syntactic category of the named entity denoted by obj.
func objKind(obj types.Object) ast.ObjKind {
	switch obj.(type) {
	case *types.Package:
		return ast.Pkg
	case *types.TypeName:
		return ast.Typ
	case *types.Const:
		return ast.Con
	case *types.Var:
		return ast.Var
	case *types.Func:
		return ast.Fun
	}
	panic(fmt.Sprintf("unexpected Object type: %T", obj))
}

// canHaveConcreteMethods returns true iff typ may have concrete
// methods associated with it.  Callers must supply allowPtr=true.
//
// TODO(gri): consider putting this in go/types.  It's surprisingly subtle.
func canHaveConcreteMethods(typ types.Type, allowPtr bool) bool {
	switch typ := typ.(type) {
	case *types.Pointer:
		return allowPtr && canHaveConcreteMethods(typ.Base, false)
	case *types.NamedType:
		switch typ.Underlying.(type) {
		case *types.Pointer, *types.Interface:
			return false
		}
		return true
	case *types.Struct:
		return true
	}
	return false
}

// DefaultType returns the default "typed" type for an "untyped" type;
// it returns the incoming type for all other types. If there is no
// corresponding untyped type, the result is types.Typ[types.Invalid].
//
// Exported to exp/ssa/interp.
//
// TODO(gri): this is a copy of go/types.defaultType; export that function.
//
func DefaultType(typ types.Type) types.Type {
	if t, ok := typ.(*types.Basic); ok {
		k := types.Invalid
		switch t.Kind {
		// case UntypedNil:
		//      There is no default type for nil. For a good error message,
		//      catch this case before calling this function.
		case types.UntypedBool:
			k = types.Bool
		case types.UntypedInt:
			k = types.Int
		case types.UntypedRune:
			k = types.Rune
		case types.UntypedFloat:
			k = types.Float64
		case types.UntypedComplex:
			k = types.Complex128
		case types.UntypedString:
			k = types.String
		}
		typ = types.Typ[k]
	}
	return typ
}

// makeId returns the Id (name, pkg) if the name is exported or
// (name, nil) otherwise.
//
func makeId(name string, pkg *types.Package) (id Id) {
	id.Name = name
	if !ast.IsExported(name) {
		id.Pkg = pkg
		// TODO(gri): fix
		// if pkg.Path == "" {
		// 	panic("Package " + pkg.Name + "has empty Path")
		// }
	}
	return
}

// IdFromQualifiedName returns the Id (qn.Name, qn.Pkg) if qn is an
// exported name or (qn.Name, nil) otherwise.
//
// Exported to exp/ssa/interp.
//
func IdFromQualifiedName(qn types.QualifiedName) Id {
	return makeId(qn.Name, qn.Pkg)
}

type ids []Id // a sortable slice of Id

func (p ids) Len() int { return len(p) }
func (p ids) Less(i, j int) bool {
	x, y := p[i], p[j]
	// *Package pointers are canonical so order by them.
	// Don't use x.Pkg.ImportPath because sometimes it's empty.
	// (TODO(gri): fix that.)
	return reflect.ValueOf(x.Pkg).Pointer() < reflect.ValueOf(y.Pkg).Pointer() ||
		x.Pkg == y.Pkg && x.Name < y.Name
}
func (p ids) Swap(i, j int) { p[i], p[j] = p[j], p[i] }

// logStack prints the formatted "start" message to stderr and
// returns a closure that prints the corresponding "end" message.
// Call using 'defer logStack(...)()' to show builder stack on panic.
// Don't forget trailing parens!
//
func logStack(format string, args ...interface{}) func() {
	msg := fmt.Sprintf(format, args...)
	io.WriteString(os.Stderr, msg)
	io.WriteString(os.Stderr, "\n")
	return func() {
		io.WriteString(os.Stderr, msg)
		io.WriteString(os.Stderr, " end\n")
	}
}
