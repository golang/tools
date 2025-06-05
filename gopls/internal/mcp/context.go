// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

// This file defines the "context" operation, which returns a summary of the
// specified package.

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/internal/mcp"
)

type ContextParams struct {
	// TODO(hxjiang): experiment if the LLM can correctly provide the right
	// location information.
	Location protocol.Location `json:"location"`
}

func contextHandler(ctx context.Context, session *cache.Session, params *mcp.CallToolParamsFor[ContextParams]) (*mcp.CallToolResultFor[struct{}], error) {
	fh, snapshot, release, err := session.FileOf(ctx, params.Arguments.Location.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	// TODO(hxjiang): support context for GoMod.
	if snapshot.FileKind(fh) != file.Go {
		return nil, fmt.Errorf("can't provide context for non-Go file")
	}

	pkg, pgf, err := golang.NarrowestPackageForFile(ctx, snapshot, params.Arguments.Location.URI)
	if err != nil {
		return nil, err
	}

	var result strings.Builder
	result.WriteString("Code blocks are delimited by --->...<--- markers.\n\n")

	// TODO(hxjiang): add context based on location's range.

	fmt.Fprintf(&result, "Current package %q (package %s) declares the following symbols:\n\n", pkg.Metadata().PkgPath, pkg.Metadata().Name)
	// Write context of the current file.
	{
		fmt.Fprintf(&result, "%s (current file):\n", pgf.URI.Base())
		result.WriteString("--->\n")
		if err := writeFileSummary(ctx, snapshot, pgf.URI, &result, false); err != nil {
			return nil, err
		}
		result.WriteString("<---\n\n")
	}

	// Write context of the rest of the files in the current package.
	{
		for _, file := range pkg.CompiledGoFiles() {
			if file.URI == pgf.URI {
				continue
			}

			fmt.Fprintf(&result, "%s:\n", file.URI.Base())
			result.WriteString("--->\n")
			if err := writeFileSummary(ctx, snapshot, file.URI, &result, false); err != nil {
				return nil, err
			}
			result.WriteString("<---\n\n")
		}
	}

	// Write dependencies context of current file.
	if len(pgf.File.Imports) > 0 {
		// Write import decls of the current file.
		{
			fmt.Fprintf(&result, "Current file %q contains this import declaration:\n", pgf.URI.Base())
			result.WriteString("--->\n")
			// Add all import decl to output including all floating comment by
			// using GenDecl's start and end position.
			for _, decl := range pgf.File.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.IMPORT {
					continue
				}

				text, err := pgf.NodeText(genDecl)
				if err != nil {
					return nil, err
				}

				result.Write(text)
				result.WriteString("\n")
			}
			result.WriteString("<---\n\n")
		}

		// Write summaries from imported packages.
		{
			result.WriteString("The imported packages declare the following symbols:\n\n")
			for _, imp := range pgf.File.Imports {
				importPath := metadata.UnquoteImportPath(imp)
				if importPath == "" {
					continue
				}

				impID := pkg.Metadata().DepsByImpPath[importPath]
				if impID == "" {
					continue // ignore error
				}
				impMetadata := snapshot.Metadata(impID)
				if impMetadata == nil {
					continue // ignore error
				}

				fmt.Fprintf(&result, "%q (package %s)\n", importPath, impMetadata.Name)
				for _, f := range impMetadata.CompiledGoFiles {
					fmt.Fprintf(&result, "%s:\n", f.Base())
					result.WriteString("--->\n")
					if err := writeFileSummary(ctx, snapshot, f, &result, true); err != nil {
						return nil, err
					}
					result.WriteString("<---\n\n")
				}
			}
		}
	}

	return &mcp.CallToolResultFor[struct{}]{
		Content: []*mcp.Content{
			mcp.NewTextContent(result.String()),
		},
	}, nil
}

// writeFileSummary writes the file summary to the string builder based on
// the input file URI.
func writeFileSummary(ctx context.Context, snapshot *cache.Snapshot, f protocol.DocumentURI, out *strings.Builder, onlyExported bool) error {
	fh, err := snapshot.ReadFile(ctx, f)
	if err != nil {
		return err
	}
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return err
	}

	// Copy everything before the first non-import declaration:
	// package decl, imports decl(s), and all comments (excluding copyright).
	{
		endPos := pgf.File.FileEnd

	outerloop:
		for _, decl := range pgf.File.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Doc != nil {
					endPos = decl.Doc.Pos()
				} else {
					endPos = decl.Pos()
				}
				break outerloop
			case *ast.GenDecl:
				if decl.Tok == token.IMPORT {
					continue
				}
				if decl.Doc != nil {
					endPos = decl.Doc.Pos()
				} else {
					endPos = decl.Pos()
				}
				break outerloop
			}
		}

		startPos := pgf.File.FileStart
		if copyright := golang.CopyrightComment(pgf.File); copyright != nil {
			startPos = copyright.End()
		}

		text, err := pgf.PosText(startPos, endPos)
		if err != nil {
			return err
		}

		out.Write(bytes.TrimSpace(text))
		out.WriteString("\n\n")
	}

	// Write func decl and gen decl.
	for _, decl := range pgf.File.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if onlyExported {
				if !decl.Name.IsExported() {
					continue
				}

				if decl.Recv != nil && len(decl.Recv.List) > 0 {
					_, rname, _ := astutil.UnpackRecv(decl.Recv.List[0].Type)
					if !rname.IsExported() {
						continue
					}
				}
			}

			// Write doc comment and func signature.
			startPos := decl.Pos()
			if decl.Doc != nil {
				startPos = decl.Doc.Pos()
			}

			text, err := pgf.PosText(startPos, decl.Type.End())
			if err != nil {
				return err
			}

			out.Write(text)
			out.WriteString("\n\n")

		case *ast.GenDecl:
			if decl.Tok == token.IMPORT {
				continue
			}

			// Dump the entire GenDecl (exported or unexported)
			// including doc comment without any filtering to the output.
			if !onlyExported {
				startPos := decl.Pos()
				if decl.Doc != nil {
					startPos = decl.Doc.Pos()
				}
				text, err := pgf.PosText(startPos, decl.End())
				if err != nil {
					return err
				}

				out.Write(text)
				out.WriteString("\n")
				continue
			}

			// Write only the GenDecl with exported identifier to the output.
			var buf bytes.Buffer
			if decl.Doc != nil {
				text, err := pgf.NodeText(decl.Doc)
				if err != nil {
					return err
				}
				buf.Write(text)
				buf.WriteString("\n")
			}

			buf.WriteString(decl.Tok.String() + " ")
			if decl.Lparen.IsValid() {
				buf.WriteString("(\n")
			}

			var anyExported bool
			for _, spec := range decl.Specs {
				// Captures the full byte range of the spec, including
				// its associated doc comments and line comments.
				// This range also covers any floating comments as these
				// can be valuable for context. Like
				// ```
				// type foo struct { // floating comment.
				// 		// floating comment.
				//
				// 		x int
				// }
				// ```
				var startPos, endPos token.Pos

				switch spec := spec.(type) {
				case *ast.TypeSpec:
					// TODO(hxjiang): only keep the exported field of
					// struct spec and exported method of interface spec.
					if !spec.Name.IsExported() {
						continue
					}
					anyExported = true

					// Include preceding doc comment, if any.
					if spec.Doc == nil {
						startPos = spec.Pos()
					} else {
						startPos = spec.Doc.Pos()
					}

					// Include trailing line comment, if any.
					if spec.Comment == nil {
						endPos = spec.End()
					} else {
						endPos = spec.Comment.End()
					}

				case *ast.ValueSpec:
					// TODO(hxjiang): only keep the exported identifier.
					if !slices.ContainsFunc(spec.Names, (*ast.Ident).IsExported) {
						continue
					}
					anyExported = true

					if spec.Doc == nil {
						startPos = spec.Pos()
					} else {
						startPos = spec.Doc.Pos()
					}

					if spec.Comment == nil {
						endPos = spec.End()
					} else {
						endPos = spec.Comment.End()
					}
				}

				indent, err := pgf.Indentation(startPos)
				if err != nil {
					return err
				}

				buf.WriteString(indent)

				text, err := pgf.PosText(startPos, endPos)
				if err != nil {
					return err
				}

				buf.Write(text)
				buf.WriteString("\n")
			}

			if decl.Lparen.IsValid() {
				buf.WriteString(")\n")
			}

			// Only write the summary of the genDecl if there is
			// any exported spec.
			if anyExported {
				out.Write(buf.Bytes())
				out.WriteString("\n")
			}
		}
	}
	return nil
}
