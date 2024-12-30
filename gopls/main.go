// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Gopls (pronounced “go please”) is an LSP server for Go.
// The Language Server Protocol allows any text editor
// to be extended with IDE-like features;
// see https://langserver.org/ for details.
//
// See https://github.com/golang/tools/blob/master/gopls/README.md
// for the most up-to-date documentation.
package main // import "golang.org/x/tools/gopls"

import (
	"context"
	"log"
	"os"

	"golang.org/x/telemetry"
	"golang.org/x/telemetry/counter"
	"golang.org/x/tools/gopls/internal/cmd"
	"golang.org/x/tools/gopls/internal/filecache"
	versionpkg "golang.org/x/tools/gopls/internal/version"
	"golang.org/x/tools/internal/tool"
)

var version = "" // if set by the linker, overrides the gopls version

func main() {
	versionpkg.VersionOverride = version

	telemetry.Start(telemetry.Config{
		ReportCrashes: true,
		Upload:        true,
	})

	// Force early creation of the filecache and refuse to start
	// if there were unexpected errors such as ENOSPC. This
	// minimizes the window of exposure to deletion of the
	// executable, and ensures that all subsequent calls to
	// filecache.Get cannot fail for these two reasons;
	// see issue #67433.
	//
	// This leaves only one likely cause for later failures:
	// deletion of the cache while gopls is running. If the
	// problem continues, we could periodically stat the cache
	// directory (for example at the start of every RPC) and
	// either re-create it or just fail the RPC with an
	// informative error and terminate the process.
	if _, err := filecache.Get("nonesuch", [32]byte{}); err != nil && err != filecache.ErrNotFound {
		counter.Inc("gopls/nocache")
		log.Fatalf("gopls cannot access its persistent index (disk full?): %v", err)
	}

	ctx := context.Background()
	tool.Main(ctx, cmd.New(), os.Args[1:])
}
