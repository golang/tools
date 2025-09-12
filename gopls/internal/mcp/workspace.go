// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/immutable"
)

func (h *handler) workspaceHandler(ctx context.Context, req *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	countGoWorkspaceMCP.Inc()
	var summary bytes.Buffer
	views := h.session.Views()
	for _, v := range views {
		snapshot, release, err := v.Snapshot()
		if err != nil {
			continue // view is shut down
		}
		defer release()

		pkgs := snapshot.WorkspacePackages()

		// Special case: check if it's likely that this isn't actually a Go workspace.
		if len(views) == 1 && // only view
			(v.Type() == cache.AdHocView || v.Type() == cache.GoPackagesDriverView) && // not necessarily Go code
			pkgs.Len() == 0 { // no packages

			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "This is not a Go workspace. To work on Go code, open a directory inside a Go module."}},
			}, nil, nil
		}

		dir := v.Root().Path()
		switch v.Type() {
		case cache.GoPackagesDriverView:
			fmt.Fprintf(&summary, "The `%s` directory is loaded using a custom golang.org/x/tools/go/packages driver.\n", dir)
			fmt.Fprintf(&summary, "This indicates a non-standard build system.\n")

		case cache.GOPATHView:
			fmt.Fprintf(&summary, "The `%s` directory is loaded using a the legacy GOPATH build system.\n", dir)

		case cache.GoModView:
			fmt.Fprintf(&summary, "The `%s` directory uses Go modules, with the following main modules:\n", dir)
			summarizeModFiles(ctx, &summary, snapshot)

		case cache.GoWorkView:
			fmt.Fprintf(&summary, "The `%s` directory is in the go workspace defined by `%s`, with the following main modules:\n", dir, v.GoWork().Path())
			summarizeModFiles(ctx, &summary, snapshot)

		case cache.AdHocView:
			fmt.Fprintf(&summary, "The `%s` directory is an ad-hoc Go package, not in a Go module.\n", dir)
		}
		fmt.Fprintln(&summary)
		const summarizePackages = false
		if summarizePackages {
			summaries := packageSummaries(snapshot, pkgs)
			fmt.Fprintf(&summary, "It contains the following Go packages:\n")
			fmt.Fprintf(&summary, "\t%s\n", strings.Join(summaries, "\n\t"))
			fmt.Fprintln(&summary)
		}
	}
	return textResult(summary.String()), nil, nil
}

func summarizeModFiles(ctx context.Context, w io.Writer, snapshot *cache.Snapshot) {
	v := snapshot.View()
	for _, m := range v.ModFiles() {
		if modPath, err := modulePath(ctx, snapshot, m); err != nil {
			// Fall back on just the go.mod file.
			fmt.Fprintf(w, "\t%s\n", m.Path())
		} else {
			fmt.Fprintf(w, "\t%s (module %s)\n", m.Path(), modPath)
		}
	}
}

func modulePath(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) (string, error) {
	fh, err := snapshot.ReadFile(ctx, uri)
	if err != nil {
		return "", fmt.Errorf("Reading %s: %v", uri, err)
	}
	pmf, err := snapshot.ParseMod(ctx, fh)
	if err != nil {
		return "", fmt.Errorf("parsing modfile: %v", err)
	}
	if pmf.File == nil || pmf.File.Module == nil {
		return "", fmt.Errorf("malformed modfile")
	}
	return pmf.File.Module.Mod.Path, nil
}

func packageSummaries(snapshot *cache.Snapshot, pkgs immutable.Map[cache.PackageID, cache.PackagePath]) []string {
	var summaries []string
	for id := range pkgs.All() {
		mp := snapshot.Metadata(id)
		if len(mp.CompiledGoFiles) == 0 {
			continue // For convenience, just skip uncompiled packages; we could do more if it matters.
		}
		dir := mp.CompiledGoFiles[0].DirPath()
		summaries = append(summaries, fmt.Sprintf("The `%s` directory contains the %q package with path %q", dir, mp.Name, mp.PkgPath))
	}
	slices.Sort(summaries) // for stability
	return summaries
}
