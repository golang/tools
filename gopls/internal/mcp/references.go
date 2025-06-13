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

type FindReferencesParams struct {
	Location protocol.Location `json:"location"`
}

func referenceHandler(ctx context.Context, session *cache.Session, params *mcp.CallToolParamsFor[FindReferencesParams]) (*mcp.CallToolResultFor[struct{}], error) {
	fh, snapshot, release, err := session.FileOf(ctx, params.Arguments.Location.URI)
	if err != nil {
		return nil, err
	}
	defer release()
	pos := params.Arguments.Location.Range.Start
	refs, err := golang.References(ctx, snapshot, fh, pos, true)
	if err != nil {
		return nil, err
	}
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
		fmt.Fprintf(&builder, "The reference is located on line %v, which has content %q\n", r.Range.Start.Line, lineContent)
		builder.WriteString("\n")
	}
	return &mcp.CallToolResultFor[struct{}]{
		Content: []*mcp.Content{
			mcp.NewTextContent(builder.String()),
		},
	}, nil
}
