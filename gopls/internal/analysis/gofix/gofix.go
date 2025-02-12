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
	var (
		inlinableFuncs   = make(map[*types.Func]*inline.Callee) // memoization of fact import (nil => no fact)
		inlinableConsts  = make(map[*types.Const]*goFixInlineConstFact)
		inlinableAliases = make(map[*types.TypeName]*goFixInlineAliasFact)
	)

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
			if decl.Tok != token.CONST && decl.Tok != token.TYPE {
				return
			}
			declInline := hasFixInline(decl.Doc)
			// Accept inline directives on the entire decl as well as individual specs.
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec: // Tok == TYPE
					if !declInline && !hasFixInline(spec.Doc) {
						continue
					}
					if !spec.Assign.IsValid() {
						pass.Reportf(spec.Pos(), "invalid //go:fix inline directive: not a type alias")
						continue
					}
					if spec.TypeParams != nil {
						// TODO(jba): handle generic aliases
						continue
					}
					// The alias must refer to another named type.
					// TODO(jba): generalize to more type expressions.
					var rhsID *ast.Ident
					switch e := ast.Unparen(spec.Type).(type) {
					case *ast.Ident:
						rhsID = e
					case *ast.SelectorExpr:
						rhsID = e.Sel
					default:
						continue
					}
					lhs := pass.TypesInfo.Defs[spec.Name].(*types.TypeName)
					// more (jba): test one alias pointing to another alias
					rhs := pass.TypesInfo.Uses[rhsID].(*types.TypeName)
					typ := &goFixInlineAliasFact{
						RHSName:    rhs.Name(),
						RHSPkgName: rhs.Pkg().Name(),
						RHSPkgPath: rhs.Pkg().Path(),
					}
					if rhs.Pkg() == pass.Pkg {
						typ.rhsObj = rhs
					}
					inlinableAliases[lhs] = typ
					// Create a fact only if the LHS is exported and defined at top level.
					// We create a fact even if the RHS is non-exported,
					// so we can warn about uses in other packages.
					if lhs.Exported() && typesinternal.IsPackageLevel(lhs) {
						pass.ExportObjectFact(lhs, typ)
					}

				case *ast.ValueSpec: // Tok == CONST
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
							// so we can warn about uses in other packages.
							if lhs.Exported() && typesinternal.IsPackageLevel(lhs) {
								pass.ExportObjectFact(lhs, con)
							}
						}
					}
				}
			}
		}
	})

	// Pass 2. Inline each static call to an inlinable function
	// and each reference to an inlinable constant or type alias.
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
			// If the identifier is a use of an inlinable type alias, suggest inlining it.
			// TODO(jba): much of this code is shared with the constant case, below.
			// Try to factor more of it out, unless it will change anyway when we move beyond simple RHS's.
			if ali, ok := pass.TypesInfo.Uses[n].(*types.TypeName); ok {
				inalias, ok := inlinableAliases[ali]
				if !ok {
					var fact goFixInlineAliasFact
					if pass.ImportObjectFact(ali, &fact) {
						inalias = &fact
						inlinableAliases[ali] = inalias
					}
				}
				if inalias == nil {
					continue // nope
				}
				curFile := currentFile(cur)

				// We have an identifier A here (n), possibly qualified by a package identifier (sel.X,
				// where sel is the parent of X), // and an inlinable "type A = B" elsewhere (inali).
				// Consider replacing A with B.

				// Check that the expression we are inlining (B) means the same thing
				// (refers to the same object) in n's scope as it does in A's scope.
				// If the RHS is not in the current package, AddImport will handle
				// shadowing, so we only need to worry about when both expressions
				// are in the current package.
				if pass.Pkg.Path() == inalias.RHSPkgPath {
					// fcon.rhsObj is the object referred to by B in the definition of A.
					scope := pass.TypesInfo.Scopes[curFile].Innermost(n.Pos()) // n's scope
					_, obj := scope.LookupParent(inalias.RHSName, n.Pos())     // what "B" means in n's scope
					if obj == nil {
						// Should be impossible: if code at n can refer to the LHS,
						// it can refer to the RHS.
						panic(fmt.Sprintf("no object for inlinable alias %s RHS %s", n.Name, inalias.RHSName))
					}
					if obj != inalias.rhsObj {
						// "B" means something different here than at the inlinable const's scope.
						continue
					}
				} else if !analysisinternal.CanImport(pass.Pkg.Path(), inalias.RHSPkgPath) {
					// If this package can't see the RHS's package, we can't inline.
					continue
				}
				var (
					importPrefix string
					edits        []analysis.TextEdit
				)
				if inalias.RHSPkgPath != pass.Pkg.Path() {
					_, importPrefix, edits = analysisinternal.AddImport(
						pass.TypesInfo, curFile, inalias.RHSPkgName, inalias.RHSPkgPath, inalias.RHSName, n.Pos())
				}
				// If n is qualified by a package identifier, we'll need the full selector expression.
				var expr ast.Expr = n
				if e, _ := cur.Edge(); e == edge.SelectorExpr_Sel {
					expr = cur.Parent().Node().(ast.Expr)
				}
				reportInline(pass, "type alias", "Type alias", expr, edits, importPrefix+inalias.RHSName)
				continue
			}
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
				curFile := currentFile(cur)

				// We have an identifier A here (n), possibly qualified by a package identifier (sel.X,
				// where sel is the parent of n), // and an inlinable "const A = B" elsewhere (incon).
				// Consider replacing A with B.

				// Check that the expression we are inlining (B) means the same thing
				// (refers to the same object) in n's scope as it does in A's scope.
				// If the RHS is not in the current package, AddImport will handle
				// shadowing, so we only need to worry about when both expressions
				// are in the current package.
				if pass.Pkg.Path() == incon.RHSPkgPath {
					// incon.rhsObj is the object referred to by B in the definition of A.
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
				// If n is qualified by a package identifier, we'll need the full selector expression.
				var expr ast.Expr = n
				if e, _ := cur.Edge(); e == edge.SelectorExpr_Sel {
					expr = cur.Parent().Node().(ast.Expr)
				}
				reportInline(pass, "constant", "Constant", expr, edits, importPrefix+incon.RHSName)
			}
		}
	}

	return nil, nil
}

// reportInline reports a diagnostic for fixing an inlinable name.
func reportInline(pass *analysis.Pass, kind, capKind string, ident ast.Expr, edits []analysis.TextEdit, newText string) {
	edits = append(edits, analysis.TextEdit{
		Pos:     ident.Pos(),
		End:     ident.End(),
		NewText: []byte(newText),
	})
	name := analysisinternal.Format(pass.Fset, ident)
	pass.Report(analysis.Diagnostic{
		Pos:     ident.Pos(),
		End:     ident.End(),
		Message: fmt.Sprintf("%s %s should be inlined", capKind, name),
		SuggestedFixes: []analysis.SuggestedFix{{
			Message:   fmt.Sprintf("Inline %s %s", kind, name),
			TextEdits: edits,
		}},
	})
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

// A goFixInlineAliasFact is exported for each type alias marked "//go:fix inline".
// It holds information about an inlinable type alias. Gob-serializable.
type goFixInlineAliasFact struct {
	// Information about "type LHSName = RHSName".
	RHSName    string
	RHSPkgPath string
	RHSPkgName string
	rhsObj     types.Object // for current package
}

func (c *goFixInlineAliasFact) String() string {
	return fmt.Sprintf("goFixInline alias %q.%s", c.RHSPkgPath, c.RHSName)
}

func (*goFixInlineAliasFact) AFact() {}

func discard(string, ...any) {}

var builtinIota = types.Universe.Lookup("iota")
