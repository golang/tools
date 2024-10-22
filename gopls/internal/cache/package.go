// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"fmt"
	"go/ast"
	"go/scanner"
	"go/token"
	"go/types"
	"slices"
	"sync"

	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/methodsets"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/cache/testfuncs"
	"golang.org/x/tools/gopls/internal/cache/xrefs"
	"golang.org/x/tools/gopls/internal/protocol"
)

// Convenient aliases for very heavily used types.
type (
	PackageID   = metadata.PackageID
	PackagePath = metadata.PackagePath
	PackageName = metadata.PackageName
	ImportPath  = metadata.ImportPath
)

// A Package is the union of package metadata and type checking results.
//
// TODO(rfindley): for now, we do not persist the post-processing of
// loadDiagnostics, because the value of the snapshot.packages map is just the
// package handle. Fix this.
type Package struct {
	metadata        *metadata.Package
	loadDiagnostics []*Diagnostic
	pkg             *syntaxPackage
}

// syntaxPackage contains parse trees and type information for a package.
type syntaxPackage struct {
	// -- identifiers --
	id PackageID

	// -- outputs --
	fset            *token.FileSet // for now, same as the snapshot's FileSet
	goFiles         []*parsego.File
	compiledGoFiles []*parsego.File
	diagnostics     []*Diagnostic
	parseErrors     []scanner.ErrorList
	typeErrors      []types.Error
	types           *types.Package
	typesInfo       *types.Info
	typesSizes      types.Sizes
	importMap       map[PackagePath]*types.Package

	xrefsOnce sync.Once
	_xrefs    []byte // only used by the xrefs method

	methodsetsOnce sync.Once
	_methodsets    *methodsets.Index // only used by the methodsets method

	testsOnce sync.Once
	_tests    *testfuncs.Index // only used by the tests method
}

func (p *syntaxPackage) xrefs() []byte {
	p.xrefsOnce.Do(func() {
		p._xrefs = xrefs.Index(p.compiledGoFiles, p.types, p.typesInfo)
	})
	return p._xrefs
}

func (p *syntaxPackage) methodsets() *methodsets.Index {
	p.methodsetsOnce.Do(func() {
		p._methodsets = methodsets.NewIndex(p.fset, p.types)
	})
	return p._methodsets
}

func (p *syntaxPackage) tests() *testfuncs.Index {
	p.testsOnce.Do(func() {
		p._tests = testfuncs.NewIndex(p.compiledGoFiles, p.typesInfo)
	})
	return p._tests
}

// hasFixedFiles reports whether there are any 'fixed' compiled go files in the
// package.
//
// Intended to be used to refine bug reports.
func (p *syntaxPackage) hasFixedFiles() bool {
	return slices.ContainsFunc(p.compiledGoFiles, (*parsego.File).Fixed)
}

func (p *Package) String() string { return string(p.metadata.ID) }

func (p *Package) Metadata() *metadata.Package { return p.metadata }

// A loadScope defines a package loading scope for use with go/packages.
//
// TODO(rfindley): move this to load.go.
type loadScope interface {
	aScope()
}

// TODO(rfindley): move to load.go
type (
	fileLoadScope    protocol.DocumentURI // load packages containing a file (including command-line-arguments)
	packageLoadScope string               // load a specific package (the value is its PackageID)
	moduleLoadScope  struct {
		dir        string // dir containing the go.mod file
		modulePath string // parsed module path
	}
	viewLoadScope struct{} // load the workspace
)

// Implement the loadScope interface.
func (fileLoadScope) aScope()    {}
func (packageLoadScope) aScope() {}
func (moduleLoadScope) aScope()  {}
func (viewLoadScope) aScope()    {}

func (p *Package) CompiledGoFiles() []*parsego.File {
	return p.pkg.compiledGoFiles
}

func (p *Package) File(uri protocol.DocumentURI) (*parsego.File, error) {
	return p.pkg.File(uri)
}

func (pkg *syntaxPackage) File(uri protocol.DocumentURI) (*parsego.File, error) {
	for _, cgf := range pkg.compiledGoFiles {
		if cgf.URI == uri {
			return cgf, nil
		}
	}
	for _, gf := range pkg.goFiles {
		if gf.URI == uri {
			return gf, nil
		}
	}
	return nil, fmt.Errorf("no parsed file for %s in %v", uri, pkg.id)
}

// Syntax returns parsed compiled Go files contained in this package.
func (p *Package) Syntax() []*ast.File {
	var syntax []*ast.File
	for _, pgf := range p.pkg.compiledGoFiles {
		syntax = append(syntax, pgf.File)
	}
	return syntax
}

// FileSet returns the FileSet describing this package's positions.
//
// The returned FileSet is guaranteed to describe all Syntax, but may also
// describe additional files.
func (p *Package) FileSet() *token.FileSet {
	return p.pkg.fset
}

// Types returns the type checked go/types.Package.
func (p *Package) Types() *types.Package {
	return p.pkg.types
}

// TypesInfo returns the go/types.Info annotating the Syntax of this package
// with type information.
//
// All fields in the resulting Info are populated.
func (p *Package) TypesInfo() *types.Info {
	return p.pkg.typesInfo
}

// TypesSizes returns the sizing function used for types in this package.
func (p *Package) TypesSizes() types.Sizes {
	return p.pkg.typesSizes
}

// DependencyTypes returns the type checker's symbol for the specified
// package. It returns nil if path is not among the transitive
// dependencies of p, or if no symbols from that package were
// referenced during the type-checking of p.
func (p *Package) DependencyTypes(path PackagePath) *types.Package {
	return p.pkg.importMap[path]
}

// ParseErrors returns a slice containing all non-empty parse errors produces
// while parsing p.Syntax, or nil if the package contains no parse errors.
func (p *Package) ParseErrors() []scanner.ErrorList {
	return p.pkg.parseErrors
}

// TypeErrors returns the go/types.Errors produced during type checking this
// package, if any.
func (p *Package) TypeErrors() []types.Error {
	return p.pkg.typeErrors
}
