// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// RenderDocs statically generates Go documentation pages.
//
// This is program is intended for testing purposes where static godoc pages
// can be generated for a large corpus of packages.
// After making a change to the render package, the packages can be regenerated
// in order to examine what differences were made.
//
// The standard library can be generated using:
//	go run render_docs.go -gopath= -output=/tmp/godoc
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"html/template"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/godoc/internal/render"
)

var (
	envGoRoot    = runtime.GOROOT()
	envGoPath, _ = os.LookupEnv("GOPATH")

	goRoot  = flag.String("goroot", envGoRoot, "GOROOT path")
	goPath  = flag.String("gopath", envGoPath, "GOPATH path")
	include = flag.String("include", ".*", "regular expression to filter in packages")
	exclude = flag.String("exclude", "(^|/)(cmd|testdata|internal|vendor)(/|$)", "regular expression to filter out packages")
	output  = flag.String("output", "", "output directory")
)

func main() {
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "output directory cannot be empty")
		os.Exit(1)
	}
	build.Default.GOROOT = *goRoot
	build.Default.GOPATH = *goPath
	includeRx := regexp.MustCompile(*include)
	excludeRx := regexp.MustCompile(*exclude)

	// Obtain the set of all packages.
	pkgSet := map[string]struct{}{}
	for _, p := range strings.Split(*goRoot+":"+*goPath, ":") {
		if p == "" {
			continue
		}
		base := filepath.Join(p, "src")
		filepath.Walk(base, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				log.Printf("unable to walk %s: %v", path, err)
				return nil
			}
			if strings.HasPrefix(fi.Name(), ".") && fi.IsDir() {
				return filepath.SkipDir
			}
			if strings.HasSuffix(path, ".go") && !fi.IsDir() {
				path = filepath.Dir(path)
				if pkg, err := filepath.Rel(base, path); err == nil {
					if includeRx.MatchString(pkg) && !excludeRx.MatchString(pkg) {
						pkgSet[pkg] = struct{}{}
					}
				}
			}
			return nil
		})
	}

	// Sort the set of packages.
	var pkgs []string
	for pkg := range pkgSet {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	// Load all of the packages.
	goPkgs := map[string]*goPackage{}
	loadCachedPackage := func(pkgPath string) (*goPackage, error) {
		if goPkg := goPkgs[pkgPath]; goPkg != nil {
			return goPkg, nil
		}
		goPkg, err := loadPackage(pkgPath)
		if err != nil {
			return nil, err
		}
		goPkgs[pkgPath] = goPkg
		return goPkg, nil
	}

	// Render HTML for all packages.
	if err := os.MkdirAll(*output, 0775); err != nil {
		log.Fatalf("os.MkdirAll error: %v", err)
	}
	for _, pkgPath := range pkgs {
		goPkg, err := loadCachedPackage(pkgPath)
		if err != nil {
			log.Printf("skipping package %q, unexpected error: %v", pkgPath, err)
			continue
		}

		// Use imported packages as a heuristic for related packages.
		var importPkgs []*doc.Package
		p := goPkg.build
		imports := append(append(append([]string(nil), p.Imports...), p.TestImports...), p.XTestImports...)
		sort.Strings(imports)
		for _, pkgPath := range imports {
			if len(importPkgs) == 0 || importPkgs[len(importPkgs)-1].ImportPath != pkgPath {
				if p, _ := loadCachedPackage(pkgPath); p != nil {
					importPkgs = append(importPkgs, p.doc)
				}
			}
		}

		b, err := renderPackage(goPkg, importPkgs)
		if err != nil {
			log.Fatalf("package %q, renderPackage error: %v", pkgPath, err)
		}
		absPath := filepath.Join(*output, goPkg.doc.ImportPath)
		if err := os.MkdirAll(absPath, 0775); err != nil {
			log.Fatalf("os.MkdirAll error: %v", err)
		}
		outPath := filepath.Join(absPath, "index.html")
		if err := ioutil.WriteFile(outPath, b, 0664); err != nil {
			log.Fatalf("ioutil.WriteFile error: %v", err)
		}
		log.Printf("rendered package %q", pkgPath)
	}

	// Write JS, CSS, and index files.
	jsPath := filepath.Join(*output, "code.js")
	if err := ioutil.WriteFile(jsPath, []byte(strings.TrimLeft(jsCode, "\n")), 0664); err != nil {
		log.Fatalf("ioutil.WriteFile error: %v", err)
	}
	cssPath := filepath.Join(*output, "style.css")
	if err := ioutil.WriteFile(cssPath, []byte(strings.TrimLeft(cssStyle, "\n")), 0664); err != nil {
		log.Fatalf("ioutil.WriteFile error: %v", err)
	}
	type pkgDesc struct{ Path, Desc string }
	var pkgDescs []pkgDesc
	for _, pkgPath := range pkgs {
		if goPkg, ok := goPkgs[pkgPath]; ok {
			pkgDescs = append(pkgDescs, pkgDesc{pkgPath, doc.Synopsis(goPkg.doc.Doc)})
		}
	}
	var b bytes.Buffer
	if err := htmlIndex.Execute(&b, pkgDescs); err != nil {
		log.Fatalf("template.Execute error: %v", err)
	}
	outPath := filepath.Join(*output, "index.html")
	if err := ioutil.WriteFile(outPath, b.Bytes(), 0664); err != nil {
		log.Fatalf("ioutil.WriteFile error: %v", err)
	}
}

func simpleImporter(imports map[string]*ast.Object, pkgPath string) (*ast.Object, error) {
	pkg := imports[pkgPath]
	if pkg == nil {
		pkgName := pkgPath[strings.LastIndex(pkgPath, "/")+1:]
		pkg = ast.NewObj(ast.Pkg, pkgName)
		pkg.Data = ast.NewScope(nil) // required by ast.NewPackage for dot-import
		imports[pkgPath] = pkg
	}
	return pkg, nil
}

type goPackage struct {
	fset     *token.FileSet
	doc      *doc.Package
	examples *examples
	build    *build.Package
}

func loadPackage(pkgPath string) (*goPackage, error) {
	buildPkg, err := build.Import(pkgPath, "", build.ImportComment)
	if err != nil {
		return nil, err
	}

	// Parse all source files.
	fset := token.NewFileSet()
	pkgFiles := make(map[string]*ast.File)
	for _, file := range append(buildPkg.GoFiles, buildPkg.CgoFiles...) {
		fname := filepath.Join(buildPkg.Dir, file)
		src, err := ioutil.ReadFile(fname)
		if err != nil {
			return nil, err
		}
		astFile, err := parser.ParseFile(fset, fname, src, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		pkgFiles[fname] = astFile
	}

	// Parse all test files.
	var testFiles []*ast.File
	for _, file := range append(buildPkg.TestGoFiles, buildPkg.XTestGoFiles...) {
		fname := filepath.Join(buildPkg.Dir, file)
		src, err := ioutil.ReadFile(fname)
		if err != nil {
			return nil, err
		}
		astFile, err := parser.ParseFile(fset, fname, src, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		testFiles = append(testFiles, astFile)
	}

	var mode doc.Mode
	if pkgPath == "builtin" {
		mode = doc.AllDecls
	}
	astPkg, _ := ast.NewPackage(fset, pkgFiles, simpleImporter, nil)
	docPkg := doc.New(astPkg, buildPkg.ImportPath, mode)
	exsPkg := newExamples(docPkg, testFiles)
	return &goPackage{fset, docPkg, exsPkg, buildPkg}, nil
}

func renderPackage(pkg *goPackage, relatedPkgs []*doc.Package) ([]byte, error) {
	var b bytes.Buffer
	r := render.New(pkg.fset, pkg.doc, &render.Options{
		RelatedPackages: relatedPkgs,
		PackageURL: func(path string) (url string) {
			return strings.Repeat("../", strings.Count(pkg.doc.ImportPath, "/")+1) + path + "/index.html"
		},
	})

	rootURL := strings.Repeat("../", strings.Count(pkg.doc.ImportPath, "/")+1)
	err := template.Must(htmlPackage.Clone()).Funcs(map[string]interface{}{
		"render_synopsis": r.Synopsis,
		"render_doc":      r.DocHTML,
		"render_decl":     r.DeclHTML,
		"render_code":     r.CodeHTML,
	}).Execute(&b, struct {
		RootURL string
		*doc.Package
		Examples *examples
	}{rootURL, pkg.doc, pkg.examples})
	return b.Bytes(), err
}

type examples struct {
	List []*example            // sorted by Name
	Map  map[string][]*example // keyed by top-level ID
}

type example struct {
	*doc.Example
	ParentID string // ID of top-level declaration this example is attached to
	Suffix   string // optional suffix name
}

// Code returns an printer.CommentedNode if ex.Comments is non-nil,
// otherwise it returns ex.Code as is.
func (ex *example) Code() interface{} {
	if len(ex.Comments) > 0 {
		return &printer.CommentedNode{Node: ex.Example.Code, Comments: ex.Comments}
	}
	return ex.Example.Code
}

func newExamples(pkg *doc.Package, files []*ast.File) *examples {
	// TODO: This logic should be in a package that is an abstraction on go/doc.

	// Mapping of IDs for funcs, types, and methods.
	ids := make(map[string]string)
	for _, f := range pkg.Funcs {
		ids[f.Name] = f.Name
	}
	for _, t := range pkg.Types {
		ids[t.Name] = t.Name
		for _, f := range t.Funcs {
			ids[f.Name] = f.Name
		}
		for _, m := range t.Methods {
			id := strings.TrimPrefix(m.Orig, "*") + "." + m.Name
			ids[strings.Replace(id, ".", "_", -1)] = id
		}
	}

	exs := &examples{nil, make(map[string][]*example)}
	for _, ex := range doc.Examples(files...) {
		name, suffix := ex.Name, ""
		if i := strings.LastIndex(name, "_"); i >= 0 {
			r, _ := utf8.DecodeRuneInString(name[i+1:])
			if !unicode.IsUpper(r) {
				name, suffix = name[:i], name[i+1:]
				suffix = strings.Title(suffix)
			}
		}
		if id := ids[name]; name == "" || id != "" {
			ex := &example{ex, id, suffix}
			exs.List = append(exs.List, ex)
			exs.Map[id] = append(exs.Map[id], ex)
			continue
		}
		// Unable to associate example; so ignore it.
	}
	sort.SliceStable(exs.List, func(i, j int) bool {
		return exs.List[i].ParentID < exs.List[j].ParentID
	})
	return exs
}

const jsCode = `
function showHidden(id) {
	if (id.startsWith("example-")) {
		elem = document.getElementById(id);
		elem = elem.getElementsByClassName("example-body")[0];
		elem.style.display = 'block';
		return false;
	}
	return true;
}
function toggleHidden(id) {
	if (id.startsWith("example-")) {
		elem = document.getElementById(id);
		elem = elem.getElementsByClassName("example-body")[0];
		elem.style.display = elem.style.display === 'block' ? 'none' : 'block';
		return false;
	}
	return true;
}

window.onhashchange = function () {
	showHidden(window.location.hash.slice(1));
}
if (window.location.hash != "") {
	showHidden(window.location.hash.slice(1));
}
`

const cssStyle = `
body {
	font-family: "Helvetica Neue", Helvetica, Arial, sans-serif;
	font-size: 14px;
	margin: 0px;
}
nav.navbar {
	position: relative;
	background-color: #e0ebf5;
	border-bottom: solid 1px #d1e1f0;
	margin-bottom: 20px;
}
div.navbutton {
	padding-top: 10px;
	padding-bottom: 10px;
	font-size: 28px;
	font-weight: bold;
}
div.container {
	max-width: 750px;
	padding-right: 10px;
	padding-left: 10px;
	margin-right: auto;
	margin-left: auto;
}
h1, h2, h3 {
	font-weight: 500;
	margin-top: 20px;
	margin-bottom: 15px;
}
h1 { font-size: 30px; }
h2 { font-size: 24px; }
h3 { font-size: 20px; }

dl { line-height: 150%; }
dd { margin-left: 0; }

table  { text-align: left; }
td, tr { padding: 2px 10px 2px 0; font-size: 14px; }

pre, code {
	font-family: Menlo, Monaco, Consolas, "Courier New", monospace;
	font-size: 13px;
}
pre {
	background-color: #eee;
	border: solid 1px #ccc;
	border-radius: 5px;
	padding: 10px;
	margin: 15px 10px;
	overflow: auto;
	line-height: 140%;
}

p          { line-height: 150%; }
a          { color: #375eab; text-decoration: none; white-space: nowrap; }
a:hover    { border-bottom: solid 1px #375eab; }
p a, pre a { border-bottom: solid 1px #dae0ec; }

pre .comment         { color: #060; }
pre .comment a       { color: #130; border-bottom: solid 1px #bca; }
pre .comment a:hover { border-bottom: solid 1px #130; }

.indent { margin-left: 20px; }

.example {
	border: solid 1px #ccc;
	border-radius: 5px;
	margin: 10px 10px;
	box-shadow: 0px 1px 2px #eee;
}
.example-header   { padding: 10px; }
.example-body {
	display: none;
	border-top: solid 1px #ccc;
	padding: 0 10px 10px 10px;
}
`

var htmlPackage = template.Must(template.New("package").Funcs(
	map[string]interface{}{
		"ternary": func(q, a, b interface{}) interface{} {
			v := reflect.ValueOf(q)
			vz := reflect.New(v.Type()).Elem()
			if reflect.DeepEqual(v.Interface(), vz.Interface()) {
				return b
			}
			return a
		},
		"render_synopsis": (*render.Renderer)(nil).Synopsis,
		"render_doc":      (*render.Renderer)(nil).DocHTML,
		"render_decl":     (*render.Renderer)(nil).DeclHTML,
		"render_code":     (*render.Renderer)(nil).CodeHTML,
	},
).Parse(`{{- "" -}}
<html>
<head>
<meta charset="utf-8">
<title>{{.Name}} - GoDoc</title>
<link rel="stylesheet" href="{{.RootURL}}style.css">
</head>
<body>
<nav class="navbar">
	<div class="container"><div class="navbutton"><a href="{{.RootURL}}index.html">GoDoc</a></div></div>
</nav>
<div class="container">

{{- define "example" -}}
	{{- range . -}}
	<div id="example-{{.Name}}" class="example">{{"\n" -}}
		<div class="example-header">{{"\n" -}}
			{{- $suffix := ternary .Suffix (printf " (%s)" .Suffix) "" -}}
			<a href="#example-{{.Name}}" onclick="return toggleHidden('example-{{.Name}}')">Example{{$suffix}}</a>{{"\n" -}}
		</div>{{"\n" -}}
		<div class="example-body">{{"\n" -}}
			{{- if .Doc -}}{{render_doc .Doc}}{{"\n" -}}{{- end -}}
			<p>Code:</p>{{"\n" -}}
			{{render_code .Code}}{{"\n" -}}
			{{- if (or .Output .EmptyOutput) -}}
				<p>{{ternary .Unordered "Unordered output:" "Output:"}}</p>{{"\n" -}}
				<pre>{{"\n"}}{{.Output}}</pre>{{"\n" -}}
			{{- end -}}
		</div>{{"\n" -}}
	</div>{{"\n" -}}
	{{"\n"}}
	{{- end -}}
{{- end -}}

{{"\n"}}
<h1>Package {{.Name}}</h1>{{"\n" -}}
	<code class="indent">import "{{.ImportPath}}"</code>{{"\n" -}}
	<dl class="indent">{{"\n" -}}
	<dd><a href="#pkg-overview">Overview</a></dd>{{"\n" -}}
	{{- if or .Consts .Vars .Funcs .Types -}}
		<dd><a href="#pkg-index">Index</a></dd>{{"\n" -}}
	{{- end -}}
	{{- if .Examples.List -}}
		<dd><a href="#pkg-examples">Examples</a></dd>{{"\n" -}}
	{{- end -}}
	{{- if or .Consts .Vars .Funcs .Types -}}
		<dd><a href="#pkg-documentation">Documentation</a></dd>{{"\n" -}}
	{{- end -}}
	</dl>{{"\n" -}}

<h2 id="pkg-overview">Overview</h2>{{"\n\n" -}}
	{{render_doc .Doc}}{{"\n" -}}
	{{- template "example" (index $.Examples.Map "") -}}

{{- if or .Consts .Vars .Funcs .Types -}}
	<h2 id="pkg-index">Index</h2>{{"\n\n" -}}
	<dl class="indent">{{"\n" -}}
	{{- if .Consts -}}<dd><a href="#pkg-constants">Constants</a></dd>{{"\n"}}{{- end -}}
	{{- if .Vars -}}<dd><a href="#pkg-variables">Variables</a></dd>{{"\n"}}{{- end -}}
	{{- range .Funcs -}}<dd><a href="#{{.Name}}">{{render_synopsis .Decl}}</a></dd>{{"\n"}}{{- end -}}
	{{- range .Types -}}
		{{- $tname := .Name -}}
		<dd><a href="#{{$tname}}">type {{$tname}}</a></dd>{{"\n"}}
		{{- range .Funcs -}}
			<dd class="indent"><a href="#{{.Name}}">{{render_synopsis .Decl}}</a></dd>{{"\n"}}
		{{- end -}}
		{{- range .Methods -}}
			<dd class="indent"><a href="#{{$tname}}.{{.Name}}">{{render_synopsis .Decl}}</a></dd>{{"\n"}}
		{{- end -}}
	{{- end -}}
	</dl>{{"\n" -}}
	{{- if .Examples.List -}}
	<h3 id="pkg-examples">Examples</h3>{{"\n" -}}
		<dl class="indent">{{"\n" -}}
		{{- range .Examples.List -}}
			{{- $suffix := ternary .Suffix (printf " (%s)" .Suffix) "" -}}
			<dd><a href="#example-{{.Name}}">{{or .ParentID "Package"}}{{$suffix}}</a></dd>{{"\n" -}}
		{{- end -}}
		</dl>{{"\n" -}}
	{{- end -}}

	<h2 id="pkg-documentation">Documentation</h2>{{"\n\n"}}
	{{- if .Consts -}}<h3 id="pkg-constants">Constants</h3>{{"\n"}}{{- end -}}
	{{- range .Consts -}}
		{{- $out := render_decl .Doc .Decl -}}
		{{- $out.Decl -}}
		{{- $out.Doc -}}
		{{"\n"}}
	{{- end -}}

	{{- if .Vars -}}<h3 id="pkg-variables">Variables</h3>{{"\n"}}{{- end -}}
	{{- range .Vars -}}
		{{- $out := render_decl .Doc .Decl -}}
		{{- $out.Decl -}}
		{{- $out.Doc -}}
		{{"\n"}}
	{{- end -}}

	{{- range .Funcs -}}
		<h3 id="{{.Name}}">func {{.Name}}</h3>{{"\n"}}
		{{- $out := render_decl .Doc .Decl -}}
		{{- $out.Decl -}}
		{{- $out.Doc -}}
		{{"\n"}}
		{{- template "example" (index $.Examples.Map .Name) -}}
	{{- end -}}

	{{- range .Types -}}
		{{- $tname := .Name -}}
		<h3 id="{{.Name}}">type {{.Name}}</h3>{{"\n"}}
		{{- $out := render_decl .Doc .Decl -}}
		{{- $out.Decl -}}
		{{- $out.Doc -}}
		{{"\n"}}
		{{- template "example" (index $.Examples.Map .Name) -}}

		{{- range .Consts -}}
			{{- $out := render_decl .Doc .Decl -}}
			{{- $out.Decl -}}
			{{- $out.Doc -}}
			{{"\n"}}
		{{- end -}}

		{{- range .Vars -}}
			{{- $out := render_decl .Doc .Decl -}}
			{{- $out.Decl -}}
			{{- $out.Doc -}}
			{{"\n"}}
		{{- end -}}

		{{- range .Funcs -}}
			<h3 id="{{.Name}}">func {{.Name}}</h3>{{"\n"}}
			{{- $out := render_decl .Doc .Decl -}}
			{{- $out.Decl -}}
			{{- $out.Doc -}}
			{{"\n"}}
			{{- template "example" (index $.Examples.Map .Name) -}}
		{{- end -}}

		{{- range .Methods -}}
			{{- $name := (printf "%s.%s" $tname .Name) -}}
			<h3 id="{{$name}}">func {{$name}}</h3>{{"\n"}}
			{{- $out := render_decl .Doc .Decl -}}
			{{- $out.Decl -}}
			{{- $out.Doc -}}
			{{"\n"}}
			{{- template "example" (index $.Examples.Map $name) -}}
		{{- end -}}
	{{- end -}}
{{- end -}}
<script src="{{.RootURL}}code.js"></script>
</div>
</body>
</hmtl>
`))

var htmlIndex = template.Must(template.New("index").Parse(`{{- "" -}}
<html>
<head>
<meta charset="utf-8">
<title>GoDoc</title>
<link rel="stylesheet" href="style.css">
</head>
<body>
<nav class="navbar">
	<div class="container"><div class="navbutton"><a href="#">GoDoc</a></div></div>
</nav>
<div class="container">

<h1>Package Index</h1>

<table>
<tr><th>Package</th><th>Description</th></tr>
{{- range $ -}}
	<tr>{{"\n" -}}
	<td><a href="{{.Path}}/index.html">{{.Path}}</a></td>{{"\n" -}}
	<td>{{.Desc}}</td>{{"\n" -}}
	</tr>{{"\n" -}}
{{- end -}}
</table>

</div>
</body>
</hmtl>
`))
