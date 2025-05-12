// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unusedparams

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/util/moreslices"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/typesinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "unusedparams",
	Doc:      analysisinternal.MustExtractDoc(doc, "unusedparams"),
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/unusedparams",
}

const FixCategory = "unusedparams" // recognized by gopls ApplyFix

func run(pass *analysis.Pass) (any, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// First find all "address-taken" functions.
	// We must conservatively assume that their parameters
	// are all required to conform to some signature.
	//
	// A named function is address-taken if it is somewhere
	// used not in call position:
	//
	//	f(...)		// not address-taken
	//      use(f)          // address-taken
	//
	// A literal function is address-taken if it is not
	// immediately bound to a variable, or if that variable is
	// used not in call position:
	//
	//    f := func() { ... }; f()     			used only in call position
	//    var f func(); f = func() { ...f()... }; f()     	ditto
	//    use(func() { ... })				address-taken
	//

	// Note: this algorithm relies on the assumption that the
	// analyzer is called only for the "widest" package for a
	// given file: that is, p_test in preference to p, if both
	// exist. Analyzing only package p may produce diagnostics
	// that would be falsified based on declarations in p_test.go
	// files. The gopls analysis driver does this, but most
	// drivers to not, so running this command in, say,
	// unitchecker or multichecker may produce incorrect results.

	// Gather global information:
	// - uses of functions not in call position
	// - unexported interface methods
	// - all referenced variables

	usesOutsideCall := make(map[types.Object][]*ast.Ident)
	unexportedIMethodNames := make(map[string]bool)
	{
		callPosn := make(map[*ast.Ident]bool) // all idents f appearing in f() calls
		filter := []ast.Node{
			(*ast.CallExpr)(nil),
			(*ast.InterfaceType)(nil),
		}
		inspect.Preorder(filter, func(n ast.Node) {
			switch n := n.(type) {
			case *ast.CallExpr:
				id := typesinternal.UsedIdent(pass.TypesInfo, n.Fun)
				// Find object:
				// record non-exported function, method, or func-typed var.
				if id != nil && !id.IsExported() {
					switch pass.TypesInfo.Uses[id].(type) {
					case *types.Func, *types.Var:
						callPosn[id] = true
					}
				}

			case *ast.InterfaceType:
				// Record the set of names of unexported interface methods.
				// (It would be more precise to record signatures but
				// generics makes it tricky, and this conservative
				// heuristic is close enough.)
				t := pass.TypesInfo.TypeOf(n).(*types.Interface)
				for i := 0; i < t.NumExplicitMethods(); i++ {
					m := t.ExplicitMethod(i)
					if !m.Exported() && m.Name() != "_" {
						unexportedIMethodNames[m.Name()] = true
					}
				}
			}
		})

		for id, obj := range pass.TypesInfo.Uses {
			if !callPosn[id] {
				// This includes "f = func() {...}", which we deal with below.
				usesOutsideCall[obj] = append(usesOutsideCall[obj], id)
			}
		}
	}

	// Find all vars (notably parameters) that are used.
	usedVars := make(map[*types.Var]bool)
	for _, obj := range pass.TypesInfo.Uses {
		if v, ok := obj.(*types.Var); ok {
			if v.IsField() {
				continue // no point gathering these
			}
			usedVars[v] = true
		}
	}

	// Check each non-address-taken function's parameters are all used.
funcloop:
	for c := range inspect.Root().Preorder((*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)) {
		var (
			fn    types.Object // function symbol (*Func, possibly *Var for a FuncLit)
			ftype *ast.FuncType
			body  *ast.BlockStmt
		)
		switch n := c.Node().(type) {
		case *ast.FuncDecl:
			// We can't analyze non-Go functions.
			if n.Body == nil {
				continue
			}

			// Ignore exported functions and methods: we
			// must assume they may be address-taken in
			// another package.
			if n.Name.IsExported() {
				continue
			}

			// Ignore methods that match the name of any
			// interface method declared in this package,
			// as the method's signature may need to conform
			// to the interface.
			if n.Recv != nil && unexportedIMethodNames[n.Name.Name] {
				continue
			}

			fn = pass.TypesInfo.Defs[n.Name].(*types.Func)
			ftype, body = n.Type, n.Body

		case *ast.FuncLit:
			// Find the symbol for the variable (if any)
			// to which the FuncLit is bound.
			// (We don't bother to allow ParenExprs.)
			switch parent := c.Parent().Node().(type) {
			case *ast.AssignStmt:
				// f  = func() {...}
				// f := func() {...}
				if ek, idx := c.ParentEdge(); ek == edge.AssignStmt_Rhs {
					// Inv: n == AssignStmt.Rhs[idx]
					if id, ok := parent.Lhs[idx].(*ast.Ident); ok {
						fn = pass.TypesInfo.ObjectOf(id)

						// Edge case: f = func() {...}
						// should not count as a use.
						if pass.TypesInfo.Uses[id] != nil {
							usesOutsideCall[fn] = moreslices.Remove(usesOutsideCall[fn], id)
						}

						if fn == nil && id.Name == "_" {
							// Edge case: _ = func() {...}
							// has no local var. Fake one.
							v := types.NewVar(id.Pos(), pass.Pkg, id.Name, pass.TypesInfo.TypeOf(n))
							typesinternal.SetVarKind(v, typesinternal.LocalVar)
							fn = v
						}
					}
				}

			case *ast.ValueSpec:
				// var f = func() { ... }
				// (unless f is an exported package-level var)
				for i, val := range parent.Values {
					if val == n {
						v := pass.TypesInfo.Defs[parent.Names[i]]
						if !(v.Parent() == pass.Pkg.Scope() && v.Exported()) {
							fn = v
						}
						break
					}
				}
			}

			ftype, body = n.Type, n.Body
		}

		// Ignore address-taken functions and methods: unused
		// parameters may be needed to conform to a func type.
		if fn == nil || len(usesOutsideCall[fn]) > 0 {
			continue
		}

		// If there are no parameters, there are no unused parameters.
		if ftype.Params.NumFields() == 0 {
			continue
		}

		// To reduce false positives, ignore functions with an
		// empty or panic body.
		//
		// We choose not to ignore functions whose body is a
		// single return statement (as earlier versions did)
		// 	func f() { return }
		// 	func f() { return g(...) }
		// as we suspect that was just heuristic to reduce
		// false positives in the earlier unsound algorithm.
		switch len(body.List) {
		case 0:
			// Empty body. Although the parameter is
			// unnecessary, it's pretty obvious to the
			// reader that that's the case, so we allow it.
			continue // func f() {}
		case 1:
			if stmt, ok := body.List[0].(*ast.ExprStmt); ok {
				// We allow a panic body, as it is often a
				// placeholder for a future implementation:
				// 	func f() { panic(...) }
				if call, ok := stmt.X.(*ast.CallExpr); ok {
					if fun, ok := call.Fun.(*ast.Ident); ok && fun.Name == "panic" {
						continue
					}
				}
			}
		}

		// Don't report diagnostics on generated files.
		// (We can't skip analysis of generated files, though.)
		for curFile := range c.Enclosing((*ast.File)(nil)) {
			if ast.IsGenerated(curFile.Node().(*ast.File)) {
				continue funcloop
			}
		}

		// Report each unused parameter.
		for _, field := range ftype.Params.List {
			for _, id := range field.Names {
				if id.Name == "_" {
					continue
				}
				param := pass.TypesInfo.Defs[id].(*types.Var)
				if !usedVars[param] {
					start, end := field.Pos(), field.End()
					if len(field.Names) > 1 {
						start, end = id.Pos(), id.End()
					}

					// This diagnostic carries both an edit-based fix to
					// rename the unused parameter, and a command-based fix
					// to remove it (see golang.RemoveUnusedParameter).
					pass.Report(analysis.Diagnostic{
						Pos:      start,
						End:      end,
						Message:  fmt.Sprintf("unused parameter: %s", id.Name),
						Category: FixCategory,
						SuggestedFixes: []analysis.SuggestedFix{
							{
								Message: `Rename parameter to "_"`,
								TextEdits: []analysis.TextEdit{{
									Pos:     id.Pos(),
									End:     id.End(),
									NewText: []byte("_"),
								}},
							},
							{
								Message: fmt.Sprintf("Remove unused parameter %q", id.Name),
								// No TextEdits => computed by gopls command
							},
						},
					})
				}
			}
		}
	}
	return nil, nil
}
