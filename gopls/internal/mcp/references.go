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

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/mcp"
)

// locationProperty decorates the schema of a protocol.Location property with
// the given name.
func locationProperty(name string) mcp.SchemaOption {
	return mcp.Property(
		name,
		mcp.Description("location inside of a text file"),
		mcp.Property("uri", mcp.Description("URI of the text document")),
		mcp.Property("range",
			mcp.Description("range within text document"),
			mcp.Required(false),
			mcp.Property(
				"start",
				mcp.Description("start position of range"),
				mcp.Property("line", mcp.Description("line number (zero-based)")),
				mcp.Property("character", mcp.Description("column number (zero-based, UTF-16 encoding)")),
			),
			mcp.Property(
				"end",
				mcp.Description("end position of range"),
				mcp.Property("line", mcp.Description("line number (zero-based)")),
				mcp.Property("character", mcp.Description("column number (zero-based, UTF-16 encoding)")),
			),
		),
	)
}

type findReferencesParams struct {
	Location protocol.Location `json:"location"`
}

func (h *handler) referencesTool() *mcp.ServerTool {
	return mcp.NewServerTool(
		"go_references",
		"Provide the locations of references to a given object",
		h.referencesHandler,
		mcp.Input(locationProperty("location")),
	)
}

func (h *handler) referencesHandler(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[findReferencesParams]) (*mcp.CallToolResultFor[any], error) {
	fh, snapshot, release, err := h.session.FileOf(ctx, params.Arguments.Location.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	pos := params.Arguments.Location.Range.Start
	refs, err := golang.References(ctx, snapshot, fh, pos, true)
	if err != nil {
		return nil, err
	}
	return formatReferences(ctx, snapshot, refs)
}

func formatReferences(ctx context.Context, snapshot *cache.Snapshot, refs []protocol.Location) (*mcp.CallToolResultFor[any], error) {
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
