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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
)

type workspaceDiagnosticsParams struct {
	Files []string `json:"files,omitempty" jsonschema:"absolute paths to active files, if any"`
}

func (h *handler) workspaceDiagnosticsHandler(ctx context.Context, req *mcp.CallToolRequest, params workspaceDiagnosticsParams) (*mcp.CallToolResult, any, error) {
	countGoDiagnosticsMCP.Inc()
	var (
		fh       file.Handle
		snapshot *cache.Snapshot
		release  func()
		err      error
	)
	if len(params.Files) > 0 {
		fh, snapshot, release, err = h.fileOf(ctx, params.Files[0])
		if err != nil {
			return nil, nil, err
		}
	} else {
		views := h.session.Views()
		if len(views) == 0 {
			return nil, nil, fmt.Errorf("No active builds.")
		}
		snapshot, release, err = views[0].Snapshot()
		if err != nil {
			return nil, nil, err
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
		return nil, nil, fmt.Errorf("diagnostics failed: %v", err)
	}

	fixes := make(map[*cache.Diagnostic]*protocol.CodeAction)
	for _, file := range params.Files {
		uri := protocol.URIFromPath(file)
		// Get more specific diagnostics for the file in question.
		fileDiagnostics, fileFixes, err := h.diagnoseFile(ctx, snapshot, uri)
		if err != nil {
			return nil, nil, fmt.Errorf("diagnostics failed: %v", err)
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
				return nil, nil, err
			}
			fmt.Fprintln(&b)
		}
	}

	if b.Len() == 0 {
		return textResult("No diagnostics."), nil, nil
	}

	return textResult(b.String()), nil, nil
}
