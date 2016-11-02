// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build go1.8

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	exact "go/constant"
	"go/token"
	"go/types"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/tools/cmd/guru/serial"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/types/typeutil"
)

// describe describes the syntax node denoted by the query position,
// including:
// - its syntactic category
// - the definition of its referent (for identifiers) [now redundant]
// - its type, fields, and methods (for an expression or type expression)
//
func describe(q *Query) error {
	lconf := loader.Config{Build: q.Build}
	allowErrors(&lconf)

	if _, err := importQueryPackage(q.Pos, &lconf); err != nil {
		return err
	}

	// Load/parse/type-check the program.
	lprog, err := lconf.Load()
	if err != nil {
		return err
	}

	qpos, err := parseQueryPos(lprog, q.Pos, true) // (need exact pos)
	if err != nil {
		return err
	}

	if false { // debugging
		fprintf(os.Stderr, lprog.Fset, qpos.path[0], "you selected: %s %s",
			astutil.NodeDescription(qpos.path[0]), pathToString(qpos.path))
	}

	var qr QueryResult
	path, action := findInterestingNode(qpos.info, qpos.path)
	switch action {
	case actionExpr:
		qr, err = describeValue(qpos, path)

	case actionType:
		qr, err = describeType(qpos, path)

	case actionPackage:
		qr, err = describePackage(qpos, path)

	case actionStmt:
		qr, err = describeStmt(qpos, path)

	case actionUnknown:
		qr = &describeUnknownResult{path[0]}

	default:
		panic(action) // unreachable
	}
	if err != nil {
		return err
	}
	q.Output(lprog.Fset, qr)
	return nil
}

type describeUnknownResult struct {
	node ast.Node
}

func (r *describeUnknownResult) PrintPlain(printf printfFunc) {
	// Nothing much to say about misc syntax.
	printf(r.node, "%s", astutil.NodeDescription(r.node))
}

func (r *describeUnknownResult) JSON(fset *token.FileSet) []byte {
	return toJSON(&serial.Describe{
		Desc: astutil.NodeDescription(r.node),
		Pos:  fset.Position(r.node.Pos()).String(),
	})
}

type action int

const (
	actionUnknown action = iota // None of the below
	actionExpr                  // FuncDecl, true Expr or Ident(types.{Const,Var})
	actionType                  // type Expr or Ident(types.TypeName).
	actionStmt                  // Stmt or Ident(types.Label)
	actionPackage               // Ident(types.Package) or ImportSpec
)

// findInterestingNode classifies the syntax node denoted by path as one of:
//    - an expression, part of an expression or a reference to a constant
//      or variable;
//    - a type, part of a type, or a reference to a named type;
//    - a statement, part of a statement, or a label referring to a statement;
//    - part of a package declaration or import spec.
//    - none of the above.
// and returns the most "interesting" associated node, which may be
// the same node, an ancestor or a descendent.
//
func findInterestingNode(pkginfo *loader.PackageInfo, path []ast.Node) ([]ast.Node, action) {
	// TODO(adonovan): integrate with go/types/stdlib_test.go and
	// apply this to every AST node we can find to make sure it
	// doesn't crash.

	// TODO(adonovan): audit for ParenExpr safety, esp. since we
	// traverse up and down.

	// TODO(adonovan): if the users selects the "." in
	// "fmt.Fprintf()", they'll get an ambiguous selection error;
	// we won't even reach here.  Can we do better?

	// TODO(adonovan): describing a field within 'type T struct {...}'
	// describes the (anonymous) struct type and concludes "no methods".
	// We should ascend to the enclosing type decl, if any.

	for len(path) > 0 {
		switch n := path[0].(type) {
		case *ast.GenDecl:
			if len(n.Specs) == 1 {
				// Descend to sole {Import,Type,Value}Spec child.
				path = append([]ast.Node{n.Specs[0]}, path...)
				continue
			}
			return path, actionUnknown // uninteresting

		case *ast.FuncDecl:
			// Descend to function name.
			path = append([]ast.Node{n.Name}, path...)
			continue

		case *ast.ImportSpec:
			return path, actionPackage

		case *ast.ValueSpec:
			if len(n.Names) == 1 {
				// Descend to sole Ident child.
				path = append([]ast.Node{n.Names[0]}, path...)
				continue
			}
			return path, actionUnknown // uninteresting

		case *ast.TypeSpec:
			// Descend to type name.
			path = append([]ast.Node{n.Name}, path...)
			continue

		case ast.Stmt:
			return path, actionStmt

		case *ast.ArrayType,
			*ast.StructType,
			*ast.FuncType,
			*ast.InterfaceType,
			*ast.MapType,
			*ast.ChanType:
			return path, actionType

		case *ast.Comment, *ast.CommentGroup, *ast.File, *ast.KeyValueExpr, *ast.CommClause:
			return path, actionUnknown // uninteresting

		case *ast.Ellipsis:
			// Continue to enclosing node.
			// e.g. [...]T in ArrayType
			//      f(x...) in CallExpr
			//      f(x...T) in FuncType

		case *ast.Field:
			// TODO(adonovan): this needs more thought,
			// since fields can be so many things.
			if len(n.Names) == 1 {
				// Descend to sole Ident child.
				path = append([]ast.Node{n.Names[0]}, path...)
				continue
			}
			// Zero names (e.g. anon field in struct)
			// or multiple field or param names:
			// continue to enclosing field list.

		case *ast.FieldList:
			// Continue to enclosing node:
			// {Struct,Func,Interface}Type or FuncDecl.

		case *ast.BasicLit:
			if _, ok := path[1].(*ast.ImportSpec); ok {
				return path[1:], actionPackage
			}
			return path, actionExpr

		case *ast.SelectorExpr:
			// TODO(adonovan): use Selections info directly.
			if pkginfo.Uses[n.Sel] == nil {
				// TODO(adonovan): is this reachable?
				return path, actionUnknown
			}
			// Descend to .Sel child.
			path = append([]ast.Node{n.Sel}, path...)
			continue

		case *ast.Ident:
			switch pkginfo.ObjectOf(n).(type) {
			case *types.PkgName:
				return path, actionPackage

			case *types.Const:
				return path, actionExpr

			case *types.Label:
				return path, actionStmt

			case *types.TypeName:
				return path, actionType

			case *types.Var:
				// For x in 'struct {x T}', return struct type, for now.
				if _, ok := path[1].(*ast.Field); ok {
					_ = path[2].(*ast.FieldList) // assertion
					if _, ok := path[3].(*ast.StructType); ok {
						return path[3:], actionType
					}
				}
				return path, actionExpr

			case *types.Func:
				return path, actionExpr

			case *types.Builtin:
				// For reference to built-in function, return enclosing call.
				path = path[1:] // ascend to enclosing function call
				continue

			case *types.Nil:
				return path, actionExpr
			}

			// No object.
			switch path[1].(type) {
			case *ast.SelectorExpr:
				// Return enclosing selector expression.
				return path[1:], actionExpr

			case *ast.Field:
				// TODO(adonovan): test this.
				// e.g. all f in:
				//  struct { f, g int }
				//  interface { f() }
				//  func (f T) method(f, g int) (f, g bool)
				//
				// switch path[3].(type) {
				// case *ast.FuncDecl:
				// case *ast.StructType:
				// case *ast.InterfaceType:
				// }
				//
				// return path[1:], actionExpr
				//
				// Unclear what to do with these.
				// Struct.Fields             -- field
				// Interface.Methods         -- field
				// FuncType.{Params.Results} -- actionExpr
				// FuncDecl.Recv             -- actionExpr

			case *ast.File:
				// 'package foo'
				return path, actionPackage

			case *ast.ImportSpec:
				return path[1:], actionPackage

			default:
				// e.g. blank identifier
				// or y in "switch y := x.(type)"
				// or code in a _test.go file that's not part of the package.
				return path, actionUnknown
			}

		case *ast.StarExpr:
			if pkginfo.Types[n].IsType() {
				return path, actionType
			}
			return path, actionExpr

		case ast.Expr:
			// All Expr but {BasicLit,Ident,StarExpr} are
			// "true" expressions that evaluate to a value.
			return path, actionExpr
		}

		// Ascend to parent.
		path = path[1:]
	}

	return nil, actionUnknown // unreachable
}

func describeValue(qpos *queryPos, path []ast.Node) (*describeValueResult, error) {
	var expr ast.Expr
	var obj types.Object
	switch n := path[0].(type) {
	case *ast.ValueSpec:
		// ambiguous ValueSpec containing multiple names
		return nil, fmt.Errorf("multiple value specification")
	case *ast.Ident:
		obj = qpos.info.ObjectOf(n)
		expr = n
	case ast.Expr:
		expr = n
	default:
		// TODO(adonovan): is this reachable?
		return nil, fmt.Errorf("unexpected AST for expr: %T", n)
	}

	t := qpos.info.TypeOf(expr)
	if t == nil {
		t = types.Typ[types.Invalid]
	}
	constVal := qpos.info.Types[expr].Value

	return &describeValueResult{
		qpos:     qpos,
		expr:     expr,
		typ:      t,
		constVal: constVal,
		obj:      obj,
		methods:  accessibleMethods(t, qpos.info.Pkg),
		fields:   accessibleFields(t, qpos.info.Pkg),
	}, nil
}

type describeValueResult struct {
	qpos     *queryPos
	expr     ast.Expr     // query node
	typ      types.Type   // type of expression
	constVal exact.Value  // value of expression, if constant
	obj      types.Object // var/func/const object, if expr was Ident
	methods  []*types.Selection
	fields   []describeField
}

func (r *describeValueResult) PrintPlain(printf printfFunc) {
	var prefix, suffix string
	if r.constVal != nil {
		suffix = fmt.Sprintf(" of constant value %s", constValString(r.constVal))
	}
	switch obj := r.obj.(type) {
	case *types.Func:
		if recv := obj.Type().(*types.Signature).Recv(); recv != nil {
			if _, ok := recv.Type().Underlying().(*types.Interface); ok {
				prefix = "interface method "
			} else {
				prefix = "method "
			}
		}
	}

	// Describe the expression.
	if r.obj != nil {
		if r.obj.Pos() == r.expr.Pos() {
			// defining ident
			printf(r.expr, "definition of %s%s%s", prefix, r.qpos.objectString(r.obj), suffix)
		} else {
			// referring ident
			printf(r.expr, "reference to %s%s%s", prefix, r.qpos.objectString(r.obj), suffix)
			if def := r.obj.Pos(); def != token.NoPos {
				printf(def, "defined here")
			}
		}
	} else {
		desc := astutil.NodeDescription(r.expr)
		if suffix != "" {
			// constant expression
			printf(r.expr, "%s%s", desc, suffix)
		} else {
			// non-constant expression
			printf(r.expr, "%s of type %s", desc, r.qpos.typeString(r.typ))
		}
	}

	printMethods(printf, r.expr, r.methods)
	printFields(printf, r.expr, r.fields)
}

func (r *describeValueResult) JSON(fset *token.FileSet) []byte {
	var value, objpos string
	if r.constVal != nil {
		value = r.constVal.String()
	}
	if r.obj != nil {
		objpos = fset.Position(r.obj.Pos()).String()
	}

	return toJSON(&serial.Describe{
		Desc:   astutil.NodeDescription(r.expr),
		Pos:    fset.Position(r.expr.Pos()).String(),
		Detail: "value",
		Value: &serial.DescribeValue{
			Type:   r.qpos.typeString(r.typ),
			Value:  value,
			ObjPos: objpos,
		},
	})
}

// ---- TYPE ------------------------------------------------------------

func describeType(qpos *queryPos, path []ast.Node) (*describeTypeResult, error) {
	var description string
	var t types.Type
	switch n := path[0].(type) {
	case *ast.Ident:
		t = qpos.info.TypeOf(n)
		switch t := t.(type) {
		case *types.Basic:
			description = "reference to built-in "

		case *types.Named:
			isDef := t.Obj().Pos() == n.Pos() // see caveats at isDef above
			if isDef {
				description = "definition of "
			} else {
				description = "reference to "
			}
		}

	case ast.Expr:
		t = qpos.info.TypeOf(n)

	default:
		// Unreachable?
		return nil, fmt.Errorf("unexpected AST for type: %T", n)
	}

	description = description + "type " + qpos.typeString(t)

	// Show sizes for structs and named types (it's fairly obvious for others).
	switch t.(type) {
	case *types.Named, *types.Struct:
		szs := types.StdSizes{WordSize: 8, MaxAlign: 8} // assume amd64
		description = fmt.Sprintf("%s (size %d, align %d)", description,
			szs.Sizeof(t), szs.Alignof(t))
	}

	return &describeTypeResult{
		qpos:        qpos,
		node:        path[0],
		description: description,
		typ:         t,
		methods:     accessibleMethods(t, qpos.info.Pkg),
		fields:      accessibleFields(t, qpos.info.Pkg),
	}, nil
}

type describeTypeResult struct {
	qpos        *queryPos
	node        ast.Node
	description string
	typ         types.Type
	methods     []*types.Selection
	fields      []describeField
}

type describeField struct {
	implicits []*types.Named
	field     *types.Var
}

func printMethods(printf printfFunc, node ast.Node, methods []*types.Selection) {
	if len(methods) > 0 {
		printf(node, "Methods:")
	}
	for _, meth := range methods {
		// Print the method type relative to the package
		// in which it was defined, not the query package,
		printf(meth.Obj(), "\t%s",
			types.SelectionString(meth, types.RelativeTo(meth.Obj().Pkg())))
	}
}

func printFields(printf printfFunc, node ast.Node, fields []describeField) {
	if len(fields) > 0 {
		printf(node, "Fields:")
	}

	// Align the names and the types (requires two passes).
	var width int
	var names []string
	for _, f := range fields {
		var buf bytes.Buffer
		for _, fld := range f.implicits {
			buf.WriteString(fld.Obj().Name())
			buf.WriteByte('.')
		}
		buf.WriteString(f.field.Name())
		name := buf.String()
		if n := utf8.RuneCountInString(name); n > width {
			width = n
		}
		names = append(names, name)
	}

	for i, f := range fields {
		// Print the field type relative to the package
		// in which it was defined, not the query package,
		printf(f.field, "\t%*s %s", -width, names[i],
			types.TypeString(f.field.Type(), types.RelativeTo(f.field.Pkg())))
	}
}

func (r *describeTypeResult) PrintPlain(printf printfFunc) {
	printf(r.node, "%s", r.description)

	// Show the underlying type for a reference to a named type.
	if nt, ok := r.typ.(*types.Named); ok && r.node.Pos() != nt.Obj().Pos() {
		// TODO(adonovan): improve display of complex struct/interface types.
		printf(nt.Obj(), "defined as %s", r.qpos.typeString(nt.Underlying()))
	}

	printMethods(printf, r.node, r.methods)
	if len(r.methods) == 0 {
		// Only report null result for type kinds
		// capable of bearing methods.
		switch r.typ.(type) {
		case *types.Interface, *types.Struct, *types.Named:
			printf(r.node, "No methods.")
		}
	}

	printFields(printf, r.node, r.fields)
}

func (r *describeTypeResult) JSON(fset *token.FileSet) []byte {
	var namePos, nameDef string
	if nt, ok := r.typ.(*types.Named); ok {
		namePos = fset.Position(nt.Obj().Pos()).String()
		nameDef = nt.Underlying().String()
	}
	return toJSON(&serial.Describe{
		Desc:   r.description,
		Pos:    fset.Position(r.node.Pos()).String(),
		Detail: "type",
		Type: &serial.DescribeType{
			Type:    r.qpos.typeString(r.typ),
			NamePos: namePos,
			NameDef: nameDef,
			Methods: methodsToSerial(r.qpos.info.Pkg, r.methods, fset),
		},
	})
}

// ---- PACKAGE ------------------------------------------------------------

func describePackage(qpos *queryPos, path []ast.Node) (*describePackageResult, error) {
	var description string
	var pkg *types.Package
	switch n := path[0].(type) {
	case *ast.ImportSpec:
		var obj types.Object
		if n.Name != nil {
			obj = qpos.info.Defs[n.Name]
		} else {
			obj = qpos.info.Implicits[n]
		}
		pkgname, _ := obj.(*types.PkgName)
		if pkgname == nil {
			return nil, fmt.Errorf("can't import package %s", n.Path.Value)
		}
		pkg = pkgname.Imported()
		description = fmt.Sprintf("import of package %q", pkg.Path())

	case *ast.Ident:
		if _, isDef := path[1].(*ast.File); isDef {
			// e.g. package id
			pkg = qpos.info.Pkg
			description = fmt.Sprintf("definition of package %q", pkg.Path())
		} else {
			// e.g. import id "..."
			//  or  id.F()
			pkg = qpos.info.ObjectOf(n).(*types.PkgName).Imported()
			description = fmt.Sprintf("reference to package %q", pkg.Path())
		}

	default:
		// Unreachable?
		return nil, fmt.Errorf("unexpected AST for package: %T", n)
	}

	var members []*describeMember
	// NB: "unsafe" has no types.Package
	if pkg != nil {
		// Enumerate the accessible package members
		// in lexicographic order.
		for _, name := range pkg.Scope().Names() {
			if pkg == qpos.info.Pkg || ast.IsExported(name) {
				mem := pkg.Scope().Lookup(name)
				var methods []*types.Selection
				if mem, ok := mem.(*types.TypeName); ok {
					methods = accessibleMethods(mem.Type(), qpos.info.Pkg)
				}
				members = append(members, &describeMember{
					mem,
					methods,
				})

			}
		}
	}

	return &describePackageResult{qpos.fset, path[0], description, pkg, members}, nil
}

type describePackageResult struct {
	fset        *token.FileSet
	node        ast.Node
	description string
	pkg         *types.Package
	members     []*describeMember // in lexicographic name order
}

type describeMember struct {
	obj     types.Object
	methods []*types.Selection // in types.MethodSet order
}

func (r *describePackageResult) PrintPlain(printf printfFunc) {
	printf(r.node, "%s", r.description)

	// Compute max width of name "column".
	maxname := 0
	for _, mem := range r.members {
		if l := len(mem.obj.Name()); l > maxname {
			maxname = l
		}
	}

	for _, mem := range r.members {
		printf(mem.obj, "\t%s", formatMember(mem.obj, maxname))
		for _, meth := range mem.methods {
			printf(meth.Obj(), "\t\t%s", types.SelectionString(meth, types.RelativeTo(r.pkg)))
		}
	}
}

// Helper function to adjust go1.5 numeric go/constant formatting.
// Can be removed once we give up compatibility with go1.5.
func constValString(v exact.Value) string {
	if v.Kind() == exact.Float {
		// In go1.5, go/constant floating-point values are printed
		// as fractions. Make them appear as floating-point numbers.
		f, _ := exact.Float64Val(v)
		return fmt.Sprintf("%g", f)
	}
	return v.String()
}

func formatMember(obj types.Object, maxname int) string {
	qualifier := types.RelativeTo(obj.Pkg())
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%-5s %-*s", tokenOf(obj), maxname, obj.Name())
	switch obj := obj.(type) {
	case *types.Const:
		fmt.Fprintf(&buf, " %s = %s", types.TypeString(obj.Type(), qualifier), constValString(obj.Val()))

	case *types.Func:
		fmt.Fprintf(&buf, " %s", types.TypeString(obj.Type(), qualifier))

	case *types.TypeName:
		// Abbreviate long aggregate type names.
		var abbrev string
		switch t := obj.Type().Underlying().(type) {
		case *types.Interface:
			if t.NumMethods() > 1 {
				abbrev = "interface{...}"
			}
		case *types.Struct:
			if t.NumFields() > 1 {
				abbrev = "struct{...}"
			}
		}
		if abbrev == "" {
			fmt.Fprintf(&buf, " %s", types.TypeString(obj.Type().Underlying(), qualifier))
		} else {
			fmt.Fprintf(&buf, " %s", abbrev)
		}

	case *types.Var:
		fmt.Fprintf(&buf, " %s", types.TypeString(obj.Type(), qualifier))
	}
	return buf.String()
}

func (r *describePackageResult) JSON(fset *token.FileSet) []byte {
	var members []*serial.DescribeMember
	for _, mem := range r.members {
		typ := mem.obj.Type()
		var val string
		switch mem := mem.obj.(type) {
		case *types.Const:
			val = constValString(mem.Val())
		case *types.TypeName:
			typ = typ.Underlying()
		}
		members = append(members, &serial.DescribeMember{
			Name:    mem.obj.Name(),
			Type:    typ.String(),
			Value:   val,
			Pos:     fset.Position(mem.obj.Pos()).String(),
			Kind:    tokenOf(mem.obj),
			Methods: methodsToSerial(r.pkg, mem.methods, fset),
		})
	}
	return toJSON(&serial.Describe{
		Desc:   r.description,
		Pos:    fset.Position(r.node.Pos()).String(),
		Detail: "package",
		Package: &serial.DescribePackage{
			Path:    r.pkg.Path(),
			Members: members,
		},
	})
}

func tokenOf(o types.Object) string {
	switch o.(type) {
	case *types.Func:
		return "func"
	case *types.Var:
		return "var"
	case *types.TypeName:
		return "type"
	case *types.Const:
		return "const"
	case *types.PkgName:
		return "package"
	case *types.Builtin:
		return "builtin" // e.g. when describing package "unsafe"
	case *types.Nil:
		return "nil"
	case *types.Label:
		return "label"
	}
	panic(o)
}

// ---- STATEMENT ------------------------------------------------------------

func describeStmt(qpos *queryPos, path []ast.Node) (*describeStmtResult, error) {
	var description string
	switch n := path[0].(type) {
	case *ast.Ident:
		if qpos.info.Defs[n] != nil {
			description = "labelled statement"
		} else {
			description = "reference to labelled statement"
		}

	default:
		// Nothing much to say about statements.
		description = astutil.NodeDescription(n)
	}
	return &describeStmtResult{qpos.fset, path[0], description}, nil
}

type describeStmtResult struct {
	fset        *token.FileSet
	node        ast.Node
	description string
}

func (r *describeStmtResult) PrintPlain(printf printfFunc) {
	printf(r.node, "%s", r.description)
}

func (r *describeStmtResult) JSON(fset *token.FileSet) []byte {
	return toJSON(&serial.Describe{
		Desc:   r.description,
		Pos:    fset.Position(r.node.Pos()).String(),
		Detail: "unknown",
	})
}

// ------------------- Utilities -------------------

// pathToString returns a string containing the concrete types of the
// nodes in path.
func pathToString(path []ast.Node) string {
	var buf bytes.Buffer
	fmt.Fprint(&buf, "[")
	for i, n := range path {
		if i > 0 {
			fmt.Fprint(&buf, " ")
		}
		fmt.Fprint(&buf, strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast."))
	}
	fmt.Fprint(&buf, "]")
	return buf.String()
}

func accessibleMethods(t types.Type, from *types.Package) []*types.Selection {
	var methods []*types.Selection
	for _, meth := range typeutil.IntuitiveMethodSet(t, nil) {
		if isAccessibleFrom(meth.Obj(), from) {
			methods = append(methods, meth)
		}
	}
	return methods
}

// accessibleFields returns the set of accessible
// field selections on a value of type recv.
func accessibleFields(recv types.Type, from *types.Package) []describeField {
	wantField := func(f *types.Var) bool {
		if !isAccessibleFrom(f, from) {
			return false
		}
		// Check that the field is not shadowed.
		obj, _, _ := types.LookupFieldOrMethod(recv, true, f.Pkg(), f.Name())
		return obj == f
	}

	var fields []describeField
	var visit func(t types.Type, stack []*types.Named)
	visit = func(t types.Type, stack []*types.Named) {
		tStruct, ok := deref(t).Underlying().(*types.Struct)
		if !ok {
			return
		}
	fieldloop:
		for i := 0; i < tStruct.NumFields(); i++ {
			f := tStruct.Field(i)

			// Handle recursion through anonymous fields.
			if f.Anonymous() {
				tf := f.Type()
				if ptr, ok := tf.(*types.Pointer); ok {
					tf = ptr.Elem()
				}
				if named, ok := tf.(*types.Named); ok { // (be defensive)
					// If we've already visited this named type
					// on this path, break the cycle.
					for _, x := range stack {
						if x == named {
							continue fieldloop
						}
					}
					visit(f.Type(), append(stack, named))
				}
			}

			// Save accessible fields.
			if wantField(f) {
				fields = append(fields, describeField{
					implicits: append([]*types.Named(nil), stack...),
					field:     f,
				})
			}
		}
	}
	visit(recv, nil)

	return fields
}

func isAccessibleFrom(obj types.Object, pkg *types.Package) bool {
	return ast.IsExported(obj.Name()) || obj.Pkg() == pkg
}

func methodsToSerial(this *types.Package, methods []*types.Selection, fset *token.FileSet) []serial.DescribeMethod {
	qualifier := types.RelativeTo(this)
	var jmethods []serial.DescribeMethod
	for _, meth := range methods {
		var ser serial.DescribeMethod
		if meth != nil { // may contain nils when called by implements (on a method)
			ser = serial.DescribeMethod{
				Name: types.SelectionString(meth, qualifier),
				Pos:  fset.Position(meth.Obj().Pos()).String(),
			}
		}
		jmethods = append(jmethods, ser)
	}
	return jmethods
}
