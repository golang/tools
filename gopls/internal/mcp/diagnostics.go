// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

// This file defines the "diagnostics" operation, which is responsible for
// returning diagnostics for the input file.

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/mcp"
)

type DiagnosticsParams struct {
	Location protocol.Location `json:"location"`
}

func diagnosticsHandler(ctx context.Context, session *cache.Session, params *mcp.CallToolParamsFor[DiagnosticsParams]) (*mcp.CallToolResultFor[struct{}], error) {
	fh, snapshot, release, err := session.FileOf(ctx, params.Arguments.Location.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	diagnostics, err := golang.DiagnoseFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}

	var builder strings.Builder
	if len(diagnostics) == 0 {
		builder.WriteString("No diagnostics")
	} else {
		for _, d := range diagnostics {
			fmt.Fprintf(&builder, "%d:%d-%d:%d: [%s] %s\n", d.Range.Start.Line, d.Range.Start.Character, d.Range.End.Line, d.Range.End.Character, d.Severity, d.Message)
		}
	}

	return &mcp.CallToolResultFor[struct{}]{
		Content: []*mcp.Content{
			mcp.NewTextContent(builder.String()),
		},
	}, nil
}
