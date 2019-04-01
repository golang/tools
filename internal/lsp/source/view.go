// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/lsp/xlog"
	"golang.org/x/tools/internal/span"
)

// View abstracts the underlying architecture of the package using the source
// package. The view provides access to files and their contents, so the source
// package does not directly access the file system.
type View interface {
	Logger() xlog.Logger
	FileSet() *token.FileSet
	GetFile(ctx context.Context, uri span.URI) (File, error)
	SetContent(ctx context.Context, uri span.URI, content []byte) error
}

// File represents a Go source file that has been type-checked. It is the input
// to most of the exported functions in this package, as it wraps up the
// building blocks for most queries. Users of the source package can abstract
// the loading of packages into their own caching systems.
type File interface {
	URI() span.URI
	GetAST(ctx context.Context) *ast.File
	GetFileSet(ctx context.Context) *token.FileSet
	GetPackage(ctx context.Context) Package
	GetToken(ctx context.Context) *token.File
	GetContent(ctx context.Context) []byte
}

// Package represents a Go package that has been type-checked. It maintains
// only the relevant fields of a *go/packages.Package.
type Package interface {
	GetFilenames() []string
	GetSyntax() []*ast.File
	GetErrors() []packages.Error
	GetTypes() *types.Package
	GetTypesInfo() *types.Info
	GetTypesSizes() types.Sizes
	IsIllTyped() bool
	GetActionGraph(ctx context.Context, a *analysis.Analyzer) (*Action, error)
}

// TextEdit represents a change to a section of a document.
// The text within the specified span should be replaced by the supplied new text.
type TextEdit struct {
	Span    span.Span
	NewText string
}
