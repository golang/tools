// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

// This file defines the "context" operation, which
// returns a summary of the specified package.

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/mcp"
)

type ContextParams struct {
	// TODO(hxjiang): experiment if the LLM can correctly provide the right
	// location information.
	Location protocol.Location `json:"location"`
}

func contextHandler(ctx context.Context, session *cache.Session, params *mcp.CallToolParams[ContextParams]) (*mcp.CallToolResult, error) {
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
	// TODO(hxjiang): consider making the context tool best effort. Ignore
	// non-critical errors.
	if err := writePackageSummary(ctx, snapshot, pkg, pgf, &result); err != nil {
		return nil, err
	}

	return &mcp.CallToolResult{
		Content: []*mcp.Content{
			mcp.NewTextContent(result.String()),
		},
	}, nil
}

// writePackageSummary writes the package summaries to the bytes buffer based on
// the input import specs.
func writePackageSummary(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, out *strings.Builder) error {
	if len(pgf.File.Imports) == 0 {
		return nil
	}

	fmt.Fprintf(out, "Current file %q contains this import declaration:\n", filepath.Base(pgf.URI.Path()))
	out.WriteString("--->\n")
	// Add all import decl to output including all floating comment by using
	// GenDecl's start and end position.
	for _, decl := range pgf.File.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}

		if genDecl.Tok != token.IMPORT {
			continue
		}

		text, err := pgf.NodeText(genDecl)
		if err != nil {
			return err
		}

		out.Write(text)
		out.WriteString("\n")
	}
	out.WriteString("<---\n\n")

	out.WriteString("The imported packages declare the following symbols:\n\n")

	for _, imp := range pgf.File.Imports {
		importPath := metadata.UnquoteImportPath(imp)
		if importPath == "" {
			continue
		}

		impID := pkg.Metadata().DepsByImpPath[importPath]
		if impID == "" {
			return fmt.Errorf("no package data for import %q", importPath)
		}
		impMetadata := snapshot.Metadata(impID)
		if impMetadata == nil {
			return bug.Errorf("failed to resolve import ID %q", impID)
		}

		fmt.Fprintf(out, "%s (package %s)\n", importPath, impMetadata.Name)
		for _, f := range impMetadata.CompiledGoFiles {
			fmt.Fprintf(out, "%s:\n", filepath.Base(f.Path()))
			out.WriteString("--->\n")
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
				out.WriteString("\n")
			}

			// Write exported func decl and gen decl.
			// TODO(hxjiang): write exported gen decl.
			for _, decl := range pgf.File.Decls {
				switch decl := decl.(type) {
				case *ast.FuncDecl:
					if !decl.Name.IsExported() {
						continue
					}

					if decl.Recv != nil && len(decl.Recv.List) > 0 {
						_, rname, _ := astutil.UnpackRecv(decl.Recv.List[0].Type)
						if !rname.IsExported() {
							continue
						}
					}

					out.WriteString("\n")
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
					out.WriteString("\n")
				}
			}

			out.WriteString("<---\n\n")
		}
	}
	return nil
}
