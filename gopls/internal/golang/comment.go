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
	"iter"
	pathpkg "path"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/astutil"
)

var errNoCommentReference = errors.New("no comment reference found")

// DocCommentToMarkdown converts the text of a [doc comment] to Markdown.
//
// TODO(adonovan): provide a package (or file imports) as context for
// proper rendering of doc links; see [newDocCommentParser] and golang/go#61677.
//
// [doc comment]: https://go.dev/doc/comment
func DocCommentToMarkdown(text string, options *settings.Options) string {
	var parser comment.Parser
	doc := parser.Parse(text)

	var printer comment.Printer
	// The default produces {#Hdr-...} tags for headings.
	// vscode displays thems, which is undesirable.
	// The godoc for comment.Printer says the tags
	// avoid a security problem.
	printer.HeadingID = func(*comment.Heading) string { return "" }
	printer.DocLinkURL = func(link *comment.DocLink) string {
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

	return string(printer.Markdown(doc))
}

// docLinkDefinition finds the definition of the doc link in comments at pos.
// If there is no reference at pos, returns errNoCommentReference.
//
// TODO(hxjiang): simplify the error handling.
func docLinkDefinition(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, pos token.Pos) ([]protocol.Location, error) {
	obj, _, err := resolveDocLink(pkg, pgf, astutil.RangeOf(pos, pos))
	if err != nil {
		return nil, err
	}
	loc, err := ObjectLocation(ctx, pkg.FileSet(), snapshot, obj)
	if err != nil {
		return nil, err
	}
	return []protocol.Location{loc}, nil
}

// resolveDocLink parses a doc link in a comment such as [fmt.Println]
// and returns the symbol at pos, along with the link's range.
func resolveDocLink(pkg *cache.Package, pgf *parsego.File, rng astutil.Range) (types.Object, protocol.Range, error) {
	var comment *ast.CommentGroup
	for _, c := range pgf.File.Comments {
		if astutil.NodeContains(c, rng) {
			comment = c
			break
		}
	}

	if comment == nil {
		return nil, protocol.Range{}, errNoCommentReference
	}

	for docLink := range commentDocLinks(comment) {
		if astutil.NodeContains(docLink.partRange, rng) {
			if obj := lookupDocLinkSymbol(pkg, pgf, docLink.nameText); obj != nil {
				rng, err := pgf.NodeRange(docLink.partRange)
				if err != nil {
					return nil, protocol.Range{}, err
				}
				return obj, rng, nil
			}
			break
		}
	}

	return nil, protocol.Range{}, errNoCommentReference
}

// A docLink holds the parsed information for a single resolution step of a
// documentation link. For a link like "[fmt.Scanner.Scan]", the parser will
// yield three docLink values, one for each progressively resolved part:
// "fmt", "fmt.Scanner", and "fmt.Scanner.Scan".
type docLink struct {
	// The text of the right-most component of the name for this step.
	// For the name "fmt.Scanner", this is "Scanner".
	partText  string
	partRange astutil.Range

	// The text of the fully-qualified symbol path resolved in this step.
	// For example, "fmt.Scanner". Used for symbol lookups.
	nameText string

	// The text of the entire, original doc link expression, including brackets.
	// For example, "[fmt.Scanner.Scan]".
	bracketText string
}

// commentDocLinks returns the sequence of Go doc links in the comment group.
// TODO(hxjiang): move to [parsego] package.
func commentDocLinks(cg *ast.CommentGroup) iter.Seq[docLink] {
	return func(yield func(docLink) bool) {
		for _, comment := range cg.List {
			// The canonical parsing algorithm is defined by go/doc/comment, but
			// unfortunately its API provides no way to reliably reconstruct the
			// position of each doc link from the parsed result.

			for _, idx := range docLinkRegex.FindAllStringSubmatchIndex(comment.Text, -1) {
				mstart, mend := idx[2], idx[3]

				// [bracketPos.Start, bracketPos.End) identifies the start and end
				// of the brackets. e.g. "[fmt.Scanner.Scan]".
				bracketText := comment.Text[mstart-1 : mend+1]

				// [mstart, mend) identifies the first submatch, which is the
				// reference name in the doc link (sans '*').
				// e.g. The "[fmt.Println]" reference name is "fmt.Println".
				match := comment.Text[mstart:mend]

				if strings.Contains(match, "\n") {
					continue
				}

				// [namePos.Start, namePos.End) identifies the start and end
				// position of a name. e.g. "fmt", "fmt.Scanner", "fmt.Scanner.Scan".
				var name string

				namePos := astutil.RangeOf(comment.Pos()+token.Pos(mstart), comment.Pos()+token.Pos(mstart-1))
				// [partPos.Start, partPos.End) identifies the start and end
				// position of a part. e.g. "fmt", "Scanner", "Scan".
				partPos := namePos
				for part := range strings.SplitSeq(match, ".") {
					if name != "" {
						name += "."
					}
					name += part

					partPos.Start = partPos.EndPos + token.Pos(len("."))  // Move start to the first char of next part
					partPos.EndPos = partPos.Start + token.Pos(len(part)) // Move end to next char of current part

					namePos.EndPos = partPos.EndPos

					if !yield(docLink{
						partRange:   partPos,
						partText:    part,
						nameText:    name,
						bracketText: bracketText,
					}) {
						return
					}
				}
			}
		}
	}
}

// lookupDocLinkSymbol returns the symbol denoted by a doc link such
// as "fmt.Println" or "bytes.Buffer.Write" in the specified file.
func lookupDocLinkSymbol(pkg *cache.Package, pgf *parsego.File, name string) types.Object {
	scope := pkg.Types().Scope()

	prefix, suffix, _ := strings.Cut(name, ".")

	// Try treating the prefix as a package name,
	// allowing for non-renaming and renaming imports.
	fileScope := pkg.TypesInfo().Scopes[pgf.File]
	if fileScope == nil {
		// As we learned in golang/go#69616, any file may not be Scopes!
		//  - A non-compiled Go file (such as unsafe.go) won't be in Scopes.
		//  - A (technically) compiled go file with the wrong package name won't be
		//    in Scopes, as it will be skipped by go/types.
		return nil
	}
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

	// Field or sel?
	recv, sel, ok := strings.Cut(name, ".")
	if ok {
		obj := scope.Lookup(recv) // package scope
		if obj == nil {
			obj = types.Universe.Lookup(recv)
		}
		obj, ok := obj.(*types.TypeName)
		if !ok {
			return nil
		}
		m, _, _ := types.LookupFieldOrMethod(obj.Type(), true, obj.Pkg(), sel)
		return m
	}

	if obj := scope.Lookup(name); obj != nil {
		return obj // package-level symbol
	}
	return types.Universe.Lookup(name) // built-in symbol
}

// newDocCommentParser returns a function that parses [doc comments],
// with context for Doc Links supplied by the specified package.
//
// Imported symbols are rendered using the import mapping for the file
// that encloses fileNode.
//
// The resulting function is not concurrency safe.
//
// See issue #61677 for how this might be generalized to support
// correct contextual parsing of doc comments in Hover too.
//
// [doc comment]: https://go.dev/doc/comment
func newDocCommentParser(pkg *cache.Package) func(fileNode ast.Node, text string) *comment.Doc {
	var currentFilePos token.Pos // pos whose enclosing file's import mapping should be used
	parser := &comment.Parser{
		LookupPackage: func(name string) (importPath string, ok bool) {
			for _, f := range pkg.Syntax() {
				// Different files in the same package have
				// different import mappings. Use the provided
				// syntax node to find the correct file.
				if astutil.NodeContainsPos(f, currentFilePos) {
					// First try each actual imported package name.
					for _, imp := range f.Imports {
						pkgName := pkg.TypesInfo().PkgNameOf(imp)
						if pkgName != nil && pkgName.Name() == name {
							return pkgName.Imported().Path(), true
						}
					}

					// Then try each imported package's declared name,
					// as some packages are typically imported under a
					// non-default name (e.g. pathpkg "path") but
					// may be referred to in doc links using their
					// canonical name.
					for _, imp := range f.Imports {
						pkgName := pkg.TypesInfo().PkgNameOf(imp)
						if pkgName != nil && pkgName.Imported().Name() == name {
							return pkgName.Imported().Path(), true
						}
					}

					// Finally try matching the last segment of each import
					// path imported by any file in the package, as the
					// doc comment may appear in a different file from the
					// import.
					//
					// Ideally we would look up the DepsByPkgPath value
					// (a PackageID) in the metadata graph and use the
					// package's declared name instead of this heuristic,
					// but we don't have access to the graph here.
					for path := range pkg.Metadata().DepsByPkgPath {
						if pathpkg.Base(trimVersionSuffix(string(path))) == name {
							return string(path), true
						}
					}

					break
				}
			}
			return "", false
		},
		LookupSym: func(recv, name string) (ok bool) {
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
		},
	}
	return func(fileNode ast.Node, text string) *comment.Doc {
		currentFilePos = fileNode.Pos()
		return parser.Parse(text)
	}
}
