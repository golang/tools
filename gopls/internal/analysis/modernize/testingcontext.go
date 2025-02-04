// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/astutil/cursor"
	"golang.org/x/tools/internal/astutil/edge"
)

// The testingContext pass replaces calls to context.WithCancel from within
// tests to a use of testing.{T,B,F}.Context(), added in Go 1.24.
//
// Specifically, the testingContext pass suggests to replace:
//
//	ctx, cancel := context.WithCancel(context.Background()) // or context.TODO
//	defer cancel()
//
// with:
//
//	ctx := t.Context()
//
// provided:
//
//   - ctx and cancel are declared by the assignment
//   - the deferred call is the only use of cancel
//   - the call is within a test or subtest function
//   - the relevant testing.{T,B,F} is named and not shadowed at the call
func testingContext(pass *analysis.Pass) {
	if !analysisinternal.Imports(pass.Pkg, "testing") {
		return
	}

	info := pass.TypesInfo

	// checkCall finds eligible calls to context.WithCancel to replace.
	checkCall := func(cur cursor.Cursor) {
		call := cur.Node().(*ast.CallExpr)
		obj := typeutil.Callee(info, call)
		if !analysisinternal.IsFunctionNamed(obj, "context", "WithCancel") {
			return
		}

		// Have: context.WithCancel(arg)

		arg, ok := call.Args[0].(*ast.CallExpr)
		if !ok {
			return
		}
		if obj := typeutil.Callee(info, arg); !analysisinternal.IsFunctionNamed(obj, "context", "Background", "TODO") {
			return
		}

		// Have: context.WithCancel(context.{Background,TODO}())

		parent := cur.Parent()
		assign, ok := parent.Node().(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE {
			return
		}

		// Have: a, b := context.WithCancel(context.{Background,TODO}())

		// Check that both a and b are declared, not redeclarations.
		var lhs []types.Object
		for _, expr := range assign.Lhs {
			id, ok := expr.(*ast.Ident)
			if !ok {
				return
			}
			obj, ok := info.Defs[id]
			if !ok {
				return
			}
			lhs = append(lhs, obj)
		}

		next, ok := parent.NextSibling()
		if !ok {
			return
		}
		defr, ok := next.Node().(*ast.DeferStmt)
		if !ok {
			return
		}
		if soleUse(info, lhs[1]) != defr.Call.Fun {
			return
		}

		// Have:
		// a, b := context.WithCancel(context.{Background,TODO}())
		// defer b()

		// Check that we are in a test func.
		var testObj types.Object // relevant testing.{T,B,F}, or nil
		if curFunc, ok := enclosingFunc(cur); ok {
			switch n := curFunc.Node().(type) {
			case *ast.FuncLit:
				if e, idx := curFunc.Edge(); e == edge.CallExpr_Args && idx == 1 {
					// Have: call(..., func(...) { ...context.WithCancel(...)... })
					obj := typeutil.Callee(info, curFunc.Parent().Node().(*ast.CallExpr))
					if (analysisinternal.IsMethodNamed(obj, "testing", "T", "Run") ||
						analysisinternal.IsMethodNamed(obj, "testing", "B", "Run")) &&
						len(n.Type.Params.List[0].Names) == 1 {

						// Have tb.Run(..., func(..., tb *testing.[TB]) { ...context.WithCancel(...)... }
						testObj = info.Defs[n.Type.Params.List[0].Names[0]]
					}
				}

			case *ast.FuncDecl:
				testObj = isTestFn(info, n)
			}
		}

		if testObj != nil {
			// Have a test function. Check that we can resolve the relevant
			// testing.{T,B,F} at the current position.
			if _, obj := lhs[0].Parent().LookupParent(testObj.Name(), lhs[0].Pos()); obj == testObj {
				pass.Report(analysis.Diagnostic{
					Pos:      call.Fun.Pos(),
					End:      call.Fun.End(),
					Category: "testingcontext",
					Message:  fmt.Sprintf("context.WithCancel can be modernized using %s.Context", testObj.Name()),
					SuggestedFixes: []analysis.SuggestedFix{{
						Message: fmt.Sprintf("Replace context.WithCancel with %s.Context", testObj.Name()),
						TextEdits: []analysis.TextEdit{{
							Pos:     assign.Pos(),
							End:     defr.End(),
							NewText: fmt.Appendf(nil, "%s := %s.Context()", lhs[0].Name(), testObj.Name()),
						}},
					}},
				})
			}
		}
	}

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for curFile := range filesUsing(inspect, info, "go1.24") {
		for cur := range curFile.Preorder((*ast.CallExpr)(nil)) {
			checkCall(cur)
		}
	}
}

// soleUse returns the ident that refers to obj, if there is exactly one.
//
// TODO(rfindley): consider factoring to share with gopls/internal/refactor/inline.
func soleUse(info *types.Info, obj types.Object) (sole *ast.Ident) {
	// This is not efficient, but it is called infrequently.
	for id, obj2 := range info.Uses {
		if obj2 == obj {
			if sole != nil {
				return nil // not unique
			}
			sole = id
		}
	}
	return sole
}

// isTestFn checks whether fn is a test function (TestX, BenchmarkX, FuzzX),
// returning the corresponding types.Object of the *testing.{T,B,F} argument.
// Returns nil if fn is a test function, but the testing.{T,B,F} argument is
// unnamed (or _).
//
// TODO(rfindley): consider handling the case of an unnamed argument, by adding
// an edit to give the argument a name.
//
// Adapted from go/analysis/passes/tests.
// TODO(rfindley): consider refactoring to share logic.
func isTestFn(info *types.Info, fn *ast.FuncDecl) types.Object {
	// Want functions with 0 results and 1 parameter.
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 ||
		fn.Type.Params == nil ||
		len(fn.Type.Params.List) != 1 ||
		len(fn.Type.Params.List[0].Names) != 1 {

		return nil
	}

	prefix := testKind(fn.Name.Name)
	if prefix == "" {
		return nil
	}

	if tparams := fn.Type.TypeParams; tparams != nil && len(tparams.List) > 0 {
		return nil // test functions must not be generic
	}

	obj := info.Defs[fn.Type.Params.List[0].Names[0]]
	if obj == nil {
		return nil // e.g. _ *testing.T
	}

	var name string
	switch prefix {
	case "Test":
		name = "T"
	case "Benchmark":
		name = "B"
	case "Fuzz":
		name = "F"
	}

	if !analysisinternal.IsPointerToNamed(obj.Type(), "testing", name) {
		return nil
	}
	return obj
}

// testKind returns "Test", "Benchmark", or "Fuzz" if name is a valid resp.
// test, benchmark, or fuzz function name. Otherwise, isTestName returns "".
//
// Adapted from go/analysis/passes/tests.isTestName.
func testKind(name string) string {
	var prefix string
	switch {
	case strings.HasPrefix(name, "Test"):
		prefix = "Test"
	case strings.HasPrefix(name, "Benchmark"):
		prefix = "Benchmark"
	case strings.HasPrefix(name, "Fuzz"):
		prefix = "Fuzz"
	}
	if prefix == "" {
		return ""
	}
	suffix := name[len(prefix):]
	if len(suffix) == 0 {
		// "Test" is ok.
		return prefix
	}
	r, _ := utf8.DecodeRuneInString(suffix)
	if unicode.IsLower(r) {
		return ""
	}
	return prefix
}
