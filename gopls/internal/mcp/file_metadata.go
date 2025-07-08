// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/tools/internal/mcp"
)

type fileMetadataParams struct {
	File string `json:"file"`
}

func (h *handler) fileMetadataTool() *mcp.ServerTool {
	return mcp.NewServerTool(
		"go_file_metadata",
		"Provides metadata about the Go package containing the file",
		h.fileMetadataHandler,
		mcp.Input(
			mcp.Property("file", mcp.Description("the absolute path to the file to describe")),
		),
	)
}

func (h *handler) fileMetadataHandler(ctx context.Context, _ *mcp.ServerSession, params *mcp.CallToolParamsFor[fileMetadataParams]) (*mcp.CallToolResultFor[any], error) {
	fh, snapshot, release, err := h.fileOf(ctx, params.Arguments.File)
	if err != nil {
		return nil, err
	}
	defer release()

	md, err := snapshot.NarrowestMetadataForFile(ctx, fh.URI())
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	addf := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
	}
	addf("File `%s` is in package %q, which has the following files:\n", params.Arguments.File, md.PkgPath)
	for _, f := range md.CompiledGoFiles {
		addf("\t%s\n", f.Path())
	}
	return textResult(b.String()), nil
}
