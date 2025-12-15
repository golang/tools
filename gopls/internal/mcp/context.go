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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/packagepath"
)

type ContextParams struct {
	File string `json:"file" jsonschema:"the absolute path to the file"`
}

func (h *handler) contextHandler(ctx context.Context, req *mcp.CallToolRequest, params ContextParams) (*mcp.CallToolResult, any, error) {
	countGoContextMCP.Inc()
	fh, snapshot, release, err := h.fileOf(ctx, params.File)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	// TODO(hxjiang): support context for GoMod.
	if snapshot.FileKind(fh) != file.Go {
		return nil, nil, fmt.Errorf("can't provide context for non-Go file")
	}

	pkg, pgf, err := golang.NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, nil, err
	}

	var result strings.Builder

	fmt.Fprintf(&result, "Current package %q (package %s):\n\n", pkg.Metadata().PkgPath, pkg.Metadata().Name)
	// Write context of the current file.
	{
		fmt.Fprintf(&result, "%s (current file):\n", pgf.URI.Base())
		result.WriteString("```go\n")
		if err := writeFileSummary(ctx, snapshot, pgf.URI, &result, false, nil); err != nil {
			return nil, nil, err
		}
		result.WriteString("```\n\n")
	}

	// Write context of the rest of the files in the current package.
	{
		for _, file := range pkg.CompiledGoFiles() {
			if file.URI == pgf.URI {
				continue
			}

			fmt.Fprintf(&result, "%s:\n", file.URI.Base())
			result.WriteString("```go\n")
			if err := writeFileSummary(ctx, snapshot, file.URI, &result, false, nil); err != nil {
				return nil, nil, err
			}
			result.WriteString("```\n\n")
		}
	}

	// Write dependencies context of current file.
	if len(pgf.File.Imports) > 0 {
		// Write import decls of the current file.
		{
			fmt.Fprintf(&result, "Current file %q contains this import declaration:\n", pgf.URI.Base())
			result.WriteString("```go\n")
			// Add all import decl to output including all floating comment by
			// using GenDecl's start and end position.
			for _, decl := range pgf.File.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.IMPORT {
					continue
				}

				text, err := pgf.NodeText(genDecl)
				if err != nil {
					return nil, nil, err
				}

				result.Write(text)
				result.WriteString("\n")
			}
			result.WriteString("```\n\n")
		}

		var toSummarize []*ast.ImportSpec
		for _, spec := range pgf.File.Imports {
			// Skip the standard library to reduce token usage, operating on
			// the assumption that the LLM is already familiar with its
			// symbols and documentation.
			if packagepath.IsStdPackage(spec.Path.Value) {
				continue
			}
			toSummarize = append(toSummarize, spec)
		}

		// Write summaries from imported packages.
		if len(toSummarize) > 0 {
			result.WriteString("The imported packages declare the following symbols:\n\n")
			for _, spec := range toSummarize {
				path := metadata.UnquoteImportPath(spec)
				id := pkg.Metadata().DepsByImpPath[path]
				if id == "" {
					continue // ignore error
				}
				md := snapshot.Metadata(id)
				if md == nil {
					continue // ignore error
				}
				if summary := summarizePackage(ctx, snapshot, md); summary != "" {
					result.WriteString(summary)
				}
			}
		}
	}

	return textResult(result.String()), nil, nil
}

func summarizePackage(ctx context.Context, snapshot *cache.Snapshot, md *metadata.Package) string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "%q (package %s)\n", md.PkgPath, md.Name)
	for _, f := range md.CompiledGoFiles {
		fmt.Fprintf(&buf, "%s:\n", f.Base())
		buf.WriteString("```go\n")
		if err := writeFileSummary(ctx, snapshot, f, &buf, true, nil); err != nil {
			return "" // ignore error
		}
		buf.WriteString("```\n\n")
	}
	return buf.String()
}

// writeFileSummary writes the file summary to the string builder based on
// the input file URI.
func writeFileSummary(ctx context.Context, snapshot *cache.Snapshot, f protocol.DocumentURI, out *strings.Builder, onlyExported bool, declsToSummarize map[string]bool) error {
	fh, err := snapshot.ReadFile(ctx, f)
	if err != nil {
		return err
	}
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return err
	}

	// If we're summarizing specific declarations, we don't need to copy the header.
	if declsToSummarize == nil {
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
	}

	// Write func decl and gen decl.
	for _, decl := range pgf.File.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if declsToSummarize != nil {
				if _, ok := declsToSummarize[decl.Name.Name]; !ok {
					continue
				}
			}
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

			// If we are summarizing specific decls, check if any of them are in this GenDecl.
			if declsToSummarize != nil {
				found := false
				for _, spec := range decl.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						if _, ok := declsToSummarize[spec.Name.Name]; ok {
							found = true
						}
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							if _, ok := declsToSummarize[name.Name]; ok {
								found = true
							}
						}
					}
				}
				if !found {
					continue
				}
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
					if declsToSummarize != nil {
						if _, ok := declsToSummarize[spec.Name.Name]; !ok {
							continue
						}
					}
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
					if declsToSummarize != nil {
						found := false
						for _, name := range spec.Names {
							if _, ok := declsToSummarize[name.Name]; ok {
								found = true
							}
						}
						if !found {
							continue
						}
					}
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
