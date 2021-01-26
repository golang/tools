// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package composite defines an Analyzer that checks for unkeyed
// composite literals.
package composite

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const Doc = `check for unkeyed composite literals

This analyzer reports a diagnostic for composite literals of struct
types imported from another package that do not use the field-keyed
syntax. Such literals are fragile because the addition of a new field
(even if unexported) to the struct will cause compilation to fail.

As an example,

	err = &net.DNSConfigError{err}

should be replaced by:

	err = &net.DNSConfigError{Err: err}
`

var Analyzer = &analysis.Analyzer{
	Name:             "composites",
	Doc:              Doc,
	Requires:         []*analysis.Analyzer{inspect.Analyzer},
	RunDespiteErrors: true,
	Run:              run,
}

var whitelist = true
var unkeyedLiteralSet map[string]bool

func init() {
	Analyzer.Flags.BoolVar(&whitelist, "whitelist", whitelist, "use composite white list; for testing only")

	unkeyedLiteralSet = make(map[string]bool)
	for _, typ := range unkeyedLiteral {
		unkeyedLiteralSet[typ] = true
	}
}

func DefaultAllowList() []string {
	return unkeyedLiteral
}

// runUnkeyedLiteral checks if a composite literal is a struct literal with
// unkeyed fields.
func CollectUnkeyed(pass *analysis.Pass, callback func (cl *ast.CompositeLit, typeName string)) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	nodeFilter := []ast.Node{
		(*ast.CompositeLit)(nil),
	}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		cl := n.(*ast.CompositeLit)

		typ := pass.TypesInfo.Types[cl].Type
		if typ == nil {
			// cannot determine composite literals' type, skip it
			return
		}
		under := typ.Underlying()
		for {
			ptr, ok := under.(*types.Pointer)
			if !ok {
				break
			}
			under = ptr.Elem().Underlying()
		}
		if _, ok := under.(*types.Struct); !ok {
			// skip non-struct composite literals
			return
		}
		if isLocalType(pass, typ) {
			// allow unkeyed locally defined composite literal
			return
		}

		// check if the CompositeLit contains an unkeyed field
		allKeyValue := true
		for _, e := range cl.Elts {
			if _, ok := e.(*ast.KeyValueExpr); !ok {
				allKeyValue = false
				break
			}
		}
		if allKeyValue {
			// all the composite literal fields are keyed
			return
		}

		typeName := typ.String()
		callback(cl, typeName)
	})
}
func run(pass *analysis.Pass) (interface{}, error) {
	CollectUnkeyed(pass, func (cl *ast.CompositeLit, typeName string) {
		if whitelist && unkeyedLiteralSet[typeName] {
			return
		}
		pass.ReportRangef(cl, "%s composite literal uses unkeyed fields", typeName)
	})
	return nil, nil
}

func isLocalType(pass *analysis.Pass, typ types.Type) bool {
	switch x := typ.(type) {
	case *types.Struct:
		// struct literals are local types
		return true
	case *types.Pointer:
		return isLocalType(pass, x.Elem())
	case *types.Named:
		// names in package foo are local to foo_test too
		return strings.TrimSuffix(x.Obj().Pkg().Path(), "_test") == strings.TrimSuffix(pass.Pkg.Path(), "_test")
	}
	return false
}
