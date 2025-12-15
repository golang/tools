// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
)

type findReferencesParams struct {
	Location protocol.Location `json:"location"`
}

func (h *handler) referencesHandler(ctx context.Context, req *mcp.CallToolRequest, params findReferencesParams) (*mcp.CallToolResult, any, error) {
	countGoReferencesMCP.Inc()
	fh, snapshot, release, err := h.session.FileOf(ctx, params.Location.URI)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	pos := params.Location.Range.Start
	refs, err := golang.References(ctx, snapshot, fh, pos, true)
	if err != nil {
		return nil, nil, err
	}
	formatted, err := formatReferences(ctx, snapshot, refs)
	return formatted, nil, err
}

func formatReferences(ctx context.Context, snapshot *cache.Snapshot, refs []protocol.Location) (*mcp.CallToolResult, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("no references found")
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "The object has %v references. Their locations are listed below\n", len(refs))
	for i, r := range refs {
		fmt.Fprintf(&builder, "Reference %d\n", i+1)
		fmt.Fprintf(&builder, "Located in the file: %s\n", filepath.ToSlash(r.URI.Path()))
		refFh, err := snapshot.ReadFile(ctx, r.URI)
		// If for some reason there is an error reading the file content, we should still
		// return the references URIs.
		if err != nil {
			continue
		}
		content, err := refFh.Content()
		if err != nil {
			continue
		}
		lines := strings.Split(string(content), "\n")
		var lineContent string
		if int(r.Range.Start.Line) < len(lines) {
			lineContent = strings.TrimLeftFunc(lines[r.Range.Start.Line], unicode.IsSpace)
		} else {
			continue
		}
		fmt.Fprintf(&builder, "The reference is located on line %v, which has content `%s`\n", r.Range.Start.Line, lineContent)
		builder.WriteString("\n")
	}
	return textResult(builder.String()), nil
}
