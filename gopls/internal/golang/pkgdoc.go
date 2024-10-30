// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file defines a simple HTML rendering of package documentation
// in imitation of the style of pkg.go.dev.
//
// The current implementation is just a starting point and a
// placeholder for a more sophisticated one.
//
// TODO(adonovan):
// - rewrite using html/template.
//   Or factor with golang.org/x/pkgsite/internal/godoc/dochtml.
// - emit breadcrumbs for parent + sibling packages.
// - list promoted methods---we have type information!
// - gather Example tests, following go/doc and pkgsite.
// - add option for doc.AllDecls: show non-exported symbols too.
// - style the <li> bullets in the index as invisible.
// - add push notifications such as didChange -> reload.
// - there appears to be a maximum file size beyond which the
//   "source.doc" code action is not offered. Remove that.
// - modify JS httpGET function to give a transient visual indication
//   when clicking a source link that the editor is being navigated
//   (in case it doesn't raise itself, like VS Code).
// - move this into a new package, golang/web, and then
//   split out the various helpers without fear of polluting
//   the golang package namespace?
// - show "Deprecated" chip when appropriate.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/doc"
	"go/doc/comment"
	"go/format"
	"go/token"
	"go/types"
	"html"
	"iter"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	goplsastutil "golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/stdlib"
	"golang.org/x/tools/internal/typesinternal"
)

// DocFragment finds the package and (optionally) symbol identified by
// the current selection, and returns the package path and the
// optional symbol URL fragment (e.g. "#Buffer.Len") for a symbol,
// along with a title for the code action.
//
// It is called once to offer the code action, and again when the
// command is executed. This is slightly inefficient but ensures that
// the title and package/symbol logic are consistent in all cases.
//
// It returns zeroes if there is nothing to see here (e.g. reference to a builtin).
func DocFragment(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (pkgpath PackagePath, fragment, title string) {
	thing := thingAtPoint(pkg, pgf, start, end)

	makeTitle := func(kind string, imp *types.Package, name string) string {
		title := "Browse documentation for " + kind + " "
		if imp != nil && imp != pkg.Types() {
			title += imp.Name() + "."
		}
		return title + name
	}

	wholePackage := func(pkg *types.Package) (PackagePath, string, string) {
		// External test packages don't have /pkg doc pages,
		// so instead show the doc for the package under test.
		// (This named-based heuristic is imperfect.)
		if forTest := strings.TrimSuffix(pkg.Path(), "_test"); forTest != pkg.Path() {
			return PackagePath(forTest), "", makeTitle("package", nil, filepath.Base(forTest))
		}

		return PackagePath(pkg.Path()), "", makeTitle("package", nil, pkg.Name())
	}

	// Conceptually, we check cases in the order:
	// 1. symbol
	// 2. package
	// 3. enclosing
	// but the logic of cases 1 and 3 are identical, hence the odd factoring.

	// Imported package?
	if thing.pkg != nil && thing.symbol == nil {
		return wholePackage(thing.pkg)
	}

	// Symbol?
	var sym types.Object
	if thing.symbol != nil {
		sym = thing.symbol // reference to a symbol
	} else if thing.enclosing != nil {
		sym = thing.enclosing // selection is within a declaration of a symbol
	}
	if sym == nil {
		return wholePackage(pkg.Types()) // no symbol
	}

	// Built-in (error.Error, append or unsafe).
	// TODO(adonovan): handle builtins in /pkg viewer.
	if sym.Pkg() == nil {
		return "", "", "" // nothing to see here
	}
	pkgpath = PackagePath(sym.Pkg().Path())

	// Unexported? Show enclosing type or package.
	if !sym.Exported() {
		// Unexported method of exported type?
		if fn, ok := sym.(*types.Func); ok {
			if recv := fn.Signature().Recv(); recv != nil {
				_, named := typesinternal.ReceiverNamed(recv)
				if named != nil && named.Obj().Exported() {
					sym = named.Obj()
					goto below
				}
			}
		}

		return wholePackage(sym.Pkg())
	below:
	}

	// Reference to symbol in external test package?
	// Short-circuit: see comment in wholePackage.
	if strings.HasSuffix(string(pkgpath), "_test") {
		return wholePackage(pkg.Types())
	}

	// package-level symbol?
	if isPackageLevel(sym) {
		return pkgpath, sym.Name(), makeTitle(objectKind(sym), sym.Pkg(), sym.Name())
	}

	// Inv: sym is field or method, or local.
	switch sym := sym.(type) {
	case *types.Func: // => method
		sig := sym.Signature()
		isPtr, named := typesinternal.ReceiverNamed(sig.Recv())
		if named != nil {
			if !named.Obj().Exported() {
				return wholePackage(sym.Pkg()) // exported method of unexported type
			}
			name := fmt.Sprintf("(%s%s).%s",
				strings.Repeat("*", btoi(isPtr)), // for *T
				named.Obj().Name(),
				sym.Name())
			fragment := named.Obj().Name() + "." + sym.Name()
			return pkgpath, fragment, makeTitle("method", sym.Pkg(), name)
		}

	case *types.Var:
		if sym.IsField() {
			// TODO(adonovan): support fields.
			// The Var symbol doesn't include the struct
			// type, so we need to use the logic from
			// Hover. (This isn't important for
			// DocFragment as fields don't have fragments,
			// but it matters to the grand unification of
			// Hover/Definition/DocFragment.
		}
	}

	// Field, non-exported method, or local declaration:
	// just show current package.
	return wholePackage(pkg.Types())
}

// thing describes the package or symbol denoted by a selection.
//
// TODO(adonovan): Hover, Definition, and References all start by
// identifying the selected object. Let's achieve a better factoring
// of the common parts using this structure, including uniform
// treatment of doc links, linkname, and suchlike.
type thing struct {
	// At most one of these fields is set.
	// (The 'enclosing' field is a fallback for when neither
	// of the first two is set.)
	symbol    types.Object   // referenced symbol
	pkg       *types.Package // referenced package
	enclosing types.Object   // package-level symbol or method decl enclosing selection
}

func thingAtPoint(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) thing {
	path, _ := astutil.PathEnclosingInterval(pgf.File, start, end)

	// In an import spec?
	if len(path) >= 3 { // [...ImportSpec GenDecl File]
		if spec, ok := path[len(path)-3].(*ast.ImportSpec); ok {
			if pkgname := pkg.TypesInfo().PkgNameOf(spec); pkgname != nil {
				return thing{pkg: pkgname.Imported()}
			}
		}
	}

	// Definition or reference to symbol?
	var obj types.Object
	if id, ok := path[0].(*ast.Ident); ok {
		obj = pkg.TypesInfo().ObjectOf(id)

		// Treat use to PkgName like ImportSpec.
		if pkgname, ok := obj.(*types.PkgName); ok {
			return thing{pkg: pkgname.Imported()}
		}

	} else if sel, ok := path[0].(*ast.SelectorExpr); ok {
		// e.g. selection is "fmt.Println" or just a portion ("mt.Prin")
		obj = pkg.TypesInfo().Uses[sel.Sel]
	}
	if obj != nil {
		return thing{symbol: obj}
	}

	// Find enclosing declaration.
	if n := len(path); n > 1 {
		switch decl := path[n-2].(type) {
		case *ast.FuncDecl:
			// method?
			if fn := pkg.TypesInfo().Defs[decl.Name]; fn != nil {
				return thing{enclosing: fn}
			}

		case *ast.GenDecl:
			// path=[... Spec? GenDecl File]
			for _, spec := range decl.Specs {
				if n > 2 && spec == path[n-3] {
					var name *ast.Ident
					switch spec := spec.(type) {
					case *ast.ValueSpec:
						// var, const: use first name
						name = spec.Names[0]
					case *ast.TypeSpec:
						name = spec.Name
					}
					if name != nil {
						return thing{enclosing: pkg.TypesInfo().Defs[name]}
					}
					break
				}
			}
		}
	}

	return thing{} // nothing to see here
}

// Web is an abstraction of gopls' web server.
type Web interface {
	// PkgURL forms URLs of package or symbol documentation.
	PkgURL(viewID string, path PackagePath, fragment string) protocol.URI

	// SrcURL forms URLs that cause the editor to open a file at a specific position.
	SrcURL(filename string, line, col8 int) protocol.URI
}

// PackageDocHTML formats the package documentation page.
//
// The posURL function returns a URL that when visited, has the side
// effect of causing gopls to direct the client editor to navigate to
// the specified file/line/column position, in UTF-8 coordinates.
func PackageDocHTML(viewID string, pkg *cache.Package, web Web) ([]byte, error) {
	// We can't use doc.NewFromFiles (even with doc.PreserveAST
	// mode) as it calls ast.NewPackage which assumes that each
	// ast.File has an ast.Scope and resolves identifiers to
	// (deprecated) ast.Objects. (This is golang/go#66290.)
	// But doc.New only requires pkg.{Name,Files},
	// so we just boil it down.
	//
	// The only loss is doc.classifyExamples.
	// TODO(adonovan): simulate that too.
	fileMap := make(map[string]*ast.File)
	for _, f := range pkg.Syntax() {
		fileMap[pkg.FileSet().File(f.FileStart).Name()] = f
	}
	astpkg := &ast.Package{
		Name:  pkg.Types().Name(),
		Files: fileMap,
	}
	// PreserveAST mode only half works (golang/go#66449): it still
	// mutates ASTs when filtering out non-exported symbols.
	// As a workaround, enable AllDecls to suppress filtering,
	// and do it ourselves.
	mode := doc.PreserveAST | doc.AllDecls
	docpkg := doc.New(astpkg, pkg.Types().Path(), mode)

	// Discard non-exported symbols.
	// TODO(adonovan): do this conditionally, and expose option in UI.
	const showUnexported = false
	if !showUnexported {
		var (
			unexported   = func(name string) bool { return !token.IsExported(name) }
			filterValues = func(slice *[]*doc.Value) {
				delValue := func(v *doc.Value) bool {
					v.Names = slices.DeleteFunc(v.Names, unexported)
					return len(v.Names) == 0
				}
				*slice = slices.DeleteFunc(*slice, delValue)
			}
			filterFuncs = func(funcs *[]*doc.Func) {
				*funcs = slices.DeleteFunc(*funcs, func(v *doc.Func) bool {
					return unexported(v.Name)
				})
			}
		)
		filterValues(&docpkg.Consts)
		filterValues(&docpkg.Vars)
		filterFuncs(&docpkg.Funcs)
		docpkg.Types = slices.DeleteFunc(docpkg.Types, func(t *doc.Type) bool {
			filterValues(&t.Consts)
			filterValues(&t.Vars)
			filterFuncs(&t.Funcs)
			filterFuncs(&t.Methods)
			return unexported(t.Name)
		})
	}

	// docHTML renders the doc comment as Markdown.
	// The fileNode is used to deduce the enclosing file
	// for the correct import mapping.
	//
	// It is not concurrency-safe.
	var docHTML func(fileNode ast.Node, comment string) []byte
	{
		// Adapt doc comment parser and printer
		// to our representation of Go packages
		// so that doc links (e.g. "[fmt.Println]")
		// become valid links.
		printer := &comment.Printer{
			DocLinkURL: func(link *comment.DocLink) string {
				path := pkg.Metadata().PkgPath
				if link.ImportPath != "" {
					path = PackagePath(link.ImportPath)
				}
				fragment := link.Name
				if link.Recv != "" {
					fragment = link.Recv + "." + link.Name
				}
				return web.PkgURL(viewID, path, fragment)
			},
		}
		parse := newDocCommentParser(pkg)
		docHTML = func(fileNode ast.Node, comment string) []byte {
			doc := parse(fileNode, comment)
			return printer.HTML(doc)
		}
	}

	scope := pkg.Types().Scope()
	escape := html.EscapeString

	title := fmt.Sprintf("%s package - %s - Gopls packages",
		pkg.Types().Name(), escape(pkg.Types().Path()))

	var buf bytes.Buffer
	buf.WriteString(`<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>` + title + `</title>
  <link rel="stylesheet" href="/assets/common.css">
  <script src="/assets/common.js"></script>
  <style>
.lit { color: darkgreen; }

header {
  position: sticky;
  top: 0;
  left: 0;
  width: 100%;
  padding: 0.3em;
}

.Documentation-sinceVersion {
  font-weight: normal;
  color: #808080;
  float: right;
}

#pkgsite { height: 1.5em; }

#hdr-Selector {
  margin-right: 0.3em;
  float: right;
  min-width: 25em;
  padding: 0.3em;
}
  </style>
  <script type='text/javascript'>
window.addEventListener('load', function() {
	// Hook up the navigation selector.
	document.getElementById('hdr-Selector').onchange = (e) => {
		window.location.href = e.target.value;
	};
});
  </script>
</head>
<body>
<header>
<select id='hdr-Selector'>
<optgroup label="Documentation">
  <option label="Overview" value="#hdr-Overview"/>
  <option label="Index" value="#hdr-Index"/>
  <option label="Constants" value="#hdr-Constants"/>
  <option label="Variables" value="#hdr-Variables"/>
  <option label="Functions" value="#hdr-Functions"/>
  <option label="Types" value="#hdr-Types"/>
  <option label="Source Files" value="#hdr-SourceFiles"/>
</optgroup>
`)

	// -- header select element --

	// option emits an <option> for the specified symbol.
	//
	// recvType is the apparent receiver type, which may
	// differ from ReceiverNamed(obj.Signature.Recv).Name
	// for promoted methods.
	option := func(obj types.Object, recvType string) {
		// Render functions/methods as "(recv) Method(p1, ..., pN)".
		fragment := obj.Name()

		// format parameter names (p1, ..., pN)
		label := obj.Name() // for a type
		if fn, ok := obj.(*types.Func); ok {
			var buf strings.Builder
			sig := fn.Signature()
			if sig.Recv() != nil {
				fmt.Fprintf(&buf, "(%s) ", sig.Recv().Name())
				fragment = recvType + "." + fn.Name()
			}
			fmt.Fprintf(&buf, "%s(", fn.Name())
			for i := 0; i < sig.Params().Len(); i++ {
				if i > 0 {
					buf.WriteString(", ")
				}
				name := sig.Params().At(i).Name()
				if name == "" {
					name = "_"
				}
				buf.WriteString(name)
			}
			buf.WriteByte(')')
			label = buf.String()
		}

		fmt.Fprintf(&buf, "  <option label='%s' value='#%s'/>\n", label, fragment)
	}

	// index of functions
	fmt.Fprintf(&buf, "<optgroup label='Functions'>\n")
	for _, fn := range docpkg.Funcs {
		option(scope.Lookup(fn.Name), "")
	}
	fmt.Fprintf(&buf, "</optgroup>\n")

	// index of types
	fmt.Fprintf(&buf, "<optgroup label='Types'>\n")
	for _, doctype := range docpkg.Types {
		option(scope.Lookup(doctype.Name), "")
	}
	fmt.Fprintf(&buf, "</optgroup>\n")

	// index of constructors and methods of each type
	for _, doctype := range docpkg.Types {
		tname := scope.Lookup(doctype.Name).(*types.TypeName)
		if len(doctype.Funcs)+len(doctype.Methods) > 0 {
			fmt.Fprintf(&buf, "<optgroup label='type %s'>\n", doctype.Name)
			for _, docfn := range doctype.Funcs {
				option(scope.Lookup(docfn.Name), "")
			}
			for _, docmethod := range doctype.Methods {
				method, _, _ := types.LookupFieldOrMethod(tname.Type(), true, tname.Pkg(), docmethod.Name)
				option(method, doctype.Name)
			}
			fmt.Fprintf(&buf, "</optgroup>\n")
		}
	}
	fmt.Fprintf(&buf, "</select>\n")
	fmt.Fprintf(&buf, "</header>\n")

	// -- main element --

	// nodeHTML returns HTML markup for a syntax tree.
	// It replaces referring identifiers with links,
	// and adds style spans for strings and comments.
	nodeHTML := func(n ast.Node) string {

		// linkify returns the appropriate URL (if any) for an identifier.
		linkify := func(id *ast.Ident) protocol.URI {
			if obj, ok := pkg.TypesInfo().Uses[id]; ok && obj.Pkg() != nil {
				// imported package name?
				if pkgname, ok := obj.(*types.PkgName); ok {
					// TODO(adonovan): do this for Defs of PkgName too.
					return web.PkgURL(viewID, PackagePath(pkgname.Imported().Path()), "")
				}

				// package-level symbol?
				if obj.Parent() == obj.Pkg().Scope() {
					if obj.Pkg() == pkg.Types() {
						return "#" + obj.Name() // intra-package ref
					} else {
						return web.PkgURL(viewID, PackagePath(obj.Pkg().Path()), obj.Name())
					}
				}

				// method of package-level named type?
				if fn, ok := obj.(*types.Func); ok {
					sig := fn.Signature()
					if sig.Recv() != nil {
						_, named := typesinternal.ReceiverNamed(sig.Recv())
						if named != nil {
							fragment := named.Obj().Name() + "." + fn.Name()
							return web.PkgURL(viewID, PackagePath(fn.Pkg().Path()), fragment)
						}
					}
					return ""
				}

				// TODO(adonovan): field of package-level named struct type.
				// (Requires an index, since there's no way to
				// get from Var to Named.)
			}
			return ""
		}

		// Splice spans into HTML-escaped segments of the
		// original source buffer (which is usually but not
		// necessarily formatted).
		//
		// (For expedience we don't use the more sophisticated
		// approach taken by cmd/godoc and pkgsite's render
		// package, which emit the text, spans, and comments
		// in one traversal of the syntax tree.)
		//
		// TODO(adonovan): splice styled spans around comments too.
		//
		// TODO(adonovan): pkgsite prints specs from grouped
		// type decls like "type ( T1; T2 )" to make them
		// appear as separate decls. We should too.
		var buf bytes.Buffer
		for _, file := range pkg.CompiledGoFiles() {
			if goplsastutil.NodeContains(file.File, n.Pos()) {
				pos := n.Pos()

				// emit emits source in the interval [pos:to] and updates pos.
				emit := func(to token.Pos) {
					// Ident and BasicLit always have a valid pos.
					// (Failure means the AST has been corrupted.)
					if !to.IsValid() {
						bug.Reportf("invalid Pos")
					}
					start, err := safetoken.Offset(file.Tok, pos)
					if err != nil {
						bug.Reportf("invalid start Pos: %v", err)
					}
					end, err := safetoken.Offset(file.Tok, to)
					if err != nil {
						bug.Reportf("invalid end Pos: %v", err)
					}
					buf.WriteString(escape(string(file.Src[start:end])))
					pos = to
				}
				ast.Inspect(n, func(n ast.Node) bool {
					switch n := n.(type) {
					case *ast.Ident:
						emit(n.Pos())
						pos = n.End()
						if url := linkify(n); url != "" {
							fmt.Fprintf(&buf, "<a class='id' href='%s'>%s</a>", url, escape(n.Name))
						} else {
							buf.WriteString(escape(n.Name)) // plain
						}

					case *ast.BasicLit:
						emit(n.Pos())
						pos = n.End()
						fmt.Fprintf(&buf, "<span class='lit'>%s</span>", escape(n.Value))
					}
					return true
				})
				emit(n.End())
				return buf.String()
			}
		}

		// Original source not found.
		// Format the node without adornments.
		if err := format.Node(&buf, pkg.FileSet(), n); err != nil {
			// e.g. BadDecl?
			buf.Reset()
			fmt.Fprintf(&buf, "formatting error: %v", err)
		}
		return escape(buf.String())
	}

	// fnString is like fn.String() except that it:
	// - shows the receiver name;
	// - uses space "(T) M()" not dot "(T).M()" after receiver;
	// - doesn't bother with the special case for interface receivers
	//   since it is unreachable for the methods in go/doc.
	// - elides parameters after the first three: f(a, b, c, ...).
	fnString := func(fn *types.Func) string {
		pkgRelative := typesinternal.NameRelativeTo(pkg.Types())

		sig := fn.Signature()

		// Emit "func (recv T) F".
		var buf bytes.Buffer
		buf.WriteString("func ")
		if recv := sig.Recv(); recv != nil {
			buf.WriteByte('(')
			if recv.Name() != "" {
				buf.WriteString(recv.Name())
				buf.WriteByte(' ')
			}
			types.WriteType(&buf, recv.Type(), pkgRelative)
			buf.WriteByte(')')
			buf.WriteByte(' ') // (ObjectString uses a '.' here)
		} else if pkg := fn.Pkg(); pkg != nil {
			if s := pkgRelative(pkg); s != "" {
				buf.WriteString(s)
				buf.WriteByte('.')
			}
		}
		buf.WriteString(fn.Name())

		// Emit signature.
		//
		// Elide parameters after the third one.
		// WriteSignature is too complex to fork, so we replace
		// parameters 4+ with "invalid type", format,
		// then post-process the string.
		if sig.Params().Len() > 3 {

			// Clone each TypeParam as NewSignatureType modifies them (#67294).
			cloneTparams := func(seq *types.TypeParamList) []*types.TypeParam {
				slice := make([]*types.TypeParam, seq.Len())
				for i := range slice {
					tparam := seq.At(i)
					slice[i] = types.NewTypeParam(tparam.Obj(), tparam.Constraint())
				}
				return slice
			}

			sig = types.NewSignatureType(
				sig.Recv(),
				cloneTparams(sig.RecvTypeParams()),
				cloneTparams(sig.TypeParams()),
				types.NewTuple(append(
					slices.Collect(tupleVariables(sig.Params()))[:3],
					types.NewVar(0, nil, "", types.Typ[types.Invalid]))...),
				sig.Results(),
				false) // any final ...T parameter is truncated
		}
		types.WriteSignature(&buf, sig, pkgRelative)
		return strings.ReplaceAll(buf.String(), ", invalid type)", ", ...)")
	}

	fmt.Fprintf(&buf, "<main>\n")

	// package name
	fmt.Fprintf(&buf, "<h1 id='hdr-Overview'>Package %s</h1>\n", pkg.Types().Name())

	// import path
	fmt.Fprintf(&buf, "<pre class='code'>import %q</pre>\n", pkg.Types().Path())

	// link to same package in pkg.go.dev
	fmt.Fprintf(&buf, "<div><a href=%q title='View in pkg.go.dev'><img id='pkgsite' src='/assets/go-logo-blue.svg'/></a>\n",
		"https://pkg.go.dev/"+string(pkg.Types().Path()))

	// package doc
	for _, f := range pkg.Syntax() {
		if f.Doc != nil {
			fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n", docHTML(f.Doc, docpkg.Doc))
			break
		}
	}

	// symbol index
	fmt.Fprintf(&buf, "<h2 id='hdr-Index'>Index</h2>\n")
	fmt.Fprintf(&buf, "<ul>\n")
	if len(docpkg.Consts) > 0 {
		fmt.Fprintf(&buf, "<li><a href='#hdr-Constants'>Constants</a></li>\n")
	}
	if len(docpkg.Vars) > 0 {
		fmt.Fprintf(&buf, "<li><a href='#hdr-Variables'>Variables</a></li>\n")
	}
	for _, fn := range docpkg.Funcs {
		obj := scope.Lookup(fn.Name).(*types.Func)
		fmt.Fprintf(&buf, "<li><a href='#%s'>%s</a></li>\n",
			obj.Name(), escape(fnString(obj)))
	}
	for _, doctype := range docpkg.Types {
		tname := scope.Lookup(doctype.Name).(*types.TypeName)
		fmt.Fprintf(&buf, "<li><a href='#%[1]s'>type %[1]s</a></li>\n",
			tname.Name())

		if len(doctype.Funcs)+len(doctype.Methods) > 0 {
			fmt.Fprintf(&buf, "<ul>\n")

			// constructors
			for _, docfn := range doctype.Funcs {
				obj := scope.Lookup(docfn.Name).(*types.Func)
				fmt.Fprintf(&buf, "<li><a href='#%s'>%s</a></li>\n",
					docfn.Name, escape(fnString(obj)))
			}
			// methods
			for _, docmethod := range doctype.Methods {
				method, _, _ := types.LookupFieldOrMethod(tname.Type(), true, tname.Pkg(), docmethod.Name)
				fmt.Fprintf(&buf, "<li><a href='#%s.%s'>%s</a></li>\n",
					doctype.Name,
					docmethod.Name,
					escape(fnString(method.(*types.Func))))
			}
			fmt.Fprintf(&buf, "</ul>\n")
		}
	}
	// TODO(adonovan): add index of Examples here.
	fmt.Fprintf(&buf, "</ul>\n")

	// constants and variables
	values := func(vals []*doc.Value) {
		for _, v := range vals {
			// anchors
			for _, name := range v.Names {
				fmt.Fprintf(&buf, "<a id='%s'></a>\n", escape(name))
			}

			// declaration
			decl2 := *v.Decl // shallow copy
			decl2.Doc = nil
			fmt.Fprintf(&buf, "<pre class='code'>%s</pre>\n", nodeHTML(&decl2))

			// comment (if any)
			fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n", docHTML(v.Decl, v.Doc))
		}
	}
	fmt.Fprintf(&buf, "<h2 id='hdr-Constants'>Constants</h2>\n")
	if len(docpkg.Consts) == 0 {
		fmt.Fprintf(&buf, "<div>(no constants)</div>\n")
	} else {
		values(docpkg.Consts)
	}
	fmt.Fprintf(&buf, "<h2 id='hdr-Variables'>Variables</h2>\n")
	if len(docpkg.Vars) == 0 {
		fmt.Fprintf(&buf, "<div>(no variables)</div>\n")
	} else {
		values(docpkg.Vars)
	}

	// addedInHTML returns an HTML division containing the Go release version at
	// which this obj became available.
	addedInHTML := func(obj types.Object) string {
		if sym := StdSymbolOf(obj); sym != nil && sym.Version != stdlib.Version(0) {
			return fmt.Sprintf("<span class='Documentation-sinceVersion'>added in %v</span>", sym.Version)
		}
		return ""
	}

	// package-level functions
	fmt.Fprintf(&buf, "<h2 id='hdr-Functions'>Functions</h2>\n")
	// funcs emits a list of package-level functions,
	// possibly organized beneath the type they construct.
	funcs := func(funcs []*doc.Func) {
		for _, docfn := range funcs {
			obj := scope.Lookup(docfn.Name).(*types.Func)

			fmt.Fprintf(&buf, "<h3 id='%s'>func %s %s</h3>\n",
				docfn.Name, objHTML(pkg.FileSet(), web, obj), addedInHTML(obj))

			// decl: func F(params) results
			fmt.Fprintf(&buf, "<pre class='code'>%s</pre>\n",
				nodeHTML(docfn.Decl.Type))

			// comment (if any)
			fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n", docHTML(docfn.Decl, docfn.Doc))
		}
	}
	funcs(docpkg.Funcs)

	// types and their subelements
	fmt.Fprintf(&buf, "<h2 id='hdr-Types'>Types</h2>\n")
	for _, doctype := range docpkg.Types {
		tname := scope.Lookup(doctype.Name).(*types.TypeName)

		// title and source link
		fmt.Fprintf(&buf, "<h3 id='%s'>type %s %s</h3>\n",
			doctype.Name, objHTML(pkg.FileSet(), web, tname), addedInHTML(tname))

		// declaration
		// TODO(adonovan): excise non-exported struct fields somehow.
		decl2 := *doctype.Decl // shallow copy
		decl2.Doc = nil
		fmt.Fprintf(&buf, "<pre class='code'>%s</pre>\n", nodeHTML(&decl2))

		// comment (if any)
		fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n", docHTML(doctype.Decl, doctype.Doc))

		// subelements
		values(doctype.Consts) // constants of type T
		values(doctype.Vars)   // vars of type T
		funcs(doctype.Funcs)   // constructors of T

		// methods on T
		for _, docmethod := range doctype.Methods {
			method, _, _ := types.LookupFieldOrMethod(tname.Type(), true, tname.Pkg(), docmethod.Name)
			fmt.Fprintf(&buf, "<h4 id='%s.%s'>func (%s) %s %s</h4>\n",
				doctype.Name, docmethod.Name,
				docmethod.Orig, // T or *T
				objHTML(pkg.FileSet(), web, method), addedInHTML(method))

			// decl: func (x T) M(params) results
			fmt.Fprintf(&buf, "<pre class='code'>%s</pre>\n",
				nodeHTML(docmethod.Decl.Type))

			// comment (if any)
			fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n",
				docHTML(docmethod.Decl, docmethod.Doc))
		}
	}

	// source files
	fmt.Fprintf(&buf, "<h2 id='hdr-SourceFiles'>Source files</h2>\n")
	for _, filename := range docpkg.Filenames {
		fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n",
			sourceLink(filepath.Base(filename), web.SrcURL(filename, 1, 1)))
	}

	fmt.Fprintf(&buf, "</main>\n")
	fmt.Fprintf(&buf, "</body>\n")
	fmt.Fprintf(&buf, "</html>\n")

	return buf.Bytes(), nil
}

// tupleVariables returns a go1.23 iterator over the variables of a tuple type.
//
// Example: for v := range tuple.Variables() { ... }
// TODO(adonovan): use t.Variables in go1.24.
func tupleVariables(t *types.Tuple) iter.Seq[*types.Var] {
	return func(yield func(v *types.Var) bool) {
		for i := range t.Len() {
			if !yield(t.At(i)) {
				break
			}
		}
	}
}
