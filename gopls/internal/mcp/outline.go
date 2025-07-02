// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"strconv"

	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/internal/mcp"
)

type outlineParams struct {
	File    string   `json:"file"`
	Imports []string `json:"imports"`
}

func (h *handler) outlineTool() *mcp.ServerTool {
	return mcp.NewServerTool(
		"go_package_outline",
		"Provides a summary of a Go package API",
		h.outlineHandler,
		mcp.Input(
			mcp.Property("file", mcp.Description("the absolute path to the file containing the imports to investigate")),
			mcp.Property("imports", mcp.Description("the import paths from the file to describe")),
		),
	)
}

func (h *handler) outlineHandler(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[outlineParams]) (*mcp.CallToolResultFor[any], error) {
	fh, snapshot, release, err := h.fileOf(ctx, params.Arguments.File)
	if err != nil {
		return nil, err
	}
	defer release()

	if snapshot.FileKind(fh) != file.Go {
		return nil, fmt.Errorf("can't provide outlines for non-Go files")
	}

	pkg, pgf, err := golang.NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}

	fileImports := make(map[string]metadata.ImportPath)
	for _, spec := range pgf.File.Imports {
		path := metadata.UnquoteImportPath(spec)
		fileImports[string(path)] = path
	}

	var toSummarize []metadata.ImportPath
	for _, imp := range params.Arguments.Imports {
		if len(imp) > 0 && imp[0] == '"' {
			unquoted, err := strconv.Unquote(imp)
			if err != nil {
				return nil, fmt.Errorf("failed to unquote %s: %v", imp, err)
			}
			imp = unquoted
		}
		impPath, ok := fileImports[imp]
		if !ok {
			return nil, fmt.Errorf("import path %s is not found in the file", imp)
		}
		toSummarize = append(toSummarize, impPath)
	}

	var content []*mcp.Content
	for _, path := range toSummarize {
		id := pkg.Metadata().DepsByImpPath[path]
		if id == "" {
			continue // ignore error
		}
		md := snapshot.Metadata(id)
		if md == nil {
			continue // ignore error
		}
		if summary := summarizePackage(ctx, snapshot, path, md); summary != "" {
			content = append(content, mcp.NewTextContent(summary))
		}
	}
	return &mcp.CallToolResultFor[any]{
		Content: content,
	}, nil
}
