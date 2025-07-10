// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/mcp"
)

type fileContextParams struct {
	File string `json:"file"`
}

func (h *handler) fileContextTool() *mcp.ServerTool {
	return mcp.NewServerTool(
		"go_file_context",
		"Summarizes a file's cross-file dependencies",
		h.fileContextHandler,
		mcp.Input(
			mcp.Property("file", mcp.Description("the absolute path to the file")),
		),
	)
}

func (h *handler) fileContextHandler(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[fileContextParams]) (*mcp.CallToolResultFor[any], error) {
	fh, snapshot, release, err := h.fileOf(ctx, params.Arguments.File)
	if err != nil {
		return nil, err
	}
	defer release()

	pkg, pgf, err := golang.NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}

	info := pkg.TypesInfo()
	if info == nil {
		return nil, fmt.Errorf("no types info for package %q", pkg.Metadata().PkgPath)
	}

	// Group objects defined in other files by file URI.
	otherFiles := make(map[protocol.DocumentURI]map[string]bool)
	addObj := func(obj types.Object) {
		if obj == nil {
			return
		}
		pos := obj.Pos()
		if !pos.IsValid() {
			return
		}
		objFile := pkg.FileSet().File(pos)
		if objFile == nil {
			return
		}
		uri := protocol.URIFromPath(objFile.Name())
		if uri == fh.URI() {
			return
		}
		if _, ok := otherFiles[uri]; !ok {
			otherFiles[uri] = make(map[string]bool)
		}
		otherFiles[uri][obj.Name()] = true
	}

	for cur := range pgf.Cursor.Preorder((*ast.Ident)(nil)) {
		id := cur.Node().(*ast.Ident)
		addObj(info.Uses[id])
		addObj(info.Defs[id])
	}

	var result strings.Builder
	fmt.Fprintf(&result, "File `%s` is in package %q.\n", params.Arguments.File, pkg.Metadata().PkgPath)
	fmt.Fprintf(&result, "Below is a summary of the APIs it uses from other files.\n")
	fmt.Fprintf(&result, "To read the full API of any package, use go_package_api.\n")
	for uri, decls := range otherFiles {
		pkgPath := "UNKNOWN"
		md, err := snapshot.NarrowestMetadataForFile(ctx, uri)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		} else {
			pkgPath = string(md.PkgPath)
		}
		fmt.Fprintf(&result, "Referenced declarations from %s (package %q):\n", uri.Path(), pkgPath)
		result.WriteString("```go\n")
		if err := writeFileSummary(ctx, snapshot, uri, &result, false, decls); err != nil {
			return nil, err
		}
		result.WriteString("```\n\n")
	}

	return textResult(result.String()), nil
}
