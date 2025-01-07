// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// The fmtstr command parses the format strings of calls to selected
// printf-like functions in the specified source file, and prints the
// formatting operations and their operands.
//
// It is intended only for debugging and is not a supported interface.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log"
	"strconv"
	"strings"

	"golang.org/x/tools/internal/fmtstr"
)

func main() {
	log.SetPrefix("fmtstr: ")
	log.SetFlags(0)
	flag.Parse()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, flag.Args()[0], nil, 0)
	if err != nil {
		log.Fatal(err)
	}

	functions := map[string]int{
		"fmt.Errorf":  0,
		"fmt.Fprintf": 1,
		"fmt.Printf":  0,
		"fmt.Sprintf": 0,
		"log.Printf":  0,
	}

	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && !call.Ellipsis.IsValid() {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && is[*ast.Ident](sel.X) {
				name := sel.X.(*ast.Ident).Name + "." + sel.Sel.Name // e.g. "fmt.Printf"
				if fmtstrIndex, ok := functions[name]; ok &&
					len(call.Args) > fmtstrIndex {
					// Is it a string literal?
					if fmtstrArg, ok := call.Args[fmtstrIndex].(*ast.BasicLit); ok &&
						fmtstrArg.Kind == token.STRING {
						// Have fmt.Printf("format", ...)
						format, _ := strconv.Unquote(fmtstrArg.Value)

						ops, err := fmtstr.Parse(format, 0)
						if err != nil {
							log.Printf("%s: %v", fset.Position(fmtstrArg.Pos()), err)
							return true
						}

						fmt.Printf("%s: %s(%s, ...)\n",
							fset.Position(fmtstrArg.Pos()),
							name,
							fmtstrArg.Value)
						for _, op := range ops {
							// TODO(adonovan): show more detail.
							fmt.Printf("\t%q\t%v\n",
								op.Text,
								formatNode(fset, call.Args[op.Verb.ArgIndex]))
						}
					}
				}
			}
		}
		return true
	})
}

func is[T any](x any) bool {
	_, ok := x.(T)
	return ok
}

func formatNode(fset *token.FileSet, n ast.Node) string {
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return "<error>"
	}
	return buf.String()
}
