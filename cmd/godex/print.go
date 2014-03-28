// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"

	"code.google.com/p/go.tools/go/types"
)

// TODO(gri) use tabwriter for alignment?
// TODO(gri) should print intuitive method sets

func print(w io.Writer, pkg *types.Package, filter func(types.Object) bool) {
	var p printer
	p.pkg = pkg
	p.printPackage(pkg, filter)
	io.Copy(w, &p.buf)
}

type printer struct {
	pkg    *types.Package
	buf    bytes.Buffer
	indent int  // current indentation level
	last   byte // last byte written
}

func (p *printer) print(s string) {
	// Write the string one byte at a time. We care about the presence of
	// newlines for indentation which we will see even in the presence of
	// (non-corrupted) Unicode; no need to read one rune at a time.
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch != '\n' && p.last == '\n' {
			// Note: This could lead to a range overflow for very large
			// indentations, but it's extremely unlikely to happen for
			// non-pathological code.
			p.buf.WriteString("\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t"[:p.indent])
		}
		p.buf.WriteByte(ch)
		p.last = ch
	}
}

func (p *printer) printf(format string, args ...interface{}) {
	p.print(fmt.Sprintf(format, args...))
}

func (p *printer) printPackage(pkg *types.Package, filter func(types.Object) bool) {
	// collect objects by kind
	var (
		consts   []*types.Const
		typez    []*types.TypeName // types without methods
		typem    []*types.Named    // types with methods
		vars     []*types.Var
		funcs    []*types.Func
		builtins []*types.Builtin
	)
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if obj.Exported() {
			// collect top-level exported and possibly filtered objects
			if filter == nil || filter(obj) {
				switch obj := obj.(type) {
				case *types.Const:
					consts = append(consts, obj)
				case *types.TypeName:
					// group into types with methods and types without
					// (for now this is only considering explicitly declared - not "inherited" methods)
					if named, _ := obj.Type().(*types.Named); named != nil && named.NumMethods() > 0 {
						typem = append(typem, named)
					} else {
						typez = append(typez, obj)
					}
				case *types.Var:
					vars = append(vars, obj)
				case *types.Func:
					funcs = append(funcs, obj)
				case *types.Builtin:
					// for unsafe.Sizeof, etc.
					builtins = append(builtins, obj)
				}
			}
		} else if filter == nil {
			// no filtering: collect top-level unexported types with methods
			if obj, _ := obj.(*types.TypeName); obj != nil {
				// see case *types.TypeName above
				if named, _ := obj.Type().(*types.Named); named != nil && named.NumMethods() > 0 {
					typem = append(typem, named)
				}
			}
		}
	}

	p.printf("package %s  // %q\n", pkg.Name(), pkg.Path())

	p.printDecl("const", len(consts), func() {
		for _, obj := range consts {
			p.printObj(obj)
			p.print("\n")
		}
	})

	p.printDecl("var", len(vars), func() {
		for _, obj := range vars {
			p.printObj(obj)
			p.print("\n")
		}
	})

	p.printDecl("type", len(typez), func() {
		for _, obj := range typez {
			p.printf("%s ", obj.Name())
			p.writeType(p.pkg, obj.Type().Underlying())
			p.print("\n")
		}
	})

	for _, typ := range typem {
		first := true
		if obj := typ.Obj(); obj.Exported() {
			if first {
				p.print("\n")
				first = false
			}
			p.printf("type %s ", obj.Name())
			p.writeType(p.pkg, typ.Underlying())
			p.print("\n")
		}
		for i, n := 0, typ.NumMethods(); i < n; i++ {
			if obj := typ.Method(i); obj.Exported() {
				if first {
					p.print("\n")
					first = false
				}
				p.printFunc(obj)
				p.print("\n")
			}
		}
	}

	if len(funcs) > 0 {
		p.print("\n")
		for _, obj := range funcs {
			p.printFunc(obj)
			p.print("\n")
		}
	}

	// TODO(gri) better handling of builtins (package unsafe only)
	if len(builtins) > 0 {
		p.print("\n")
		for _, obj := range builtins {
			p.printf("func %s() // builtin\n", obj.Name())
		}
	}

	p.print("\n")
}

func (p *printer) printDecl(keyword string, n int, printGroup func()) {
	switch n {
	case 0:
		// nothing to do
	case 1:
		p.printf("\n%s ", keyword)
		printGroup()
	default:
		p.printf("\n%s (\n", keyword)
		p.indent++
		printGroup()
		p.indent--
		p.print(")\n")
	}
}

func (p *printer) printObj(obj types.Object) {
	p.printf("%s", obj.Name())
	// don't write untyped types (for constants)
	if typ := obj.Type(); typed(typ) {
		p.print(" ")
		p.writeType(p.pkg, typ)
	}
	// write constant value
	// TODO(gri) use floating-point notation for exact floating-point numbers (fractions)
	if obj, ok := obj.(*types.Const); ok {
		p.printf(" = %s", obj.Val())
	}
}

func (p *printer) printFunc(obj *types.Func) {
	p.print("func ")
	sig := obj.Type().(*types.Signature)
	if recv := sig.Recv(); recv != nil {
		p.print("(")
		if name := recv.Name(); name != "" {
			p.print(name)
			p.print(" ")
		}
		p.writeType(p.pkg, recv.Type())
		p.print(") ")
	}
	p.print(obj.Name())
	p.writeSignature(p.pkg, sig)
}

func typed(typ types.Type) bool {
	if t, ok := typ.Underlying().(*types.Basic); ok {
		return t.Info()&types.IsUntyped == 0
	}
	return true
}
