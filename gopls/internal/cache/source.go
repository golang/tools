// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"

	"golang.org/x/tools/internal/imports"
)

// interim code for using the module cache index in imports
// This code just forwards everything to an imports.ProcessEnvSource

// goplsSource is an imports.Source that provides import information using
// gopls and the module cache index.
// TODO(pjw): implement. Right now, this just forwards to the imports.ProcessEnvSource.
type goplsSource struct {
	envSource *imports.ProcessEnvSource
}

func (s *Snapshot) NewGoplsSource(is *imports.ProcessEnvSource) *goplsSource {
	return &goplsSource{
		envSource: is,
	}
}

func (s *goplsSource) LoadPackageNames(ctx context.Context, srcDir string, paths []imports.ImportPath) (map[imports.ImportPath]imports.PackageName, error) {
	return s.envSource.LoadPackageNames(ctx, srcDir, paths)
}

func (s *goplsSource) ResolveReferences(ctx context.Context, filename string, missing imports.References) ([]*imports.Result, error) {
	return s.envSource.ResolveReferences(ctx, filename, missing)
}
