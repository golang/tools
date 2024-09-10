// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/doc/comment"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

var errNoCommentReference = errors.New("no comment reference found")

// CommentToMarkdown converts comment text to formatted markdown.
// The comment was prepared by DocReader,
// so it is known not to have leading, trailing blank lines
// nor to have trailing spaces at the end of lines.
// The comment markers have already been removed.
func CommentToMarkdown(text string, options *settings.Options) string {
	var p comment.Parser
	doc := p.Parse(text)
	var pr comment.Printer
	// The default produces {#Hdr-...} tags for headings.
	// vscode displays thems, which is undesirable.
	// The godoc for comment.Printer says the tags
	// avoid a security problem.
	pr.HeadingID = func(*comment.Heading) string { return "" }
	pr.DocLinkURL = func(link *comment.DocLink) string {
		msg := fmt.Sprintf("https://%s/%s", options.LinkTarget, link.ImportPath)
		if link.Name != "" {
			msg += "#"
			if link.Recv != "" {
				msg += link.Recv + "."
			}
			msg += link.Name
		}
		return msg
	}
	easy := pr.Markdown(doc)
	return string(easy)
}

// docLinkDefinition finds the definition of the doc link in comments at pos.
// If there is no reference at pos, returns errNoCommentReference.
func docLinkDefinition(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, pos token.Pos) ([]protocol.Location, error) {
	obj, _, err := parseDocLink(pkg, pgf, pos)
	if err != nil {
		return nil, err
	}
	loc, err := mapPosition(ctx, pkg.FileSet(), snapshot, obj.Pos(), adjustedObjEnd(obj))
	if err != nil {
		return nil, err
	}
	return []protocol.Location{loc}, nil
}

// parseDocLink parses a doc link in a comment such as [fmt.Println]
// and returns the symbol at pos, along with the link's start position.
func parseDocLink(pkg *cache.Package, pgf *parsego.File, pos token.Pos) (types.Object, protocol.Range, error) {
	var comment *ast.Comment
	for _, cg := range pgf.File.Comments {
		for _, c := range cg.List {
			if c.Pos() <= pos && pos <= c.End() {
				comment = c
				break
			}
		}
		if comment != nil {
			break
		}
	}
	if comment == nil {
		return nil, protocol.Range{}, errNoCommentReference
	}

	// The canonical parsing algorithm is defined by go/doc/comment, but
	// unfortunately its API provides no way to reliably reconstruct the
	// position of each doc link from the parsed result.
	line := safetoken.Line(pgf.Tok, pos)
	var start, end token.Pos
	if pgf.Tok.LineStart(line) > comment.Pos() {
		start = pgf.Tok.LineStart(line)
	} else {
		start = comment.Pos()
	}
	if line < pgf.Tok.LineCount() && pgf.Tok.LineStart(line+1) < comment.End() {
		end = pgf.Tok.LineStart(line + 1)
	} else {
		end = comment.End()
	}

	offsetStart, offsetEnd, err := safetoken.Offsets(pgf.Tok, start, end)
	if err != nil {
		return nil, protocol.Range{}, err
	}

	text := string(pgf.Src[offsetStart:offsetEnd])
	lineOffset := int(pos - start)

	for _, idx := range docLinkRegex.FindAllStringSubmatchIndex(text, -1) {
		// The [idx[2], idx[3]) identifies the first submatch,
		// which is the reference name in the doc link (sans '*').
		// e.g. The "[fmt.Println]" reference name is "fmt.Println".
		if !(idx[2] <= lineOffset && lineOffset < idx[3]) {
			continue
		}
		p := lineOffset - idx[2]
		name := text[idx[2]:idx[3]]
		i := strings.LastIndexByte(name, '.')
		for i != -1 {
			if p > i {
				break
			}
			name = name[:i]
			i = strings.LastIndexByte(name, '.')
		}
		obj := lookupDocLinkSymbol(pkg, pgf, name)
		if obj == nil {
			return nil, protocol.Range{}, errNoCommentReference
		}
		namePos := start + token.Pos(idx[2]+i+1)
		rng, err := pgf.PosRange(namePos, namePos+token.Pos(len(obj.Name())))
		if err != nil {
			return nil, protocol.Range{}, err
		}
		return obj, rng, nil
	}

	return nil, protocol.Range{}, errNoCommentReference
}

// lookupDocLinkSymbol returns the symbol denoted by a doc link such
// as "fmt.Println" or "bytes.Buffer.Write" in the specified file.
func lookupDocLinkSymbol(pkg *cache.Package, pgf *parsego.File, name string) types.Object {
	scope := pkg.Types().Scope()

	prefix, suffix, _ := strings.Cut(name, ".")

	// Try treating the prefix as a package name,
	// allowing for non-renaming and renaming imports.
	fileScope := pkg.TypesInfo().Scopes[pgf.File]
	pkgname, ok := fileScope.Lookup(prefix).(*types.PkgName) // ok => prefix is imported name
	if !ok {
		// Handle renaming import, e.g.
		// [path.Join] after import pathpkg "path".
		// (Should we look at all files of the package?)
		for _, imp := range pgf.File.Imports {
			pkgname2 := pkg.TypesInfo().PkgNameOf(imp)
			if pkgname2 != nil && pkgname2.Imported().Name() == prefix {
				pkgname = pkgname2
				break
			}
		}
	}
	if pkgname != nil {
		scope = pkgname.Imported().Scope()
		if suffix == "" {
			return pkgname // not really a valid doc link
		}
		name = suffix
	}

	// TODO(adonovan): try searching the forward closure for packages
	// that define the symbol but are not directly imported;
	// see https://github.com/golang/go/issues/61677

	// Type.Method?
	recv, method, ok := strings.Cut(name, ".")
	if ok {
		obj, ok := scope.Lookup(recv).(*types.TypeName)
		if !ok {
			return nil
		}
		t, ok := obj.Type().(*types.Named)
		if !ok {
			return nil
		}
		for i := 0; i < t.NumMethods(); i++ {
			m := t.Method(i)
			if m.Name() == method {
				return m
			}
		}
		return nil
	}

	// package-level symbol
	return scope.Lookup(name)
}
