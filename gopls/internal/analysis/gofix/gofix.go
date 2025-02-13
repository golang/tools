// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gofix

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	_ "embed"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/util/moreiters"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/astutil/cursor"
	"golang.org/x/tools/internal/astutil/edge"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/refactor/inline"
	"golang.org/x/tools/internal/typesinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:      "gofix",
	Doc:       analysisinternal.MustExtractDoc(doc, "gofix"),
	URL:       "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/gofix",
	Run:       run,
	FactTypes: []analysis.Fact{new(goFixInlineFuncFact), new(goFixInlineConstFact)},
	Requires:  []*analysis.Analyzer{inspect.Analyzer},
}

func run(pass *analysis.Pass) (any, error) {
	// Memoize repeated calls for same file.
	fileContent := make(map[string][]byte)
	readFile := func(node ast.Node) ([]byte, error) {
		filename := pass.Fset.File(node.Pos()).Name()
		content, ok := fileContent[filename]
		if !ok {
			var err error
			content, err = pass.ReadFile(filename)
			if err != nil {
				return nil, err
			}
			fileContent[filename] = content
		}
		return content, nil
	}

	// Return the unique ast.File for a cursor.
	currentFile := func(c cursor.Cursor) *ast.File {
		cf, _ := moreiters.First(c.Ancestors((*ast.File)(nil)))
		return cf.Node().(*ast.File)
	}

	// Pass 1: find functions and constants annotated with an appropriate "//go:fix"
	// comment (the syntax proposed by #32816),
	// and export a fact for each one.
	inlinableFuncs := make(map[*types.Func]*inline.Callee) // memoization of fact import (nil => no fact)
	inlinableConsts := make(map[*types.Const]*goFixInlineConstFact)

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{(*ast.FuncDecl)(nil), (*ast.GenDecl)(nil)}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			if !hasFixInline(decl.Doc) {
				return
			}
			content, err := readFile(decl)
			if err != nil {
				pass.Reportf(decl.Doc.Pos(), "invalid inlining candidate: cannot read source file: %v", err)
				return
			}
			callee, err := inline.AnalyzeCallee(discard, pass.Fset, pass.Pkg, pass.TypesInfo, decl, content)
			if err != nil {
				pass.Reportf(decl.Doc.Pos(), "invalid inlining candidate: %v", err)
				return
			}
			fn := pass.TypesInfo.Defs[decl.Name].(*types.Func)
			pass.ExportObjectFact(fn, &goFixInlineFuncFact{callee})
			inlinableFuncs[fn] = callee

		case *ast.GenDecl:
			if decl.Tok != token.CONST {
				return
			}
			declInline := hasFixInline(decl.Doc)
			// Accept inline directives on the entire decl as well as individual specs.
			for _, spec := range decl.Specs {
				spec := spec.(*ast.ValueSpec) // guaranteed by Tok == CONST
				specInline := hasFixInline(spec.Doc)
				if declInline || specInline {
					for i, name := range spec.Names {
						if i >= len(spec.Values) {
							// Possible following an iota.
							break
						}
						val := spec.Values[i]
						var rhsID *ast.Ident
						switch e := val.(type) {
						case *ast.Ident:
							// Constants defined with the predeclared iota cannot be inlined.
							if pass.TypesInfo.Uses[e] == builtinIota {
								pass.Reportf(val.Pos(), "invalid //go:fix inline directive: const value is iota")
								continue
							}
							rhsID = e
						case *ast.SelectorExpr:
							rhsID = e.Sel
						default:
							pass.Reportf(val.Pos(), "invalid //go:fix inline directive: const value is not the name of another constant")
							continue
						}
						lhs := pass.TypesInfo.Defs[name].(*types.Const)
						rhs := pass.TypesInfo.Uses[rhsID].(*types.Const) // must be so in a well-typed program
						con := &goFixInlineConstFact{
							RHSName:    rhs.Name(),
							RHSPkgName: rhs.Pkg().Name(),
							RHSPkgPath: rhs.Pkg().Path(),
						}
						if rhs.Pkg() == pass.Pkg {
							con.rhsObj = rhs
						}
						inlinableConsts[lhs] = con
						// Create a fact only if the LHS is exported and defined at top level.
						// We create a fact even if the RHS is non-exported,
						// so we can warn uses in other packages.
						if lhs.Exported() && typesinternal.IsPackageLevel(lhs) {
							pass.ExportObjectFact(lhs, con)
						}
					}
				}
			}
		}
	})

	// Pass 2. Inline each static call to an inlinable function
	// and each reference to an inlinable constant.
	//
	// TODO(adonovan):  handle multiple diffs that each add the same import.
	for cur := range cursor.Root(inspect).Preorder((*ast.CallExpr)(nil), (*ast.Ident)(nil)) {
		n := cur.Node()
		switch n := n.(type) {
		case *ast.CallExpr:
			call := n
			if fn := typeutil.StaticCallee(pass.TypesInfo, call); fn != nil {
				// Inlinable?
				callee, ok := inlinableFuncs[fn]
				if !ok {
					var fact goFixInlineFuncFact
					if pass.ImportObjectFact(fn, &fact) {
						callee = fact.Callee
						inlinableFuncs[fn] = callee
					}
				}
				if callee == nil {
					continue // nope
				}

				// Inline the call.
				content, err := readFile(call)
				if err != nil {
					pass.Reportf(call.Lparen, "invalid inlining candidate: cannot read source file: %v", err)
					continue
				}
				curFile := currentFile(cur)
				caller := &inline.Caller{
					Fset:    pass.Fset,
					Types:   pass.Pkg,
					Info:    pass.TypesInfo,
					File:    curFile,
					Call:    call,
					Content: content,
				}
				res, err := inline.Inline(caller, callee, &inline.Options{Logf: discard})
				if err != nil {
					pass.Reportf(call.Lparen, "%v", err)
					continue
				}
				if res.Literalized {
					// Users are not fond of inlinings that literalize
					// f(x) to func() { ... }(), so avoid them.
					//
					// (Unfortunately the inliner is very timid,
					// and often literalizes when it cannot prove that
					// reducing the call is safe; the user of this tool
					// has no indication of what the problem is.)
					continue
				}
				got := res.Content

				// Suggest the "fix".
				var textEdits []analysis.TextEdit
				for _, edit := range diff.Bytes(content, got) {
					textEdits = append(textEdits, analysis.TextEdit{
						Pos:     curFile.FileStart + token.Pos(edit.Start),
						End:     curFile.FileStart + token.Pos(edit.End),
						NewText: []byte(edit.New),
					})
				}
				pass.Report(analysis.Diagnostic{
					Pos:     call.Pos(),
					End:     call.End(),
					Message: fmt.Sprintf("Call of %v should be inlined", callee),
					SuggestedFixes: []analysis.SuggestedFix{{
						Message:   fmt.Sprintf("Inline call of %v", callee),
						TextEdits: textEdits,
					}},
				})
			}

		case *ast.Ident:
			// If the identifier is a use of an inlinable constant, suggest inlining it.
			if con, ok := pass.TypesInfo.Uses[n].(*types.Const); ok {
				incon, ok := inlinableConsts[con]
				if !ok {
					var fact goFixInlineConstFact
					if pass.ImportObjectFact(con, &fact) {
						incon = &fact
						inlinableConsts[con] = incon
					}
				}
				if incon == nil {
					continue // nope
				}

				// If n is qualified by a package identifier, we'll need the full selector expression.
				var sel *ast.SelectorExpr
				if e, _ := cur.Edge(); e == edge.SelectorExpr_Sel {
					sel = cur.Parent().Node().(*ast.SelectorExpr)
				}
				curFile := currentFile(cur)

				// We have an identifier A here (n), possibly qualified by a package identifier (sel.X),
				// and an inlinable "const A = B" elsewhere (fcon).
				// Consider replacing A with B.

				// Check that the expression we are inlining (B) means the same thing
				// (refers to the same object) in n's scope as it does in A's scope.
				// If the RHS is not in the current package, AddImport will handle
				// shadowing, so we only need to worry about when both expressions
				// are in the current package.
				if pass.Pkg.Path() == incon.RHSPkgPath {
					// fcon.rhsObj is the object referred to by B in the definition of A.
					scope := pass.TypesInfo.Scopes[curFile].Innermost(n.Pos()) // n's scope
					_, obj := scope.LookupParent(incon.RHSName, n.Pos())       // what "B" means in n's scope
					if obj == nil {
						// Should be impossible: if code at n can refer to the LHS,
						// it can refer to the RHS.
						panic(fmt.Sprintf("no object for inlinable const %s RHS %s", n.Name, incon.RHSName))
					}
					if obj != incon.rhsObj {
						// "B" means something different here than at the inlinable const's scope.
						continue
					}
				} else if !analysisinternal.CanImport(pass.Pkg.Path(), incon.RHSPkgPath) {
					// If this package can't see the RHS's package, we can't inline.
					continue
				}
				var (
					importPrefix string
					edits        []analysis.TextEdit
				)
				if incon.RHSPkgPath != pass.Pkg.Path() {
					_, importPrefix, edits = analysisinternal.AddImport(
						pass.TypesInfo, curFile, incon.RHSPkgName, incon.RHSPkgPath, incon.RHSName, n.Pos())
				}
				var (
					pos  = n.Pos()
					end  = n.End()
					name = n.Name
				)
				// Replace the entire SelectorExpr if there is one.
				if sel != nil {
					pos = sel.Pos()
					end = sel.End()
					name = sel.X.(*ast.Ident).Name + "." + n.Name
				}
				edits = append(edits, analysis.TextEdit{
					Pos:     pos,
					End:     end,
					NewText: []byte(importPrefix + incon.RHSName),
				})
				pass.Report(analysis.Diagnostic{
					Pos:     pos,
					End:     end,
					Message: fmt.Sprintf("Constant %s should be inlined", name),
					SuggestedFixes: []analysis.SuggestedFix{{
						Message:   fmt.Sprintf("Inline constant %s", name),
						TextEdits: edits,
					}},
				})
			}
		}
	}

	return nil, nil
}

// hasFixInline reports the presence of a "//go:fix inline" directive
// in the comments.
func hasFixInline(cg *ast.CommentGroup) bool {
	for _, d := range directives(cg) {
		if d.Tool == "go" && d.Name == "fix" && d.Args == "inline" {
			return true
		}
	}
	return false
}

// A goFixInlineFuncFact is exported for each function marked "//go:fix inline".
// It holds information about the callee to support inlining.
type goFixInlineFuncFact struct{ Callee *inline.Callee }

func (f *goFixInlineFuncFact) String() string { return "goFixInline " + f.Callee.String() }
func (*goFixInlineFuncFact) AFact()           {}

// A goFixInlineConstFact is exported for each constant marked "//go:fix inline".
// It holds information about an inlinable constant. Gob-serializable.
type goFixInlineConstFact struct {
	// Information about "const LHSName = RHSName".
	RHSName    string
	RHSPkgPath string
	RHSPkgName string
	rhsObj     types.Object // for current package
}

func (c *goFixInlineConstFact) String() string {
	return fmt.Sprintf("goFixInline const %q.%s", c.RHSPkgPath, c.RHSName)
}

func (*goFixInlineConstFact) AFact() {}

func discard(string, ...any) {}

var builtinIota = types.Universe.Lookup("iota")
