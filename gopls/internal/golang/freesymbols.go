// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file implements the "Browse free symbols" code action.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"html"
	"slices"
	"sort"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/typesinternal"
)

// FreeSymbolsHTML returns an HTML document containing the report of
// free symbols referenced by the selection.
func FreeSymbolsHTML(viewID string, pkg *cache.Package, pgf *parsego.File, start, end token.Pos, web Web) []byte {

	// Compute free references.
	refs := freeRefs(pkg.Types(), pkg.TypesInfo(), pgf.File, start, end)

	// -- model --

	type Import struct {
		Path    metadata.PackagePath
		Symbols []string
	}
	type Symbol struct {
		Kind string
		Type string
		Refs []types.Object
	}
	var model struct {
		Imported []Import
		PkgLevel []Symbol
		Local    []Symbol
	}

	qualifier := typesinternal.NameRelativeTo(pkg.Types())

	// Populate model.
	{
		// List the refs in order of dotted paths.
		sort.Slice(refs, func(i, j int) bool {
			return refs[i].dotted < refs[j].dotted
		})

		// Inspect the references.
		imported := make(map[string][]*freeRef) // refs to imported symbols, by package path
		seen := make(map[string]bool)           // to de-dup dotted paths
		for _, ref := range refs {
			if seen[ref.dotted] {
				continue // de-dup
			}
			seen[ref.dotted] = true

			var symbols *[]Symbol
			switch ref.scope {
			case "file":
				// imported symbol: group by package
				if pkgname, ok := ref.objects[0].(*types.PkgName); ok {
					path := pkgname.Imported().Path()
					imported[path] = append(imported[path], ref)
				}
				continue
			case "pkg":
				symbols = &model.PkgLevel
			case "local":
				symbols = &model.Local
			default:
				panic(ref.scope)
			}

			// Package and local symbols are presented the same way.
			// We treat each dotted path x.y.z as a separate entity.

			// Compute kind and type of last object (y in obj.x.y).
			typestr := " " + types.TypeString(ref.typ, qualifier)
			var kind string
			switch obj := ref.objects[len(ref.objects)-1].(type) {
			case *types.Var:
				kind = "var"
			case *types.Func:
				kind = "func"
			case *types.TypeName:
				if is[*types.TypeParam](obj.Type()) {
					kind = "type parameter"
				} else {
					kind = "type"
				}
				typestr = "" // avoid "type T T"
			case *types.Const:
				kind = "const"
			case *types.Label:
				kind = "label"
				typestr = "" // avoid "label L L"
			}

			*symbols = append(*symbols, Symbol{
				Kind: kind,
				Type: typestr,
				Refs: ref.objects,
			})
		}

		// Imported symbols.
		// Produce one record per package, with a list of symbols.
		for pkgPath, refs := range moremaps.Sorted(imported) {
			var syms []string
			for _, ref := range refs {
				// strip package name (bytes.Buffer.Len -> Buffer.Len)
				syms = append(syms, ref.dotted[len(ref.objects[0].Name())+len("."):])
			}
			sort.Strings(syms)
			const max = 4
			if len(syms) > max {
				syms[max-1] = fmt.Sprintf("... (%d)", len(syms))
				syms = syms[:max]
			}

			model.Imported = append(model.Imported, Import{
				Path:    PackagePath(pkgPath),
				Symbols: syms,
			})
		}
	}

	// -- presentation --

	var buf bytes.Buffer
	buf.WriteString(`<!DOCTYPE html>
<html>
<head>
<style>
.col-pkg { color: #2eb007 }
.col-file { color: #a10b15 }
.col-local { color: #0cb7c9 }
li { font-family: monospace; }
p { max-width: 6in; }
</style>
  <script src="/assets/common.js"></script>
  <link rel="stylesheet" href="/assets/common.css">
</head>
<body>
<h1>Free symbols</h1>
<p>
  The selected code contains references to these free* symbols:
</p>
`)

	// Present the refs in three sections: imported, same package, local.

	// -- imported symbols --

	// Show one item per package, with a list of symbols.
	fmt.Fprintf(&buf, "<h2><span class='col-file'>⬤</span> Imported symbols</h2>\n")
	fmt.Fprintf(&buf, "<ul>\n")
	for _, imp := range model.Imported {
		fmt.Fprintf(&buf, "<li>import \"<a href='%s'>%s</a>\" // for %s</li>\n",
			web.PkgURL(viewID, imp.Path, ""),
			html.EscapeString(string(imp.Path)),
			strings.Join(imp.Symbols, ", "))
	}
	if len(model.Imported) == 0 {
		fmt.Fprintf(&buf, "<li>(none)</li>\n")
	}
	buf.WriteString("</ul>\n")

	// -- package and local symbols --

	showSymbols := func(scope, title string, symbols []Symbol) {
		fmt.Fprintf(&buf, "<h2><span class='col-%s'>⬤</span> %s</h2>\n", scope, title)
		fmt.Fprintf(&buf, "<ul>\n")
		pre := buf.Len()
		for _, sym := range symbols {
			fmt.Fprintf(&buf, "<li>%s ", sym.Kind) // of rightmost symbol in dotted path
			for i, obj := range sym.Refs {
				if i > 0 {
					buf.WriteByte('.')
				}
				buf.WriteString(objHTML(pkg.FileSet(), web, obj))
			}
			fmt.Fprintf(&buf, " %s</li>\n", html.EscapeString(sym.Type))
		}
		if buf.Len() == pre {
			fmt.Fprintf(&buf, "<li>(none)</li>\n")
		}
		buf.WriteString("</ul>\n")
	}
	showSymbols("pkg", "Package-level symbols", model.PkgLevel)
	showSymbols("local", "Local symbols", model.Local)

	// -- code selection --

	// Print the selection, highlighting references to free symbols.
	buf.WriteString("<hr/>\n")
	sort.Slice(refs, func(i, j int) bool {
		return refs[i].expr.Pos() < refs[j].expr.Pos()
	})
	pos := start
	emitTo := func(end token.Pos) {
		if pos < end {
			fileStart := pgf.File.FileStart
			text := pgf.Mapper.Content[pos-fileStart : end-fileStart]
			buf.WriteString(html.EscapeString(string(text)))
			pos = end
		}
	}
	buf.WriteString(`<pre>`)
	for _, ref := range refs {
		emitTo(ref.expr.Pos())
		fmt.Fprintf(&buf, `<b class='col-%s'>`, ref.scope)
		emitTo(ref.expr.End())
		buf.WriteString(`</b>`)
	}
	emitTo(end)
	buf.WriteString(`</pre>
<hr>
<p>
  *A symbol is "free" if it is referenced within the selection but declared
  outside of it.

  The free variables are approximately the set of parameters that
  would be needed if the block were extracted into its own function in
  the same package.

  Free identifiers may include local types and control labels as well.

  Even when you don't intend to extract a block into a new function,
  this information can help you to tell at a glance what names a block
  of code depends on.
</p>
<p>
  Each dotted path of identifiers (such as file.Name.Pos) is reported
  as a separate item, so that you can see which parts of a complex
  type are actually needed.

  The free symbols referenced by the body of a function may
  reveal that only a small part (a single field of a struct, say) of
  one of the function's parameters is used, allowing you to simplify
  and generalize the function by choosing a different type for that
  parameter.
</p>
`)
	return buf.Bytes()
}

// A freeRef records a reference to a dotted path obj.x.y,
// where obj (=objects[0]) is a free symbol.
type freeRef struct {
	objects []types.Object // [obj x y]
	dotted  string         // "obj.x.y"  (used as sort key)
	scope   string         // scope of obj: pkg|file|local
	expr    ast.Expr       // =*Ident|*SelectorExpr
	typ     types.Type     // type of obj.x.y
}

// freeRefs returns the list of references to free symbols (from
// within the selection to a symbol declared outside of it).
// It uses only info.{Scopes,Types,Uses}.
func freeRefs(pkg *types.Package, info *types.Info, file *ast.File, start, end token.Pos) []*freeRef {
	// Keep us honest about which fields we access.
	info = &types.Info{
		Scopes: info.Scopes,
		Types:  info.Types,
		Uses:   info.Uses,
	}

	fileScope := info.Scopes[file]
	pkgScope := fileScope.Parent()

	// id is called for the leftmost id x in each dotted chain such as (x.y).z.
	// suffix is the reversed suffix of selections (e.g. [z y]).
	id := func(n *ast.Ident, suffix []types.Object) *freeRef {
		obj := info.Uses[n]
		if obj == nil {
			return nil // not a reference
		}
		if start <= obj.Pos() && obj.Pos() < end {
			return nil // defined within selection => not free
		}
		parent := obj.Parent()

		// Compute dotted path.
		objects := append(suffix, obj)
		if obj.Pkg() != nil && obj.Pkg() != pkg && typesinternal.IsPackageLevel(obj) { // dot import
			// Synthesize the implicit PkgName.
			pkgName := types.NewPkgName(token.NoPos, pkg, obj.Pkg().Name(), obj.Pkg())
			parent = fileScope
			objects = append(objects, pkgName)
		}
		slices.Reverse(objects)
		var dotted strings.Builder
		for i, obj := range objects {
			if obj == nil {
				return nil // type error
			}
			if i > 0 {
				dotted.WriteByte('.')
			}
			dotted.WriteString(obj.Name())
		}

		// Compute scope of base object.
		var scope string
		switch parent {
		case nil:
			return nil // interface method or struct field
		case types.Universe:
			return nil // built-in (not interesting)
		case fileScope:
			scope = "file" // defined at file scope (imported package)
		case pkgScope:
			scope = "pkg" // defined at package level
		default:
			scope = "local" // defined within current function
		}

		return &freeRef{
			objects: objects,
			dotted:  dotted.String(),
			scope:   scope,
		}
	}

	// sel(x.y.z, []) calls sel(x.y, [z]) calls id(x, [z, y]).
	sel := func(sel *ast.SelectorExpr, suffix []types.Object) *freeRef {
		for {
			suffix = append(suffix, info.Uses[sel.Sel])

			switch x := ast.Unparen(sel.X).(type) {
			case *ast.Ident:
				return id(x, suffix)
			default:
				return nil
			case *ast.SelectorExpr:
				sel = x
			}
		}
	}

	// Visit all the identifiers in the selected ASTs.
	var free []*freeRef
	path, _ := astutil.PathEnclosingInterval(file, start, end)
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		// Is this node contained within the selection?
		// (freesymbols permits inexact selections,
		// like two stmts in a block.)
		if n != nil && start <= n.Pos() && n.End() <= end {
			var ref *freeRef
			switch n := n.(type) {
			case *ast.Ident:
				ref = id(n, nil)
			case *ast.SelectorExpr:
				ref = sel(n, nil)
			}

			if ref != nil {
				ref.expr = n.(ast.Expr)
				if tv, ok := info.Types[ref.expr]; ok {
					ref.typ = tv.Type
				} else {
					ref.typ = types.Typ[types.Invalid]
				}
				free = append(free, ref)
			}

			// After visiting x.sel, don't descend into sel.
			// Descend into x only if we didn't get a ref for x.sel.
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if ref == nil {
					ast.Inspect(sel.X, visit)
				}
				return false
			}
		}

		return true // descend
	}
	ast.Inspect(path[0], visit)
	return free
}

// objHTML returns HTML for obj.Name(), possibly marked up as a link
// to the web server that, when visited, opens the declaration in the
// client editor.
func objHTML(fset *token.FileSet, web Web, obj types.Object) string {
	text := obj.Name()
	if posn := safetoken.StartPosition(fset, obj.Pos()); posn.IsValid() {
		url := web.SrcURL(posn.Filename, posn.Line, posn.Column)
		return sourceLink(text, url)
	}
	return text
}

// sourceLink returns HTML for a link to open a file in the client editor.
func sourceLink(text, url string) string {
	// The /src URL returns nothing but has the side effect
	// of causing the LSP client to open the requested file.
	// So we use onclick to prevent the browser from navigating.
	// We keep the href attribute as it causes the <a> to render
	// as a link: blue, underlined, with URL hover information.
	return fmt.Sprintf(`<a href="%[1]s" onclick='return httpGET("%[1]s")'>%[2]s</a>`,
		html.EscapeString(url), text)
}
