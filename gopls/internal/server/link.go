// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/event"
	"mvdan.cc/xurls/v2"
)

func (s *server) DocumentLink(ctx context.Context, params *protocol.DocumentLinkParams) (links []protocol.DocumentLink, err error) {
	ctx, done := event.Start(ctx, "server.DocumentLink")
	defer done()

	fh, snapshot, release, err := s.session.FileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	switch snapshot.FileKind(fh) {
	case file.Mod:
		links, err = modLinks(ctx, snapshot, fh)
	case file.Go:
		links, err = goLinks(ctx, snapshot, fh)
	}
	// Don't return errors for document links.
	if err != nil {
		event.Error(ctx, "failed to compute document links", err, label.URI.Of(fh.URI()))
		return nil, nil // empty result
	}
	return links, nil // may be empty (for other file types)
}

func modLinks(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.DocumentLink, error) {
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil {
		return nil, err
	}

	var links []protocol.DocumentLink
	for _, rep := range pm.File.Replace {
		if modfile.IsDirectoryPath(rep.New.Path) {
			// Have local replacement, such as 'replace A => ../'.
			dep := []byte(rep.New.Path)
			start, end := rep.Syntax.Start.Byte, rep.Syntax.End.Byte
			i := bytes.Index(pm.Mapper.Content[start:end], dep)
			if i < 0 {
				continue
			}
			path := rep.New.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(fh.URI().DirPath(), path)
			}
			// jump to the go.mod file of replaced module.
			path = filepath.Join(filepath.Clean(path), "go.mod")
			l, err := toProtocolLink(pm.Mapper, protocol.URIFromPath(path).Path(), start+i, start+i+len(dep))
			if err != nil {
				return nil, err
			}
			links = append(links, l)
			continue
		}
	}

	for _, req := range pm.File.Require {
		if req.Syntax == nil {
			continue
		}
		// See golang/go#36998: don't link to modules matching GOPRIVATE.
		if snapshot.IsGoPrivatePath(req.Mod.Path) {
			continue
		}
		dep := []byte(req.Mod.Path)
		start, end := req.Syntax.Start.Byte, req.Syntax.End.Byte
		i := bytes.Index(pm.Mapper.Content[start:end], dep)
		if i == -1 {
			continue
		}

		mod := req.Mod
		// respect the replacement when constructing a module link.
		if m, ok := pm.ReplaceMap[req.Mod]; ok {
			// Have: 'replace A v1.2.3 => A vx.x.x' or 'replace A v1.2.3 => B vx.x.x'.
			mod = m
		} else if m, ok := pm.ReplaceMap[module.Version{Path: req.Mod.Path}]; ok &&
			!modfile.IsDirectoryPath(m.Path) { // exclude local replacement.
			// Have: 'replace A => A vx.x.x' or 'replace A => B vx.x.x'.
			mod = m
		}

		// Shift the start position to the location of the
		// dependency within the require statement.
		target := cache.BuildLink(snapshot.Options().LinkTarget, "mod/"+mod.String(), "")
		l, err := toProtocolLink(pm.Mapper, target, start+i, start+i+len(dep))
		if err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	// TODO(ridersofrohan): handle links for replace and exclude directives.
	if syntax := pm.File.Syntax; syntax == nil {
		return links, nil
	}

	// Get all the links that are contained in the comments of the file.
	urlRegexp := xurls.Relaxed()
	for _, expr := range pm.File.Syntax.Stmt {
		comments := expr.Comment()
		if comments == nil {
			continue
		}
		for _, section := range [][]modfile.Comment{comments.Before, comments.Suffix, comments.After} {
			for _, comment := range section {
				l, err := findLinksInString(urlRegexp, comment.Token, comment.Start.Byte, pm.Mapper)
				if err != nil {
					return nil, err
				}
				links = append(links, l...)
			}
		}
	}
	return links, nil
}

// goLinks returns the set of hyperlink annotations for the specified Go file.
func goLinks(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.DocumentLink, error) {

	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return nil, err
	}

	var links []protocol.DocumentLink

	// Create links for import specs.
	if snapshot.Options().ImportShortcut.ShowLinks() {

		// If links are to pkg.go.dev, append module version suffixes.
		// This requires the import map from the package metadata. Ignore errors.
		var depsByImpPath map[golang.ImportPath]golang.PackageID
		if strings.ToLower(snapshot.Options().LinkTarget) == "pkg.go.dev" {
			if meta, err := snapshot.NarrowestMetadataForFile(ctx, fh.URI()); err == nil {
				depsByImpPath = meta.DepsByImpPath
			}
		}

		for _, imp := range pgf.File.Imports {
			importPath := metadata.UnquoteImportPath(imp)
			if importPath == "" {
				continue // bad import
			}
			// See golang/go#36998: don't link to modules matching GOPRIVATE.
			if snapshot.IsGoPrivatePath(string(importPath)) {
				continue
			}

			urlPath := string(importPath)

			// For pkg.go.dev, append module version suffix to package import path.
			if mp := snapshot.Metadata(depsByImpPath[importPath]); mp != nil && mp.Module != nil && cache.ResolvedPath(mp.Module) != "" && cache.ResolvedVersion(mp.Module) != "" {
				urlPath = strings.Replace(urlPath, mp.Module.Path, cache.ResolvedString(mp.Module), 1)
			}

			start, end, err := safetoken.Offsets(pgf.Tok, imp.Path.Pos(), imp.Path.End())
			if err != nil {
				return nil, err
			}
			targetURL := cache.BuildLink(snapshot.Options().LinkTarget, urlPath, "")
			// Account for the quotation marks in the positions.
			l, err := toProtocolLink(pgf.Mapper, targetURL, start+len(`"`), end-len(`"`))
			if err != nil {
				return nil, err
			}
			links = append(links, l)
		}
	}

	urlRegexp := xurls.Relaxed()

	// Gather links found in string literals.
	var str []*ast.BasicLit
	for curLit := range pgf.Cursor.Preorder((*ast.BasicLit)(nil)) {
		lit := curLit.Node().(*ast.BasicLit)
		if lit.Kind == token.STRING {
			if _, ok := curLit.Parent().Node().(*ast.ImportSpec); ok {
				continue // ignore import strings
			}
			str = append(str, lit)
		}
	}
	for _, s := range str {
		strOffset, err := safetoken.Offset(pgf.Tok, s.Pos())
		if err != nil {
			return nil, err
		}
		l, err := findLinksInString(urlRegexp, s.Value, strOffset, pgf.Mapper)
		if err != nil {
			return nil, err
		}
		links = append(links, l...)
	}

	// Gather links found in comments.
	for _, commentGroup := range pgf.File.Comments {
		for _, comment := range commentGroup.List {
			commentOffset, err := safetoken.Offset(pgf.Tok, comment.Pos())
			if err != nil {
				return nil, err
			}
			l, err := findLinksInString(urlRegexp, comment.Text, commentOffset, pgf.Mapper)
			if err != nil {
				return nil, err
			}
			links = append(links, l...)
		}
	}

	return links, nil
}

// acceptedSchemes controls the schemes that URLs must have to be shown to the
// user. Other schemes can't be opened by LSP clients, so linkifying them is
// distracting. See golang/go#43990.
var acceptedSchemes = map[string]bool{
	"http":  true,
	"https": true,
}

// findLinksInString is the user-supplied regular expression to match URL.
// srcOffset is the start offset of 'src' within m's file.
func findLinksInString(urlRegexp *regexp.Regexp, src string, srcOffset int, m *protocol.Mapper) ([]protocol.DocumentLink, error) {
	var links []protocol.DocumentLink
	for _, index := range urlRegexp.FindAllIndex([]byte(src), -1) {
		start, end := index[0], index[1]
		link := src[start:end]
		linkURL, err := url.Parse(link)
		// Fallback: Linkify IP addresses as suggested in golang/go#18824.
		if err != nil {
			linkURL, err = url.Parse("//" + link)
			// Not all potential links will be valid, so don't return this error.
			if err != nil {
				continue
			}
		}
		// If the URL has no scheme, use https.
		if linkURL.Scheme == "" {
			linkURL.Scheme = "https"
		}
		if !acceptedSchemes[linkURL.Scheme] {
			continue
		}

		l, err := toProtocolLink(m, linkURL.String(), srcOffset+start, srcOffset+end)
		if err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	// Handle golang/go#1234-style links.
	r := getIssueRegexp()
	for _, index := range r.FindAllIndex([]byte(src), -1) {
		start, end := index[0], index[1]
		matches := r.FindStringSubmatch(src)
		if len(matches) < 4 {
			continue
		}
		org, repo, number := matches[1], matches[2], matches[3]
		targetURL := fmt.Sprintf("https://github.com/%s/%s/issues/%s", org, repo, number)
		l, err := toProtocolLink(m, targetURL, srcOffset+start, srcOffset+end)
		if err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, nil
}

func getIssueRegexp() *regexp.Regexp {
	once.Do(func() {
		issueRegexp = regexp.MustCompile(`(\w+)/([\w-]+)#([0-9]+)`)
	})
	return issueRegexp
}

var (
	once        sync.Once
	issueRegexp *regexp.Regexp
)

func toProtocolLink(m *protocol.Mapper, targetURL string, start, end int) (protocol.DocumentLink, error) {
	rng, err := m.OffsetRange(start, end)
	if err != nil {
		return protocol.DocumentLink{}, err
	}
	return protocol.DocumentLink{
		Range:  rng,
		Target: &targetURL,
	}, nil
}
