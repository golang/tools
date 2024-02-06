// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package work

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

func Diagnostics(ctx context.Context, snapshot *cache.Snapshot) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	ctx, done := event.Start(ctx, "work.Diagnostics", snapshot.Labels()...)
	defer done()

	reports := map[protocol.DocumentURI][]*cache.Diagnostic{}
	uri := snapshot.View().GoWork()
	if uri == "" {
		return nil, nil
	}
	fh, err := snapshot.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	reports[fh.URI()] = []*cache.Diagnostic{}
	diagnostics, err := diagnoseOne(ctx, snapshot, fh)
	if err != nil {
		return nil, err
	}
	for _, d := range diagnostics {
		fh, err := snapshot.ReadFile(ctx, d.URI)
		if err != nil {
			return nil, err
		}
		reports[fh.URI()] = append(reports[fh.URI()], d)
	}

	return reports, nil
}

func diagnoseOne(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]*cache.Diagnostic, error) {
	pw, err := snapshot.ParseWork(ctx, fh)
	if err != nil {
		if pw == nil || len(pw.ParseErrors) == 0 {
			return nil, err
		}
		return pw.ParseErrors, nil
	}

	// Add diagnostic if a directory does not contain a module.
	var diagnostics []*cache.Diagnostic
	for _, use := range pw.File.Use {
		rng, err := pw.Mapper.OffsetRange(use.Syntax.Start.Byte, use.Syntax.End.Byte)
		if err != nil {
			return nil, err
		}

		modfh, err := snapshot.ReadFile(ctx, modFileURI(pw, use))
		if err != nil {
			return nil, err
		}
		if _, err := modfh.Content(); err != nil && os.IsNotExist(err) {
			diagnostics = append(diagnostics, &cache.Diagnostic{
				URI:      fh.URI(),
				Range:    rng,
				Severity: protocol.SeverityError,
				Source:   cache.WorkFileError,
				Message:  fmt.Sprintf("directory %v does not contain a module", use.Path),
			})
		}
	}
	return diagnostics, nil
}

func modFileURI(pw *cache.ParsedWorkFile, use *modfile.Use) protocol.DocumentURI {
	workdir := filepath.Dir(pw.URI.Path())

	modroot := filepath.FromSlash(use.Path)
	if !filepath.IsAbs(modroot) {
		modroot = filepath.Join(workdir, modroot)
	}

	return protocol.URIFromPath(filepath.Join(modroot, "go.mod"))
}
