// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fileMetadataParams struct {
	File string `json:"file" jsonschema:"the absolute path to the file to describe"`
}

func (h *handler) fileMetadataHandler(ctx context.Context, req *mcp.CallToolRequest, params fileMetadataParams) (*mcp.CallToolResult, any, error) {
	countGoFileMetadataMCP.Inc()
	fh, snapshot, release, err := h.fileOf(ctx, params.File)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	md, err := snapshot.NarrowestMetadataForFile(ctx, fh.URI())
	if err != nil {
		return nil, nil, err
	}

	var b strings.Builder
	addf := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
	}
	addf("File `%s` is in package %q, which has the following files:\n", params.File, md.PkgPath)
	for _, f := range md.CompiledGoFiles {
		addf("\t%s\n", f.Path())
	}
	return textResult(b.String()), nil, nil
}
