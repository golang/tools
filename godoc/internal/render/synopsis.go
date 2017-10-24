// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package render

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"strings"
)

// oneLineNodeDepth returns a one-line summary of the given input node.
// The depth specifies the maximum depth when traversing the AST.
func oneLineNodeDepth(fset *token.FileSet, node ast.Node, depth int) string {
	const dotDotDot = "..."
	if depth == 0 {
		return dotDotDot
	}
	depth--

	switch n := node.(type) {
	case nil:
		return ""

	case *ast.GenDecl:
		trailer := ""
		if len(n.Specs) > 1 {
			trailer = " " + dotDotDot
		}

		switch n.Tok {
		case token.CONST, token.VAR:
			typ := ""
			for i, spec := range n.Specs {
				valueSpec := spec.(*ast.ValueSpec) // must succeed; we can't mix types in one GenDecl.
				if len(valueSpec.Names) > 1 || len(valueSpec.Values) > 1 {
					trailer = " " + dotDotDot
				}

				// The type name may carry over from a previous specification in the
				// case of constants and iota.
				if valueSpec.Type != nil {
					typ = fmt.Sprintf(" %s", oneLineNodeDepth(fset, valueSpec.Type, depth))
				} else if len(valueSpec.Values) > 0 {
					typ = ""
				}

				val := ""
				if i < len(valueSpec.Values) && valueSpec.Values[i] != nil {
					val = fmt.Sprintf(" = %s", oneLineNodeDepth(fset, valueSpec.Values[i], depth))
				}
				return fmt.Sprintf("%s %s%s%s%s", n.Tok, valueSpec.Names[0], typ, val, trailer)
			}
		case token.TYPE:
			if len(n.Specs) > 0 {
				return oneLineNodeDepth(fset, n.Specs[0], depth) + trailer
			}
		case token.IMPORT:
			if len(n.Specs) > 0 {
				pkg := n.Specs[0].(*ast.ImportSpec).Path.Value
				return fmt.Sprintf("%s %s%s", n.Tok, pkg, trailer)
			}
		}
		return fmt.Sprintf("%s ()", n.Tok)

	case *ast.FuncDecl:
		// Formats func declarations.
		name := n.Name.Name
		recv := oneLineNodeDepth(fset, n.Recv, depth)
		if len(recv) > 0 {
			recv = "(" + recv + ") "
		}
		fnc := oneLineNodeDepth(fset, n.Type, depth)
		if strings.Index(fnc, "func") == 0 {
			fnc = fnc[4:]
		}
		return fmt.Sprintf("func %s%s%s", recv, name, fnc)

	case *ast.TypeSpec:
		sep := " "
		if n.Assign.IsValid() {
			sep = " = "
		}
		return fmt.Sprintf("type %s%s%s", n.Name.Name, sep, oneLineNodeDepth(fset, n.Type, depth))

	case *ast.FuncType:
		var params []string
		if n.Params != nil {
			for _, field := range n.Params.List {
				params = append(params, oneLineField(fset, field, depth))
			}
		}
		needParens := false
		var results []string
		if n.Results != nil {
			needParens = needParens || len(n.Results.List) > 1
			for _, field := range n.Results.List {
				needParens = needParens || len(field.Names) > 0
				results = append(results, oneLineField(fset, field, depth))
			}
		}

		param := joinStrings(params)
		if len(results) == 0 {
			return fmt.Sprintf("func(%s)", param)
		}
		result := joinStrings(results)
		if !needParens {
			return fmt.Sprintf("func(%s) %s", param, result)
		}
		return fmt.Sprintf("func(%s) (%s)", param, result)

	case *ast.StructType:
		if n.Fields == nil || len(n.Fields.List) == 0 {
			return "struct{}"
		}
		return "struct{ ... }"

	case *ast.InterfaceType:
		if n.Methods == nil || len(n.Methods.List) == 0 {
			return "interface{}"
		}
		return "interface{ ... }"

	case *ast.FieldList:
		if n == nil || len(n.List) == 0 {
			return ""
		}
		if len(n.List) == 1 {
			return oneLineField(fset, n.List[0], depth)
		}
		return dotDotDot

	case *ast.FuncLit:
		return oneLineNodeDepth(fset, n.Type, depth) + " { ... }"

	case *ast.CompositeLit:
		typ := oneLineNodeDepth(fset, n.Type, depth)
		if len(n.Elts) == 0 {
			return fmt.Sprintf("%s{}", typ)
		}
		return fmt.Sprintf("%s{ %s }", typ, dotDotDot)

	case *ast.ArrayType:
		length := oneLineNodeDepth(fset, n.Len, depth)
		element := oneLineNodeDepth(fset, n.Elt, depth)
		return fmt.Sprintf("[%s]%s", length, element)

	case *ast.MapType:
		key := oneLineNodeDepth(fset, n.Key, depth)
		value := oneLineNodeDepth(fset, n.Value, depth)
		return fmt.Sprintf("map[%s]%s", key, value)

	case *ast.CallExpr:
		fnc := oneLineNodeDepth(fset, n.Fun, depth)
		var args []string
		for _, arg := range n.Args {
			args = append(args, oneLineNodeDepth(fset, arg, depth))
		}
		return fmt.Sprintf("%s(%s)", fnc, joinStrings(args))

	case *ast.UnaryExpr:
		return fmt.Sprintf("%s%s", n.Op, oneLineNodeDepth(fset, n.X, depth))

	case *ast.Ident:
		return n.Name

	default:
		// As a fallback, use default formatter for all unknown node types.
		buf := new(bytes.Buffer)
		format.Node(buf, fset, node)
		s := buf.String()
		if strings.Contains(s, "\n") {
			return dotDotDot
		}
		return s
	}
}

// oneLineField returns a one-line summary of the field.
func oneLineField(fset *token.FileSet, field *ast.Field, depth int) string {
	var names []string
	for _, name := range field.Names {
		names = append(names, name.Name)
	}
	if len(names) == 0 {
		return oneLineNodeDepth(fset, field.Type, depth)
	}
	return joinStrings(names) + " " + oneLineNodeDepth(fset, field.Type, depth)
}

// joinStrings formats the input as a comma-separated list,
// but truncates the list at some reasonable length if necessary.
func joinStrings(ss []string) string {
	const widthLimit = 80
	var n int
	for i, s := range ss {
		n += len(s) + len(", ")
		if n > widthLimit {
			ss = append(ss[:i:i], "...")
			break
		}
	}
	return strings.Join(ss, ", ")
}
