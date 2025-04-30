// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"fmt"
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
	typeindexanalyzer "golang.org/x/tools/internal/analysisinternal/typeindex"
	"golang.org/x/tools/internal/typesinternal/typeindex"
)

// stringscutprefix offers a fix to replace an if statement which
// calls to the 2 patterns below with strings.CutPrefix.
//
// Patterns:
//
//  1. if strings.HasPrefix(s, pre) { use(strings.TrimPrefix(s, pre) }
//     =>
//     if after, ok := strings.CutPrefix(s, pre); ok { use(after) }
//
//  2. if after := strings.TrimPrefix(s, pre); after != s { use(after) }
//     =>
//     if after, ok := strings.CutPrefix(s, pre); ok { use(after) }
//
// The use must occur within the first statement of the block, and the offered fix
// only replaces the first occurrence of strings.TrimPrefix.
//
// Variants:
// - bytes.HasPrefix usage as pattern 1.
func stringscutprefix(pass *analysis.Pass) {
	var (
		inspect = pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		index   = pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
		info    = pass.TypesInfo

		stringsTrimPrefix = index.Object("strings", "TrimPrefix")
		bytesTrimPrefix   = index.Object("bytes", "TrimPrefix")
	)
	if !index.Used(stringsTrimPrefix, bytesTrimPrefix) {
		return
	}

	const (
		category     = "stringscutprefix"
		fixedMessage = "Replace HasPrefix/TrimPrefix with CutPrefix"
	)

	for curFile := range filesUsing(inspect, pass.TypesInfo, "go1.20") {
		for curIfStmt := range curFile.Preorder((*ast.IfStmt)(nil)) {
			ifStmt := curIfStmt.Node().(*ast.IfStmt)

			// pattern1
			if call, ok := ifStmt.Cond.(*ast.CallExpr); ok && ifStmt.Init == nil && len(ifStmt.Body.List) > 0 {

				obj := typeutil.Callee(info, call)
				if !analysisinternal.IsFunctionNamed(obj, "strings", "HasPrefix") &&
					!analysisinternal.IsFunctionNamed(obj, "bytes", "HasPrefix") {
					continue
				}

				// Replace the first occurrence of strings.TrimPrefix(s, pre) in the first statement only,
				// but not later statements in case s or pre are modified by intervening logic.
				firstStmt := curIfStmt.Child(ifStmt.Body).Child(ifStmt.Body.List[0])
				for curCall := range firstStmt.Preorder((*ast.CallExpr)(nil)) {
					call1 := curCall.Node().(*ast.CallExpr)
					obj1 := typeutil.Callee(info, call1)
					// bytesTrimPrefix or stringsTrimPrefix might be nil if the file doesn't import it,
					// so we need to ensure the obj1 is not nil otherwise the call1 is not TrimPrefix and cause a panic.
					if obj1 == nil ||
						obj1 != stringsTrimPrefix && obj1 != bytesTrimPrefix {
						continue
					}
					// Have: if strings.HasPrefix(s0, pre0) { ...strings.TrimPrefix(s, pre)... }
					var (
						s0   = call.Args[0]
						pre0 = call.Args[1]
						s    = call1.Args[0]
						pre  = call1.Args[1]
					)

					// check whether the obj1 uses the exact the same argument with strings.HasPrefix
					// shadow variables won't be valid because we only access the first statement.
					if equalSyntax(s0, s) && equalSyntax(pre0, pre) {
						after := analysisinternal.FreshName(info.Scopes[ifStmt], ifStmt.Pos(), "after")
						_, prefix, importEdits := analysisinternal.AddImport(
							info,
							curFile.Node().(*ast.File),
							obj1.Pkg().Name(),
							obj1.Pkg().Path(),
							"CutPrefix",
							call.Pos(),
						)
						okVarName := analysisinternal.FreshName(info.Scopes[ifStmt], ifStmt.Pos(), "ok")
						pass.Report(analysis.Diagnostic{
							// highlight at HasPrefix call.
							Pos:      call.Pos(),
							End:      call.End(),
							Category: category,
							Message:  "HasPrefix + TrimPrefix can be simplified to CutPrefix",
							SuggestedFixes: []analysis.SuggestedFix{{
								Message: fixedMessage,
								// if              strings.HasPrefix(s, pre)     { use(strings.TrimPrefix(s, pre)) }
								//    ------------ -----------------        -----      --------------------------
								// if after, ok := strings.CutPrefix(s, pre); ok { use(after)                      }
								TextEdits: append(importEdits, []analysis.TextEdit{
									{
										Pos:     call.Fun.Pos(),
										End:     call.Fun.Pos(),
										NewText: fmt.Appendf(nil, "%s, %s :=", after, okVarName),
									},
									{
										Pos:     call.Fun.Pos(),
										End:     call.Fun.End(),
										NewText: fmt.Appendf(nil, "%sCutPrefix", prefix),
									},
									{
										Pos:     call.End(),
										End:     call.End(),
										NewText: fmt.Appendf(nil, "; %s ", okVarName),
									},
									{
										Pos:     call1.Pos(),
										End:     call1.End(),
										NewText: []byte(after),
									},
								}...),
							}}},
						)
						break
					}
				}
			}

			// pattern2
			if bin, ok := ifStmt.Cond.(*ast.BinaryExpr); ok &&
				bin.Op == token.NEQ &&
				ifStmt.Init != nil &&
				isSimpleAssign(ifStmt.Init) {
				assign := ifStmt.Init.(*ast.AssignStmt)
				if call, ok := assign.Rhs[0].(*ast.CallExpr); ok && assign.Tok == token.DEFINE {
					lhs := assign.Lhs[0]
					obj := typeutil.Callee(info, call)
					if obj == stringsTrimPrefix &&
						(equalSyntax(lhs, bin.X) && equalSyntax(call.Args[0], bin.Y) ||
							(equalSyntax(lhs, bin.Y) && equalSyntax(call.Args[0], bin.X))) {
						okVarName := analysisinternal.FreshName(info.Scopes[ifStmt], ifStmt.Pos(), "ok")
						// Have one of:
						//   if rest := TrimPrefix(s, prefix); rest != s {
						//   if rest := TrimPrefix(s, prefix); s != rest {

						// We use AddImport not to add an import (since it exists already)
						// but to compute the correct prefix in the dot-import case.
						_, prefix, importEdits := analysisinternal.AddImport(
							info,
							curFile.Node().(*ast.File),
							obj.Pkg().Name(),
							obj.Pkg().Path(),
							"CutPrefix",
							call.Pos(),
						)

						pass.Report(analysis.Diagnostic{
							// highlight from the init and the condition end.
							Pos:      ifStmt.Init.Pos(),
							End:      ifStmt.Cond.End(),
							Category: category,
							Message:  "TrimPrefix can be simplified to CutPrefix",
							SuggestedFixes: []analysis.SuggestedFix{{
								Message: fixedMessage,
								// if x     := strings.TrimPrefix(s, pre); x != s ...
								//     ----            ----------          ------
								// if x, ok := strings.CutPrefix (s, pre); ok     ...
								TextEdits: append(importEdits, []analysis.TextEdit{
									{
										Pos:     assign.Lhs[0].End(),
										End:     assign.Lhs[0].End(),
										NewText: fmt.Appendf(nil, ", %s", okVarName),
									},
									{
										Pos:     call.Fun.Pos(),
										End:     call.Fun.End(),
										NewText: fmt.Appendf(nil, "%sCutPrefix", prefix),
									},
									{
										Pos:     ifStmt.Cond.Pos(),
										End:     ifStmt.Cond.End(),
										NewText: []byte(okVarName),
									},
								}...),
							}},
						})
					}
				}
			}
		}
	}
}
