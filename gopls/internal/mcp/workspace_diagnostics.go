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

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/mcp"
)

type workspaceDiagnosticsParams struct {
	Files []string `json:"files,omitempty"`
}

func (h *handler) workspaceDiagnosticsTool() *mcp.ServerTool {
	const desc = `Provides Go workspace diagnostics.

Checks for parse and build errors across the entire Go workspace. If provided,
"files" holds absolute paths for active files, on which additional linting is
performed.
`
	return mcp.NewServerTool(
		"go_diagnostics",
		"Checks for parse and build errors across the go workspace.",
		h.workspaceDiagnosticsHandler,
		mcp.Input(
			mcp.Property("files", mcp.Description("absolute paths to active files, if any")),
		),
	)
}

func (h *handler) workspaceDiagnosticsHandler(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[workspaceDiagnosticsParams]) (*mcp.CallToolResultFor[any], error) {
	var (
		fh       file.Handle
		snapshot *cache.Snapshot
		release  func()
		err      error
	)
	if len(params.Arguments.Files) > 0 {
		fh, snapshot, release, err = h.fileOf(ctx, params.Arguments.Files[0])
		if err != nil {
			return nil, err
		}
	} else {
		views := h.session.Views()
		if len(views) == 0 {
			return nil, fmt.Errorf("No active builds.")
		}
		snapshot, release, err = views[0].Snapshot()
		if err != nil {
			return nil, err
		}
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
		return nil, fmt.Errorf("diagnostics failed: %v", err)
	}

	fixes := make(map[*cache.Diagnostic]*protocol.CodeAction)
	for _, file := range params.Arguments.Files {
		uri := protocol.URIFromPath(file)
		// Get more specific diagnostics for the file in question.
		fileDiagnostics, fileFixes, err := h.diagnoseFile(ctx, snapshot, uri)
		if err != nil {
			return nil, fmt.Errorf("diagnostics failed: %v", err)
		}
		diagnostics[fh.URI()] = fileDiagnostics
		maps.Insert(fixes, maps.All(fileFixes))
	}

	keys := slices.Sorted(maps.Keys(diagnostics))
	var b strings.Builder
	for _, uri := range keys {
		diags := diagnostics[uri]
		if len(diags) > 0 {
			fmt.Fprintf(&b, "File `%s` has the following diagnostics:\n", uri.Path())
			if err := summarizeDiagnostics(ctx, snapshot, &b, diags, fixes); err != nil {
				return nil, err
			}
			fmt.Fprintln(&b)
		}
	}

	if b.Len() == 0 {
		return textResult("No diagnostics."), nil
	}

	return textResult(b.String()), nil
}
