// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gofix

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"iter"
	"slices"
	"strings"

	_ "embed"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/gofix/findgofix"
	"golang.org/x/tools/internal/refactor/inline"
	"golang.org/x/tools/internal/typesinternal"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name: "gofix",
	Doc:  analysisinternal.MustExtractDoc(doc, "gofix"),
	URL:  "https://pkg.go.dev/golang.org/x/tools/internal/gofix",
	Run:  run,
	FactTypes: []analysis.Fact{
		(*goFixInlineFuncFact)(nil),
		(*goFixInlineConstFact)(nil),
		(*goFixInlineAliasFact)(nil),
	},
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

var allowBindingDecl bool

func init() {
	Analyzer.Flags.BoolVar(&allowBindingDecl, "allow_binding_decl", false,
		"permit inlinings that require a 'var params = args' declaration")
}

// analyzer holds the state for this analysis.
type analyzer struct {
	pass *analysis.Pass
	root inspector.Cursor
	// memoization of repeated calls for same file.
	fileContent map[string][]byte
	// memoization of fact imports (nil => no fact)
	inlinableFuncs   map[*types.Func]*inline.Callee
	inlinableConsts  map[*types.Const]*goFixInlineConstFact
	inlinableAliases map[*types.TypeName]*goFixInlineAliasFact
}

func run(pass *analysis.Pass) (any, error) {
	a := &analyzer{
		pass:             pass,
		root:             pass.ResultOf[inspect.Analyzer].(*inspector.Inspector).Root(),
		fileContent:      make(map[string][]byte),
		inlinableFuncs:   make(map[*types.Func]*inline.Callee),
		inlinableConsts:  make(map[*types.Const]*goFixInlineConstFact),
		inlinableAliases: make(map[*types.TypeName]*goFixInlineAliasFact),
	}
	findgofix.Find(pass, a.root, a)
	a.inline()
	return nil, nil
}

// HandleFunc exports a fact for functions marked with go:fix.
func (a *analyzer) HandleFunc(decl *ast.FuncDecl) {
	content, err := a.readFile(decl)
	if err != nil {
		a.pass.Reportf(decl.Doc.Pos(), "invalid inlining candidate: cannot read source file: %v", err)
		return
	}
	callee, err := inline.AnalyzeCallee(discard, a.pass.Fset, a.pass.Pkg, a.pass.TypesInfo, decl, content)
	if err != nil {
		a.pass.Reportf(decl.Doc.Pos(), "invalid inlining candidate: %v", err)
		return
	}
	fn := a.pass.TypesInfo.Defs[decl.Name].(*types.Func)
	a.pass.ExportObjectFact(fn, &goFixInlineFuncFact{callee})
	a.inlinableFuncs[fn] = callee
}

// HandleAlias exports a fact for aliases marked with go:fix.
func (a *analyzer) HandleAlias(spec *ast.TypeSpec) {
	// Remember that this is an inlinable alias.
	typ := &goFixInlineAliasFact{}
	lhs := a.pass.TypesInfo.Defs[spec.Name].(*types.TypeName)
	a.inlinableAliases[lhs] = typ
	// Create a fact only if the LHS is exported and defined at top level.
	// We create a fact even if the RHS is non-exported,
	// so we can warn about uses in other packages.
	if lhs.Exported() && typesinternal.IsPackageLevel(lhs) {
		a.pass.ExportObjectFact(lhs, typ)
	}
}

// HandleConst exports a fact for constants marked with go:fix.
func (a *analyzer) HandleConst(nameIdent, rhsIdent *ast.Ident) {
	lhs := a.pass.TypesInfo.Defs[nameIdent].(*types.Const)
	rhs := a.pass.TypesInfo.Uses[rhsIdent].(*types.Const) // must be so in a well-typed program
	con := &goFixInlineConstFact{
		RHSName:    rhs.Name(),
		RHSPkgName: rhs.Pkg().Name(),
		RHSPkgPath: rhs.Pkg().Path(),
	}
	if rhs.Pkg() == a.pass.Pkg {
		con.rhsObj = rhs
	}
	a.inlinableConsts[lhs] = con
	// Create a fact only if the LHS is exported and defined at top level.
	// We create a fact even if the RHS is non-exported,
	// so we can warn about uses in other packages.
	if lhs.Exported() && typesinternal.IsPackageLevel(lhs) {
		a.pass.ExportObjectFact(lhs, con)
	}
}

// inline inlines each static call to an inlinable function
// and each reference to an inlinable constant or type alias.
//
// TODO(adonovan):  handle multiple diffs that each add the same import.
func (a *analyzer) inline() {
	for cur := range a.root.Preorder((*ast.CallExpr)(nil), (*ast.Ident)(nil)) {
		switch n := cur.Node().(type) {
		case *ast.CallExpr:
			a.inlineCall(n, cur)

		case *ast.Ident:
			switch t := a.pass.TypesInfo.Uses[n].(type) {
			case *types.TypeName:
				a.inlineAlias(t, cur)
			case *types.Const:
				a.inlineConst(t, cur)
			}
		}
	}
}

// If call is a call to an inlinable func, suggest inlining its use at cur.
func (a *analyzer) inlineCall(call *ast.CallExpr, cur inspector.Cursor) {
	if fn := typeutil.StaticCallee(a.pass.TypesInfo, call); fn != nil {
		// Inlinable?
		callee, ok := a.inlinableFuncs[fn]
		if !ok {
			var fact goFixInlineFuncFact
			if a.pass.ImportObjectFact(fn, &fact) {
				callee = fact.Callee
				a.inlinableFuncs[fn] = callee
			}
		}
		if callee == nil {
			return // nope
		}

		// Inline the call.
		content, err := a.readFile(call)
		if err != nil {
			a.pass.Reportf(call.Lparen, "invalid inlining candidate: cannot read source file: %v", err)
			return
		}
		curFile := currentFile(cur)
		caller := &inline.Caller{
			Fset:    a.pass.Fset,
			Types:   a.pass.Pkg,
			Info:    a.pass.TypesInfo,
			File:    curFile,
			Call:    call,
			Content: content,
		}
		res, err := inline.Inline(caller, callee, &inline.Options{Logf: discard})
		if err != nil {
			a.pass.Reportf(call.Lparen, "%v", err)
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
		if res.BindingDecl && !allowBindingDecl {
			// When applying fix en masse, users are similarly
			// unenthusiastic about inlinings that cannot
			// entirely eliminate the parameters and
			// insert a 'var params = args' declaration.
			// The flag allows them to decline such fixes.
			return
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
		a.pass.Report(analysis.Diagnostic{
			Pos:     call.Pos(),
			End:     call.End(),
			Message: fmt.Sprintf("Call of %v should be inlined", callee),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message:   fmt.Sprintf("Inline call of %v", callee),
				TextEdits: textEdits,
			}},
		})
	}
}

// If tn is the TypeName of an inlinable alias, suggest inlining its use at cur.
func (a *analyzer) inlineAlias(tn *types.TypeName, curId inspector.Cursor) {
	inalias, ok := a.inlinableAliases[tn]
	if !ok {
		var fact goFixInlineAliasFact
		if a.pass.ImportObjectFact(tn, &fact) {
			inalias = &fact
			a.inlinableAliases[tn] = inalias
		}
	}
	if inalias == nil {
		return // nope
	}

	alias := tn.Type().(*types.Alias)
	// Remember the names of the alias's type params. When we check for shadowing
	// later, we'll ignore these because they won't appear in the replacement text.
	typeParamNames := map[*types.TypeName]bool{}
	for tp := range listIter(alias.TypeParams()) {
		typeParamNames[tp.Obj()] = true
	}
	rhs := alias.Rhs()
	curPath := a.pass.Pkg.Path()
	curFile := currentFile(curId)
	id := curId.Node().(*ast.Ident)
	// We have an identifier A here (n), possibly qualified by a package
	// identifier (sel.n), and an inlinable "type A = rhs" elsewhere.
	//
	// We can replace A with rhs if no name in rhs is shadowed at n's position,
	// and every package in rhs is importable by the current package.

	var (
		importPrefixes = map[string]string{curPath: ""} // from pkg path to prefix
		edits          []analysis.TextEdit
	)
	for _, tn := range typenames(rhs) {
		// Ignore the type parameters of the alias: they won't appear in the result.
		if typeParamNames[tn] {
			continue
		}
		var pkgPath, pkgName string
		if pkg := tn.Pkg(); pkg != nil {
			pkgPath = pkg.Path()
			pkgName = pkg.Name()
		}
		if pkgPath == "" || pkgPath == curPath {
			// The name is in the current package or the universe scope, so no import
			// is required. Check that it is not shadowed (that is, that the type
			// it refers to in rhs is the same one it refers to at n).
			scope := a.pass.TypesInfo.Scopes[curFile].Innermost(id.Pos()) // n's scope
			_, obj := scope.LookupParent(tn.Name(), id.Pos())             // what qn.name means in n's scope
			if obj != tn {
				return
			}
		} else if !analysisinternal.CanImport(a.pass.Pkg.Path(), pkgPath) {
			// If this package can't see the package of this part of rhs, we can't inline.
			return
		} else if _, ok := importPrefixes[pkgPath]; !ok {
			// Use AddImport to add pkgPath if it's not there already. Associate the prefix it assigns
			// with the package path for use by the TypeString qualifier below.
			_, prefix, eds := analysisinternal.AddImport(
				a.pass.TypesInfo, curFile, pkgName, pkgPath, tn.Name(), id.Pos())
			importPrefixes[pkgPath] = strings.TrimSuffix(prefix, ".")
			edits = append(edits, eds...)
		}
	}
	// Find the complete identifier, which may take any of these forms:
	//       Id
	//       Id[T]
	//       Id[K, V]
	//   pkg.Id
	//   pkg.Id[T]
	//   pkg.Id[K, V]
	var expr ast.Expr = id
	if ek, _ := curId.ParentEdge(); ek == edge.SelectorExpr_Sel {
		curId = curId.Parent()
		expr = curId.Node().(ast.Expr)
	}
	// If expr is part of an IndexExpr or IndexListExpr, we'll need that node.
	// Given C[int], TypeOf(C) is generic but TypeOf(C[int]) is instantiated.
	switch ek, _ := curId.ParentEdge(); ek {
	case edge.IndexExpr_X:
		expr = curId.Parent().Node().(*ast.IndexExpr)
	case edge.IndexListExpr_X:
		expr = curId.Parent().Node().(*ast.IndexListExpr)
	}
	t := a.pass.TypesInfo.TypeOf(expr).(*types.Alias) // type of entire identifier
	if targs := t.TypeArgs(); targs.Len() > 0 {
		// Instantiate the alias with the type args from this use.
		// For example, given type A = M[K, V], compute the type of the use
		// A[int, Foo] as M[int, Foo].
		// Don't validate instantiation: it can't panic unless we have a bug,
		// in which case seeing the stack trace via telemetry would be helpful.
		instAlias, _ := types.Instantiate(nil, alias, slices.Collect(listIter(targs)), false)
		rhs = instAlias.(*types.Alias).Rhs()
	}
	// To get the replacement text, render the alias RHS using the package prefixes
	// we assigned above.
	newText := types.TypeString(rhs, func(p *types.Package) string {
		if p == a.pass.Pkg {
			return ""
		}
		if prefix, ok := importPrefixes[p.Path()]; ok {
			return prefix
		}
		panic(fmt.Sprintf("in %q, package path %q has no import prefix", rhs, p.Path()))
	})
	a.reportInline("type alias", "Type alias", expr, edits, newText)
}

// typenames returns the TypeNames for types within t (including t itself) that have
// them: basic types, named types and alias types.
// The same name may appear more than once.
func typenames(t types.Type) []*types.TypeName {
	var tns []*types.TypeName

	var visit func(types.Type)
	visit = func(t types.Type) {
		if hasName, ok := t.(interface{ Obj() *types.TypeName }); ok {
			tns = append(tns, hasName.Obj())
		}
		switch t := t.(type) {
		case *types.Basic:
			tns = append(tns, types.Universe.Lookup(t.Name()).(*types.TypeName))
		case *types.Named:
			for t := range listIter(t.TypeArgs()) {
				visit(t)
			}
		case *types.Alias:
			for t := range listIter(t.TypeArgs()) {
				visit(t)
			}
		case *types.TypeParam:
			tns = append(tns, t.Obj())
		case *types.Pointer:
			visit(t.Elem())
		case *types.Slice:
			visit(t.Elem())
		case *types.Array:
			visit(t.Elem())
		case *types.Chan:
			visit(t.Elem())
		case *types.Map:
			visit(t.Key())
			visit(t.Elem())
		case *types.Struct:
			for i := range t.NumFields() {
				visit(t.Field(i).Type())
			}
		case *types.Signature:
			// Ignore the receiver: although it may be present, it has no meaning
			// in a type expression.
			// Ditto for receiver type params.
			// Also, function type params cannot appear in a type expression.
			if t.TypeParams() != nil {
				panic("Signature.TypeParams in type expression")
			}
			visit(t.Params())
			visit(t.Results())
		case *types.Interface:
			for i := range t.NumEmbeddeds() {
				visit(t.EmbeddedType(i))
			}
			for i := range t.NumExplicitMethods() {
				visit(t.ExplicitMethod(i).Type())
			}
		case *types.Tuple:
			for v := range listIter(t) {
				visit(v.Type())
			}
		case *types.Union:
			panic("Union in type expression")
		default:
			panic(fmt.Sprintf("unknown type %T", t))
		}
	}

	visit(t)

	return tns
}

// If con is an inlinable constant, suggest inlining its use at cur.
func (a *analyzer) inlineConst(con *types.Const, cur inspector.Cursor) {
	incon, ok := a.inlinableConsts[con]
	if !ok {
		var fact goFixInlineConstFact
		if a.pass.ImportObjectFact(con, &fact) {
			incon = &fact
			a.inlinableConsts[con] = incon
		}
	}
	if incon == nil {
		return // nope
	}

	// If n is qualified by a package identifier, we'll need the full selector expression.
	curFile := currentFile(cur)
	n := cur.Node().(*ast.Ident)

	// We have an identifier A here (n), possibly qualified by a package identifier (sel.X,
	// where sel is the parent of n), // and an inlinable "const A = B" elsewhere (incon).
	// Consider replacing A with B.

	// Check that the expression we are inlining (B) means the same thing
	// (refers to the same object) in n's scope as it does in A's scope.
	// If the RHS is not in the current package, AddImport will handle
	// shadowing, so we only need to worry about when both expressions
	// are in the current package.
	if a.pass.Pkg.Path() == incon.RHSPkgPath {
		// incon.rhsObj is the object referred to by B in the definition of A.
		scope := a.pass.TypesInfo.Scopes[curFile].Innermost(n.Pos()) // n's scope
		_, obj := scope.LookupParent(incon.RHSName, n.Pos())         // what "B" means in n's scope
		if obj == nil {
			// Should be impossible: if code at n can refer to the LHS,
			// it can refer to the RHS.
			panic(fmt.Sprintf("no object for inlinable const %s RHS %s", n.Name, incon.RHSName))
		}
		if obj != incon.rhsObj {
			// "B" means something different here than at the inlinable const's scope.
			return
		}
	} else if !analysisinternal.CanImport(a.pass.Pkg.Path(), incon.RHSPkgPath) {
		// If this package can't see the RHS's package, we can't inline.
		return
	}
	var (
		importPrefix string
		edits        []analysis.TextEdit
	)
	if incon.RHSPkgPath != a.pass.Pkg.Path() {
		_, importPrefix, edits = analysisinternal.AddImport(
			a.pass.TypesInfo, curFile, incon.RHSPkgName, incon.RHSPkgPath, incon.RHSName, n.Pos())
	}
	// If n is qualified by a package identifier, we'll need the full selector expression.
	var expr ast.Expr = n
	if ek, _ := cur.ParentEdge(); ek == edge.SelectorExpr_Sel {
		expr = cur.Parent().Node().(ast.Expr)
	}
	a.reportInline("constant", "Constant", expr, edits, importPrefix+incon.RHSName)
}

// reportInline reports a diagnostic for fixing an inlinable name.
func (a *analyzer) reportInline(kind, capKind string, ident ast.Expr, edits []analysis.TextEdit, newText string) {
	edits = append(edits, analysis.TextEdit{
		Pos:     ident.Pos(),
		End:     ident.End(),
		NewText: []byte(newText),
	})
	name := analysisinternal.Format(a.pass.Fset, ident)
	a.pass.Report(analysis.Diagnostic{
		Pos:     ident.Pos(),
		End:     ident.End(),
		Message: fmt.Sprintf("%s %s should be inlined", capKind, name),
		SuggestedFixes: []analysis.SuggestedFix{{
			Message:   fmt.Sprintf("Inline %s %s", kind, name),
			TextEdits: edits,
		}},
	})
}

func (a *analyzer) readFile(node ast.Node) ([]byte, error) {
	filename := a.pass.Fset.File(node.Pos()).Name()
	content, ok := a.fileContent[filename]
	if !ok {
		var err error
		content, err = a.pass.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		a.fileContent[filename] = content
	}
	return content, nil
}

// currentFile returns the unique ast.File for a cursor.
func currentFile(c inspector.Cursor) *ast.File {
	for cf := range c.Enclosing((*ast.File)(nil)) {
		return cf.Node().(*ast.File)
	}
	panic("no *ast.File enclosing a cursor: impossible")
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
// It holds no information; its mere existence demonstrates that an alias is inlinable.
type goFixInlineAliasFact struct{}

func (c *goFixInlineAliasFact) String() string { return "goFixInline alias" }
func (*goFixInlineAliasFact) AFact()           {}

func discard(string, ...any) {}

type list[T any] interface {
	Len() int
	At(int) T
}

// TODO(adonovan): eliminate in favor of go/types@go1.24 iterators.
func listIter[L list[T], T any](lst L) iter.Seq[T] {
	return func(yield func(T) bool) {
		for i := range lst.Len() {
			if !yield(lst.At(i)) {
				return
			}
		}
	}
}
