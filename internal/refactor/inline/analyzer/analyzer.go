// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package analyzer

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
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/refactor/inline"
	"golang.org/x/tools/internal/typesinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:      "inline",
	Doc:       analysisinternal.MustExtractDoc(doc, "inline"),
	URL:       "https://pkg.go.dev/golang.org/x/tools/internal/refactor/inline/analyzer",
	Run:       run,
	FactTypes: []analysis.Fact{new(goFixInlineFuncFact), new(goFixForwardConstFact)},
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

	// Pass 1: find functions and constants annotated with an appropriate "//go:fix"
	// comment (the syntax proposed by #32816),
	// and export a fact for each one.
	inlinableFuncs := make(map[*types.Func]*inline.Callee) // memoization of fact import (nil => no fact)
	forwardableConsts := make(map[*types.Const]*goFixForwardConstFact)

	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	nodeFilter := []ast.Node{(*ast.FuncDecl)(nil), (*ast.GenDecl)(nil)}
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			hasInline, hasForward := fixDirectives(decl.Doc)
			if hasForward {
				pass.Reportf(decl.Doc.Pos(), "use //go:fix inline for functions")
				return
			}
			if !hasInline {
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
			declInline, declForward := fixDirectives(decl.Doc)
			if declInline {
				pass.Reportf(decl.Doc.Pos(), "use //go:fix forward for constants")
				return
			}
			// Accept forward directives on the entire decl as well as individual specs.
			for _, spec := range decl.Specs {
				spec := spec.(*ast.ValueSpec) // guaranteed by Tok == CONST
				specInline, specForward := fixDirectives(spec.Doc)
				if specInline {
					pass.Reportf(spec.Doc.Pos(), "use //go:fix forward for constants")
					return
				}
				if declForward || specForward {
					for i, name := range spec.Names {
						if i >= len(spec.Values) {
							// Possible following an iota.
							break
						}
						val := spec.Values[i]
						var rhsID *ast.Ident
						switch e := val.(type) {
						case *ast.Ident:
							// Constants defined with the predeclared iota cannot be forwarded.
							if pass.TypesInfo.Uses[e] == builtinIota {
								pass.Reportf(val.Pos(), "invalid //go:fix forward directive: const value is iota")
								continue
							}
							rhsID = e
						case *ast.SelectorExpr:
							rhsID = e.Sel
						default:
							pass.Reportf(val.Pos(), "invalid //go:fix forward directive: const value is not the name of another constant")
							continue
						}
						lhs := pass.TypesInfo.Defs[name].(*types.Const)
						rhs := pass.TypesInfo.Uses[rhsID].(*types.Const) // must be so in a well-typed program
						con := &goFixForwardConstFact{
							RHSName:    rhs.Name(),
							RHSPkgName: rhs.Pkg().Name(),
							RHSPkgPath: rhs.Pkg().Path(),
						}
						if rhs.Pkg() == pass.Pkg {
							con.rhsObj = rhs
						}
						forwardableConsts[lhs] = con
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

	// Pass 2. Inline each static call to an inlinable function,
	// and forward each reference to a forwardable constant.
	//
	// TODO(adonovan):  handle multiple diffs that each add the same import.
	nodeFilter = []ast.Node{
		(*ast.File)(nil),
		(*ast.CallExpr)(nil),
		(*ast.SelectorExpr)(nil),
		(*ast.Ident)(nil),
	}
	var currentFile *ast.File
	var currentSel *ast.SelectorExpr
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		if file, ok := n.(*ast.File); ok {
			currentFile = file
			return
		}
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
					return // nope
				}

				// Inline the call.
				content, err := readFile(call)
				if err != nil {
					pass.Reportf(call.Lparen, "invalid inlining candidate: cannot read source file: %v", err)
					return
				}
				caller := &inline.Caller{
					Fset:    pass.Fset,
					Types:   pass.Pkg,
					Info:    pass.TypesInfo,
					File:    currentFile,
					Call:    call,
					Content: content,
				}
				res, err := inline.Inline(caller, callee, &inline.Options{Logf: discard})
				if err != nil {
					pass.Reportf(call.Lparen, "%v", err)
					return
				}
				if res.Literalized {
					// Users are not fond of inlinings that literalize
					// f(x) to func() { ... }(), so avoid them.
					//
					// (Unfortunately the inliner is very timid,
					// and often literalizes when it cannot prove that
					// reducing the call is safe; the user of this tool
					// has no indication of what the problem is.)
					return
				}
				got := res.Content

				// Suggest the "fix".
				var textEdits []analysis.TextEdit
				for _, edit := range diff.Bytes(content, got) {
					textEdits = append(textEdits, analysis.TextEdit{
						Pos:     currentFile.FileStart + token.Pos(edit.Start),
						End:     currentFile.FileStart + token.Pos(edit.End),
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

		case *ast.SelectorExpr:
			currentSel = n

		case *ast.Ident:
			// If the identifier is a use of a forwardable constant, suggest forwarding it.
			if con, ok := pass.TypesInfo.Uses[n].(*types.Const); ok {
				fcon, ok := forwardableConsts[con]
				if !ok {
					var fact goFixForwardConstFact
					if pass.ImportObjectFact(con, &fact) {
						fcon = &fact
						forwardableConsts[con] = fcon
					}
				}
				if fcon == nil {
					return // nope
				}

				// If n is qualified by a package identifier, we'll need the full selector expression.
				var sel *ast.SelectorExpr
				if currentSel != nil && n == currentSel.Sel {
					sel = currentSel
					currentSel = nil
				}

				// We have an identifier A here (n), possibly qualified by a package identifier (sel.X),
				// and a forwardable "const A = B" elsewhere (fcon).
				// Consider replacing A with B.

				// Check that the expression we are inlining (B) means the same thing
				// (refers to the same object) in n's scope as it does in A's scope.
				// If the RHS is not in the current package, AddImport will handle
				// shadowing, so we only need to worry about when both expressions
				// are in the current package.
				if pass.Pkg.Path() == fcon.RHSPkgPath {
					// fcon.rhsObj is the object referred to by B in the definition of A.
					scope := pass.TypesInfo.Scopes[currentFile].Innermost(n.Pos()) // n's scope
					_, obj := scope.LookupParent(fcon.RHSName, n.Pos())            // what "B" means in n's scope
					if obj == nil {
						// Should be impossible: if code at n can refer to the LHS,
						// it can refer to the RHS.
						panic(fmt.Sprintf("no object for forwardable const %s RHS %s", n.Name, fcon.RHSName))
					}
					if obj != fcon.rhsObj {
						// "B" means something different here than at the forwardable const's scope.
						return
					}
				}
				importPrefix := ""
				var edits []analysis.TextEdit
				if fcon.RHSPkgPath != pass.Pkg.Path() {
					// TODO(jba): fix AddImport so that it returns "." if an existing dot import will work.
					// We will need to tell AddImport the name of the identifier we want to qualify (fcon.RHSName here).
					importID, eds := analysisinternal.AddImport(
						pass.TypesInfo, currentFile, n.Pos(), fcon.RHSPkgPath, fcon.RHSPkgName)
					importPrefix = importID + "."
					edits = eds
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
					NewText: []byte(importPrefix + fcon.RHSName),
				})
				pass.Report(analysis.Diagnostic{
					Pos:     pos,
					End:     end,
					Message: fmt.Sprintf("Constant %s should be forwarded", name),
					SuggestedFixes: []analysis.SuggestedFix{{
						Message:   fmt.Sprintf("Forward constant %s", name),
						TextEdits: edits,
					}},
				})
			}
		}
	})

	return nil, nil
}

// fixDirectives reports the presence of "//go:fix inline" and "//go:fix forward"
// directives in the comments.
func fixDirectives(cg *ast.CommentGroup) (inline, forward bool) {
	for _, d := range directives(cg) {
		if d.Tool == "go" && d.Name == "fix" {
			switch d.Args {
			case "inline":
				inline = true
			case "forward":
				forward = true
			}
		}
	}
	return
}

// A goFixInlineFuncFact is exported for each function marked "//go:fix inline".
// It holds information about the callee to support inlining.
type goFixInlineFuncFact struct{ Callee *inline.Callee }

func (f *goFixInlineFuncFact) String() string { return "goFixInline " + f.Callee.String() }
func (*goFixInlineFuncFact) AFact()           {}

// A goFixForwardConstFact is exported for each constant marked "//go:fix forward".
// It holds information about a forwardable constant. Gob-serializable.
type goFixForwardConstFact struct {
	// Information about "const LHSName = RHSName".
	RHSName    string
	RHSPkgPath string
	RHSPkgName string
	rhsObj     types.Object // for current package
}

func (c *goFixForwardConstFact) String() string {
	return fmt.Sprintf("goFixForward const %q.%s", c.RHSPkgPath, c.RHSName)
}

func (*goFixForwardConstFact) AFact() {}

func discard(string, ...any) {}

var builtinIota = types.Universe.Lookup("iota")
