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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
)

type fileContextParams struct {
	File string `json:"file" jsonschema:"the absolute path to the file"`
}

func (h *handler) fileContextHandler(ctx context.Context, req *mcp.CallToolRequest, params fileContextParams) (*mcp.CallToolResult, any, error) {
	countGoFileContextMCP.Inc()
	fh, snapshot, release, err := h.fileOf(ctx, params.File)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	pkg, pgf, err := golang.NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, nil, err
	}

	info := pkg.TypesInfo()
	if info == nil {
		return nil, nil, fmt.Errorf("no types info for package %q", pkg.Metadata().PkgPath)
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
	fmt.Fprintf(&result, "File `%s` is in package %q.\n", params.File, pkg.Metadata().PkgPath)
	fmt.Fprintf(&result, "Below is a summary of the APIs it uses from other files.\n")
	fmt.Fprintf(&result, "To read the full API of any package, use go_package_api.\n")
	for uri, decls := range otherFiles {
		pkgPath := "UNKNOWN"
		md, err := snapshot.NarrowestMetadataForFile(ctx, uri)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
		} else {
			pkgPath = string(md.PkgPath)
		}
		fmt.Fprintf(&result, "Referenced declarations from %s (package %q):\n", uri.Path(), pkgPath)
		result.WriteString("```go\n")
		if err := writeFileSummary(ctx, snapshot, uri, &result, false, decls); err != nil {
			return nil, nil, err
		}
		result.WriteString("```\n\n")
	}

	return textResult(result.String()), nil, nil
}
