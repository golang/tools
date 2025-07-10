// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"strconv"

	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/internal/mcp"
)

type outlineParams struct {
	PackagePaths []string `json:"packagePaths"`
}

func (h *handler) outlineTool() *mcp.ServerTool {
	return mcp.NewServerTool(
		"go_package_api",
		"Provides a summary of a Go package API",
		h.outlineHandler,
		mcp.Input(
			mcp.Property("packagePaths", mcp.Description("the go package paths to describe")),
		),
	)
}

func (h *handler) outlineHandler(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[outlineParams]) (*mcp.CallToolResultFor[any], error) {
	snapshot, release, err := h.snapshot()
	if err != nil {
		return nil, err
	}
	defer release()

	// Await initialization to ensure we've at least got an initial package graph
	md, err := snapshot.LoadMetadataGraph(ctx)
	if err != nil {
		return nil, err
	}

	var toSummarize []*metadata.Package
	for _, imp := range params.Arguments.PackagePaths {
		pkgPath := metadata.PackagePath(imp)
		if len(imp) > 0 && imp[0] == '"' {
			unquoted, err := strconv.Unquote(imp)
			if err != nil {
				return nil, fmt.Errorf("failed to unquote %s: %v", imp, err)
			}
			pkgPath = metadata.PackagePath(unquoted)
		}
		if mps := md.ForPackagePath[pkgPath]; len(mps) > 0 {
			toSummarize = append(toSummarize, mps[0]) // first is best
		}
	}

	var content []*mcp.Content
	for _, mp := range toSummarize {
		if md == nil {
			continue // ignore error
		}
		if summary := summarizePackage(ctx, snapshot, mp); summary != "" {
			content = append(content, mcp.NewTextContent(summary))
		}
	}
	return &mcp.CallToolResultFor[any]{
		Content: content,
	}, nil
}
