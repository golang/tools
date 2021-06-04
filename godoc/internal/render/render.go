// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package render formats Go documentation as HTML or text.
package render

import (
	"go/ast"
	"go/doc"
	"go/token"
	"html/template"
	"regexp"
	"strings"
)

// TODO: Hide slice elements and long strings to avoid overwhelming godoc.

var (
	// Regexp for headings.
	headingHead = `^[\p{Lu}]`                                  // any uppercase letter
	headingBody = `([^,.;:!?+*/=()\[\]{}_^°&§~%#@<">\\]|'s )*` // any non-illegal character
	headingTail = `([\p{L}\p{Nd}]|'s)?$`                       // any letter or digit

	headingRx = regexp.MustCompile(headingHead + headingBody + headingTail)

	// Regexp for example outputs.
	exampleOutputRx = regexp.MustCompile(`(?i)//[[:space:]]*(unordered )?output:`)
)

type Renderer struct {
	fset       *token.FileSet
	pids       *packageIDs
	packageURL func(string) string
}

type Options struct {
	// RelatedPackages is a list of related packages to use for hotlinking.
	// A recommended heuristic is to include all packages imported by the
	// given package, its tests, and its example tests.
	//
	// Only relevant for HTML formatting.
	RelatedPackages []*doc.Package

	// PackageURL is a function that given a package path,
	// returns a URL for navigating to the godoc for that package.
	//
	// Only relevant for HTML formatting.
	PackageURL func(pkgPath string) (url string)
}

func New(fset *token.FileSet, pkg *doc.Package, opts *Options) *Renderer {
	var others []*doc.Package
	var packageURL func(string) string
	if opts != nil {
		if len(opts.RelatedPackages) > 0 {
			others = opts.RelatedPackages
		}
		if opts.PackageURL != nil {
			packageURL = opts.PackageURL
		}
	}
	pids := newPackageIDs(pkg, others...)
	return &Renderer{fset: fset, pids: pids, packageURL: packageURL}
}

// Synopsis returns a one-line summary of the given input node.
func (r *Renderer) Synopsis(n ast.Node) string {
	const maxDepth = 10
	return oneLineNodeDepth(r.fset, n, maxDepth)
}

// DocHTML formats documentation text as HTML.
//
// Each span of unindented non-blank lines is converted into a single paragraph.
// There is one exception to the rule: a span that consists of a
// single line, is followed by another paragraph span, begins with a capital
// letter, and contains no punctuation is formatted as a heading.
//
// A span of indented lines is converted into a <pre> block, with the common
// indent prefix removed.
//
// URLs in the comment text are converted into links. Any word that matches
// an exported top-level identifier in the package is automatically converted
// into a hyperlink to the declaration of that identifier.
//
// This returns formatted HTML with:
//	<p>                elements for plain documentation text
//	<pre>              elements for preformatted text
//	<h3 id="hdr-XXX">  elements for headings with the "id" attribute
//	<a href="XXX">     elements for URL hyperlinks
//
// DocHTML is intended for documentation for the package and examples.
func (r *Renderer) DocHTML(doc string) template.HTML {
	return r.declHTML(doc, nil).Doc
}

// DeclHTML formats the doc and decl and returns a tuple of
// strings corresponding with each input argument.
//
// This formats documentation HTML according to the same rules as DocHTML.
//
// This format declaration HTML with:
//	<pre>                   element wrapping the entire declaration
//	<span id="XXX">         elements for every top-level declaration
//	<span class="comment">  elements for every Go comment
//	<a href="XXX">          elements for URL hyperlinks
//
// DeclHTML is intended for top-level package declarations.
func (r *Renderer) DeclHTML(doc string, decl ast.Decl) (out struct{ Doc, Decl template.HTML }) {
	// This returns an anonymous struct instead of multiple return values since
	// the template package only allows single return values.
	return r.declHTML(doc, decl)
}

// CodeHTML formats example code. If the code is a single block statement,
// the outer braces are stripped and the code unindented. If the example code
// contains an output comment, that will stripped as well.
//
// The code type must be *ast.File, *CommentedNode, []ast.Decl, []ast.Stmt
// or assignment-compatible to ast.Expr, ast.Decl, ast.Spec, or ast.Stmt.
//
// This returns formatted HTML with:
//	<pre>                   element wrapping entire block
//	<span class="comment">  elements for every Go comment
//
// CodeHTML is intended for use with example code snippets.
func (r *Renderer) CodeHTML(code interface{}) template.HTML {
	return r.codeHTML(code)
}

// block is (*heading | *paragraph | *preformat).
type block interface{}

type (
	lines   []string
	heading struct {
		title string
	}
	paragraph struct {
		lines lines
	}
	preformat struct {
		lines lines
	}
)

func docToBlocks(doc string) []block {
	docLines := unindent(strings.Split(strings.Trim(doc, "\n"), "\n"))

	// Group the lines based on indentation and blank lines.
	var groups [][]string
	var lastGroup []string
	var wasInCode bool
	for _, line := range docLines {
		hasIndent := indentLength(line) > 0
		nowInCode := hasIndent || (wasInCode && line == "")
		newGroup := wasInCode != nowInCode || (!nowInCode && line == "")
		wasInCode = nowInCode
		if newGroup && len(lastGroup) > 0 {
			groups = append(groups, lastGroup)
			lastGroup = nil
		}
		if line != "" || nowInCode {
			lastGroup = append(lastGroup, line)
		}
	}
	if len(lastGroup) > 0 {
		groups = append(groups, lastGroup)
	}

	// Classify groups of lines as individual blocks.
	var blks []block
	var lastBlk block
	for i, group := range groups {
		willParagraph := i+1 < len(groups) && indentLength(groups[i+1][0]) == 0
		for len(group) > 0 && group[len(group)-1] == "" {
			group = group[:len(group)-1] // remove trailing empty lines
		}
		_, wasHeading := lastBlk.(*heading)
		switch {
		case indentLength(group[0]) > 0:
			blks = append(blks, &preformat{unindent(group)})
		case !wasHeading && len(group) == 1 && headingRx.MatchString(group[0]) && willParagraph:
			blks = append(blks, &heading{group[0]})
		default:
			blks = append(blks, &paragraph{group})
		}
		lastBlk = blks[len(blks)-1]
	}
	return blks
}

func indentLength(s string) int {
	return len(s) - len(trimIndent(s))
}

func trimIndent(s string) string {
	return strings.TrimLeft(s, " \t")
}

func commonPrefixLength(a, b string) (n int) {
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func unindent(lines []string) []string {
	if len(lines) > 0 {
		npre := indentLength(lines[0])
		for _, line := range lines {
			if line != "" {
				npre = commonPrefixLength(lines[0][:npre], line)
			}
		}
		for i, line := range lines {
			if line != "" {
				lines[i] = line[npre:]
			}
		}
	}
	return lines
}
