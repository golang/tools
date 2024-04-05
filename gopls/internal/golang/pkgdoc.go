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
// - abbreviate long signatures by replacing parameters 4 onwards with "...".
// - style the <li> bullets in the index as invisible.
// - add push notifications such as didChange -> reload.
// - there appears to be a maximum file size beyond which the
//   "source.doc" code action is not offered. Remove that.
// - modify JS httpGET function to give a transient visual indication
//   when clicking a source link that the editor is being navigated
//   (in case it doesn't raise itself, like VS Code).

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
	"log"
	"path/filepath"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/gopls/internal/util/slices"
	"golang.org/x/tools/gopls/internal/util/typesutil"
	"golang.org/x/tools/internal/typesinternal"
)

// RenderPackageDoc formats the package documentation page.
//
// The posURL function returns a URL that when visited, has the side
// effect of causing gopls to direct the client editor to navigate to
// the specified file/line/column position, in UTF-8 coordinates.
//
// The pkgURL function returns a URL for the documentation of the
// specified package and symbol.
func RenderPackageDoc(pkg *cache.Package, posURL func(filename string, line, col8 int) protocol.URI, pkgURL func(path PackagePath, fragment string) protocol.URI) ([]byte, error) {
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
		fileMap[pkg.FileSet().File(f.Pos()).Name()] = f
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

	var docHTML func(comment string) []byte
	{
		// Adapt doc comment parser and printer
		// to our representation of Go packages
		// so that doc links (e.g. "[fmt.Println]")
		// become valid links.

		printer := docpkg.Printer()
		printer.DocLinkURL = func(link *comment.DocLink) string {
			path := pkg.Metadata().PkgPath
			if link.ImportPath != "" {
				path = PackagePath(link.ImportPath)
			}
			fragment := link.Name
			if link.Recv != "" {
				fragment = link.Recv + "." + link.Name
			}
			return pkgURL(path, fragment)
		}
		parser := docpkg.Parser()
		parser.LookupPackage = func(name string) (importPath string, ok bool) {
			// Ambiguous: different files in the same
			// package may have different import mappings,
			// but the hook doesn't provide the file context.
			// TODO(adonovan): conspire with docHTML to
			// pass the doc comment's enclosing file through
			// a shared variable, so that we can compute
			// the correct per-file mapping.
			//
			// TODO(adonovan): check for PkgName.Name
			// matches, but also check for
			// PkgName.Imported.Namer matches, since some
			// packages are typically imported under a
			// non-default name (e.g. pathpkg "path") but
			// may be referred to in doc links using their
			// canonical name.
			for _, f := range pkg.Syntax() {
				for _, imp := range f.Imports {
					pkgName, ok := typesutil.ImportedPkgName(pkg.TypesInfo(), imp)
					if ok && pkgName.Name() == name {
						return pkgName.Imported().Path(), true
					}
				}
			}
			return "", false
		}
		parser.LookupSym = func(recv, name string) (ok bool) {
			defer func() {
				log.Printf("LookupSym %q %q = %t ", recv, name, ok)
			}()
			// package-level decl?
			if recv == "" {
				return pkg.Types().Scope().Lookup(name) != nil
			}

			// method?
			tname, ok := pkg.Types().Scope().Lookup(recv).(*types.TypeName)
			if !ok {
				return false
			}
			m, _, _ := types.LookupFieldOrMethod(tname.Type(), true, pkg.Types(), name)
			return is[*types.Func](m)
		}
		docHTML = func(comment string) []byte {
			return printer.HTML(parser.Parse(comment))
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
  <style>` + pkgDocStyle + `</style>
  <title>` + title + `</title>
  <script type='text/javascript'>
// httpGET requests a URL for its effects only.
function httpGET(url) {
	var xhttp = new XMLHttpRequest();
	xhttp.open("GET", url, true);
	xhttp.send();
	return false; // disable usual <a href=...> behavior
}

window.onload = () => {
	// Hook up the navigation selector.
	document.getElementById('hdr-Selector').onchange = (e) => {
		window.location.href = e.target.value;
	};
};

// Start a GET /hang request. If it ever completes, the server
// has disconnected. Show a banner in that case.
{
	var x = new XMLHttpRequest();
	x.open("GET", "/hang", true);
	x.onloadend = () => {
		document.getElementById("disconnected").style.display = 'block';
	};
	x.send();
};
  </script>
</head>
<body>
<header>
<div id='disconnected'>Gopls server has terminated. Page is inactive.</div>
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
			sig := fn.Type().(*types.Signature)
			if sig.Recv() != nil {
				fmt.Fprintf(&buf, "(%s) ", sig.Recv().Name())
				fragment = recvType + "." + fn.Name()
			}
			fmt.Fprintf(&buf, "%s(", fn.Name())
			for i := 0; i < sig.Params().Len(); i++ {
				if i > 0 {
					buf.WriteString(", ")
				}
				buf.WriteString(sig.Params().At(i).Name())
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

	// sourceLink returns HTML for a link to open a file in the client editor.
	sourceLink := func(text, url string) string {
		// The /open URL returns nothing but has the side effect
		// of causing the LSP client to open the requested file.
		// So we use onclick to prevent the browser from navigating.
		// We keep the href attribute as it causes the <a> to render
		// as a link: blue, underlined, with URL hover information.
		return fmt.Sprintf(`<a href="%[1]s" onclick='return httpGET("%[1]s")'>%[2]s</a>`,
			escape(url), text)
	}

	// objHTML returns HTML for obj.Name(), possibly as a link.
	objHTML := func(obj types.Object) string {
		text := obj.Name()
		if posn := safetoken.StartPosition(pkg.FileSet(), obj.Pos()); posn.IsValid() {
			return sourceLink(text, posURL(posn.Filename, posn.Line, posn.Column))
		}
		return text
	}

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
					return pkgURL(PackagePath(pkgname.Imported().Path()), "")
				}

				// package-level symbol?
				if obj.Parent() == obj.Pkg().Scope() {
					if obj.Pkg() == pkg.Types() {
						return "#" + obj.Name() // intra-package ref
					} else {
						return pkgURL(PackagePath(obj.Pkg().Path()), obj.Name())
					}
				}

				// method of package-level named type?
				if fn, ok := obj.(*types.Func); ok {
					sig := fn.Type().(*types.Signature)
					if sig.Recv() != nil {
						_, named := typesinternal.ReceiverNamed(sig.Recv())
						if named != nil {
							fragment := named.Obj().Name() + "." + fn.Name()
							return pkgURL(PackagePath(fn.Pkg().Path()), fragment)
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
			if astutil.NodeContains(file.File, n.Pos()) {
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

	// pkgRelative qualifies types by package name alone
	pkgRelative := func(other *types.Package) string {
		if pkg.Types() == other {
			return "" // same package; unqualified
		}
		return other.Name()
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
	fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n", docHTML(docpkg.Doc))

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
		// TODO(adonovan): show only 3 parameters; elide 4+. Ditto for methods.
		fmt.Fprintf(&buf, "<li><a href='#%s'>%s</a></li>\n",
			obj.Name(),
			escape(types.ObjectString(obj, pkgRelative)))
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
					docfn.Name,
					escape(types.ObjectString(obj, pkgRelative)))
			}
			// methods
			for _, docmethod := range doctype.Methods {
				method, _, _ := types.LookupFieldOrMethod(tname.Type(), true, tname.Pkg(), docmethod.Name)
				// TODO(adonovan): style: change the . into a space in
				// ObjectString's "func (T).M()", and hide unexported
				// embedded types.
				fmt.Fprintf(&buf, "<li><a href='#%s.%s'>%s</a></li>\n",
					doctype.Name,
					docmethod.Name,
					escape(types.ObjectString(method, pkgRelative)))
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
			fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n", docHTML(v.Doc))
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

	// package-level functions
	fmt.Fprintf(&buf, "<h2 id='hdr-Functions'>Functions</h2>\n")
	// funcs emits a list of package-level functions,
	// possibly organized beneath the type they construct.
	funcs := func(funcs []*doc.Func) {
		for _, docfn := range funcs {
			obj := scope.Lookup(docfn.Name).(*types.Func)
			fmt.Fprintf(&buf, "<h3 id='%s'>func %s</h3>\n",
				docfn.Name, objHTML(obj))

			// decl: func F(params) results
			fmt.Fprintf(&buf, "<pre class='code'>%s</pre>\n",
				nodeHTML(docfn.Decl.Type))

			// comment (if any)
			fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n", docHTML(docfn.Doc))
		}
	}
	funcs(docpkg.Funcs)

	// types and their subelements
	fmt.Fprintf(&buf, "<h2 id='hdr-Types'>Types</h2>\n")
	for _, doctype := range docpkg.Types {
		tname := scope.Lookup(doctype.Name).(*types.TypeName)

		// title and source link
		fmt.Fprintf(&buf, "<h3 id='%s'>type %s</a></h3>\n", doctype.Name, objHTML(tname))

		// declaration
		// TODO(adonovan): excise non-exported struct fields somehow.
		decl2 := *doctype.Decl // shallow copy
		decl2.Doc = nil
		fmt.Fprintf(&buf, "<pre class='code'>%s</pre>\n", nodeHTML(&decl2))

		// comment (if any)
		fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n", docHTML(doctype.Doc))

		// subelements
		values(doctype.Consts) // constants of type T
		values(doctype.Vars)   // vars of type T
		funcs(doctype.Funcs)   // constructors of T

		// methods on T
		for _, docmethod := range doctype.Methods {
			method, _, _ := types.LookupFieldOrMethod(tname.Type(), true, tname.Pkg(), docmethod.Name)
			fmt.Fprintf(&buf, "<h4 id='%s.%s'>func (%s) %s</h4>\n",
				doctype.Name, docmethod.Name,
				doctype.Name, objHTML(method))

			// decl: func (x T) M(params) results
			fmt.Fprintf(&buf, "<pre class='code'>%s</pre>\n",
				nodeHTML(docmethod.Decl.Type))

			// comment (if any)
			fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n",
				docHTML(docmethod.Doc))
		}
	}

	// source files
	fmt.Fprintf(&buf, "<h2 id='hdr-SourceFiles'>Source files</h2>\n")
	for _, filename := range docpkg.Filenames {
		fmt.Fprintf(&buf, "<div class='comment'>%s</div>\n",
			sourceLink(filepath.Base(filename), posURL(filename, 1, 1)))
	}

	fmt.Fprintf(&buf, "</main>\n")
	fmt.Fprintf(&buf, "</body>\n")
	fmt.Fprintf(&buf, "</html>\n")

	return buf.Bytes(), nil
}

// (partly taken from pkgsite's typography.css)
const pkgDocStyle = `
body {
  font-family: Helvetica, Arial, sans-serif;
  font-size: 1rem;
  line-height: normal;
}

h1 {
  font-size: 1.5rem;
}

h2 {
  font-size: 1.375rem;
}

h3 {
  font-size: 1.25rem;
}

h4 {
  font-size: 1.125rem;
}

h5 {
  font-size: 1rem;
}

h6 {
  font-size: 0.875rem;
}

h1,
h2,
h3,
h4 {
  font-weight: 600;
  line-height: 1.25em;
  word-break: break-word;
}

h5,
h6 {
  font-weight: 500;
  line-height: 1.3em;
  word-break: break-word;
}

p {
  font-size: 1rem;
  line-height: 1.5rem;
  max-width: 60rem;
}

strong {
  font-weight: 600;
}

code,
pre,
textarea.code {
  font-family: Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 0.875rem;
  line-height: 1.5em;
}

pre,
textarea.code {
  background-color: #eee;
  border: 3px;
  border-radius: 3px
  color: black;
  overflow-x: auto;
  padding: 0.625rem;
  tab-size: 4;
  white-space: pre;
}

button,
input,
select,
textarea {
  font: inherit;
}

a,
a:link,
a:visited {
  color: rgb(0, 125, 156);
  text-decoration: none;
}

a:hover,
a:focus {
  color: rgb(0, 125, 156);
  text-decoration: underline;
}

a:hover > * {
  text-decoration: underline;
}

.lit { color: darkgreen; }

#pkgsite { height: 1.5em; }

header {
  position: sticky;
  top: 0;
  left: 0;
  width: 100%;
  padding: 0.3em;
}

#hdr-Selector {
  margin-right: 0.3em;
  float: right;
  min-width: 25em;
  padding: 0.3em;
}

#disconnected {
  position: fixed;
  top: 1em;
  left: 1em;
  display: none; /* initially */
  background-color: white;
  border: thick solid red;
  padding: 2em;
}
`
