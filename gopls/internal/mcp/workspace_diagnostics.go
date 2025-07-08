// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/mcp"
)

type workspaceDiagnosticsParams struct {
	File string `json:"file"`
}

func (h *handler) workspaceDiagnosticsTool() *mcp.ServerTool {
	return mcp.NewServerTool(
		"go_workspace_diagnostics",
		"Checks for parse and build errors across the go workspace",
		h.workspaceDiagnosticsHandler,
		mcp.Input(
			mcp.Property("file", mcp.Description("the absolute path of any file in the workspace")),
		),
	)
}

func (h *handler) workspaceDiagnosticsHandler(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[workspaceDiagnosticsParams]) (*mcp.CallToolResultFor[any], error) {
	_, snapshot, release, err := h.fileOf(ctx, params.Arguments.File)
	if err != nil {
		return nil, err
	}
	defer release()

	pkgMap := snapshot.WorkspacePackages()
	var ids []metadata.PackageID
	for id := range pkgMap.All() {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	diagnostics, err := snapshot.PackageDiagnostics(ctx, ids...)
	if err != nil {
		event.Error(ctx, "warning: diagnostics failed", err, snapshot.Labels()...)
	}

	keys := slices.Sorted(maps.Keys(diagnostics))
	var b strings.Builder
	for _, uri := range keys {
		diags := diagnostics[uri]
		if len(diags) > 0 {
			fmt.Fprintf(&b, "File `%s` has the following diagnostics:\n", uri.Path())
		}
		if err := summarizeDiagnostics(ctx, snapshot, &b, diags, nil); err != nil {
			return nil, err
		}
		fmt.Fprintln(&b)
	}

	return &mcp.CallToolResultFor[any]{
		Content: []*mcp.Content{
			mcp.NewTextContent(b.String()),
		},
	}, nil
}
