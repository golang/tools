// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/lsp/cache/metadata"
	"golang.org/x/tools/gopls/internal/lsp/cache/methodsets"
	"golang.org/x/tools/gopls/internal/lsp/cache/parsego"
	"golang.org/x/tools/gopls/internal/lsp/progress"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/imports"
)

// A GlobalSnapshotID uniquely identifies a snapshot within this process and
// increases monotonically with snapshot creation time.
//
// We use a distinct integral type for global IDs to help enforce correct
// usage.
type GlobalSnapshotID uint64

// Snapshot represents the current state for the given view.
type Snapshot interface {
	// FileKind returns the type of a file.
	//
	// We can't reliably deduce the kind from the file name alone,
	// as some editors can be told to interpret a buffer as
	// language different from the file name heuristic, e.g. that
	// an .html file actually contains Go "html/template" syntax,
	// or even that a .go file contains Python.
	FileKind(file.Handle) file.Kind

	// Options returns the options associated with this snapshot.
	Options() *settings.Options

	// A Snapshot is a caching implementation of FileSource whose
	// ReadFile method returns consistent information about the existence
	// and content of each file throughout its lifetime.
	file.Source

	// FindFile returns the FileHandle for the given URI, if it is already
	// in the given snapshot.
	// TODO(adonovan): delete this operation; use ReadFile instead.
	FindFile(uri protocol.DocumentURI) file.Handle

	// ParseGo returns the parsed AST for the file.
	// If the file is not available, returns nil and an error.
	// Position information is added to FileSet().
	ParseGo(ctx context.Context, fh file.Handle, mode parser.Mode) (*ParsedGoFile, error)

	// Analyze runs the specified analyzers on the given packages at this snapshot.
	//
	// If the provided tracker is non-nil, it may be used to report progress of
	// the analysis pass.
	Analyze(ctx context.Context, pkgIDs map[PackageID]unit, analyzers []*settings.Analyzer, tracker *progress.Tracker) ([]*Diagnostic, error)

	// RunGoCommandDirect runs the given `go` command. Verb, Args, and
	// WorkingDir must be specified.
	//
	// TODO(rfindley): eliminate this from the Snapshot interface.
	RunGoCommandDirect(ctx context.Context, mode InvocationFlags, inv *gocommand.Invocation) (*bytes.Buffer, error)

	// RunProcessEnvFunc runs fn with the process env for this snapshot's view.
	// Note: the process env contains cached module and filesystem state.
	RunProcessEnvFunc(ctx context.Context, fn func(context.Context, *imports.Options) error) error

	// ModFiles are the go.mod files enclosed in the snapshot's view and known
	// to the snapshot.
	ModFiles() []protocol.DocumentURI

	// ParseMod is used to parse go.mod files.
	ParseMod(ctx context.Context, fh file.Handle) (*ParsedModule, error)

	// ParseWork is used to parse go.work files.
	ParseWork(ctx context.Context, fh file.Handle) (*ParsedWorkFile, error)

	// BuiltinFile returns information about the special builtin package.
	BuiltinFile(ctx context.Context) (*ParsedGoFile, error)

	// Symbols returns all symbols in the snapshot.
	//
	// If workspaceOnly is set, this only includes symbols from files in a
	// workspace package. Otherwise, it returns symbols from all loaded packages.
	Symbols(ctx context.Context, workspaceOnly bool) (map[protocol.DocumentURI][]Symbol, error)

	// -- package metadata --

	// ReverseDependencies returns a new mapping whose entries are
	// the ID and Metadata of each package in the workspace that
	// directly or transitively depend on the package denoted by id,
	// excluding id itself.
	ReverseDependencies(ctx context.Context, id PackageID, transitive bool) (map[PackageID]*Metadata, error)

	// WorkspaceMetadata returns a new, unordered slice containing
	// metadata for all ordinary and test packages (but not
	// intermediate test variants) in the workspace.
	//
	// The workspace is the set of modules typically defined by a
	// go.work file. It is not transitively closed: for example,
	// the standard library is not usually part of the workspace
	// even though every module in the workspace depends on it.
	//
	// Operations that must inspect all the dependencies of the
	// workspace packages should instead use AllMetadata.
	WorkspaceMetadata(ctx context.Context) ([]*Metadata, error)

	// AllMetadata returns a new unordered array of metadata for
	// all packages known to this snapshot, which includes the
	// packages of all workspace modules plus their transitive
	// import dependencies.
	//
	// It may also contain ad-hoc packages for standalone files.
	// It includes all test variants.
	AllMetadata(ctx context.Context) ([]*Metadata, error)

	// Metadata returns the metadata for the specified package,
	// or nil if it was not found.
	Metadata(id PackageID) *Metadata

	// MetadataForFile returns a new slice containing metadata for each
	// package containing the Go file identified by uri, ordered by the
	// number of CompiledGoFiles (i.e. "narrowest" to "widest" package),
	// and secondarily by IsIntermediateTestVariant (false < true).
	// The result may include tests and intermediate test variants of
	// importable packages.
	// It returns an error if the context was cancelled.
	MetadataForFile(ctx context.Context, uri protocol.DocumentURI) ([]*Metadata, error)

	// -- package type-checking --

	// TypeCheck parses and type-checks the specified packages,
	// and returns them in the same order as the ids.
	// The resulting packages' types may belong to different importers,
	// so types from different packages are incommensurable.
	//
	// In general, clients should never need to type-checked
	// syntax for an intermediate test variant (ITV) package.
	// Callers should apply RemoveIntermediateTestVariants (or
	// equivalent) before this method, or any of the potentially
	// type-checking methods below.
	TypeCheck(ctx context.Context, ids ...PackageID) ([]Package, error)

	// PackageDiagnostics returns diagnostics for files contained in specified
	// packages.
	//
	// If these diagnostics cannot be loaded from cache, the requested packages
	// may be type-checked.
	PackageDiagnostics(ctx context.Context, ids ...PackageID) (map[protocol.DocumentURI][]*Diagnostic, error)

	// References returns cross-references indexes for the specified packages.
	//
	// If these indexes cannot be loaded from cache, the requested packages may
	// be type-checked.
	References(ctx context.Context, ids ...PackageID) ([]XrefIndex, error)

	// MethodSets returns method-set indexes for the specified packages.
	//
	// If these indexes cannot be loaded from cache, the requested packages may
	// be type-checked.
	MethodSets(ctx context.Context, ids ...PackageID) ([]*methodsets.Index, error)

	// IsGoPrivatePath reports whether target is a private import path, as identified
	// by the GOPRIVATE environment variable.
	IsGoPrivatePath(path string) bool

	// Folder returns the folder with which this view was created.
	Folder() protocol.DocumentURI

	// GoVersionString returns the go version string configured for this view.
	// Unlike [GoVersion], this encodes the minor version and commit hash information.
	GoVersionString() string
}

// NarrowestMetadataForFile returns metadata for the narrowest package
// (the one with the fewest files) that encloses the specified file.
// The result may be a test variant, but never an intermediate test variant.
func NarrowestMetadataForFile(ctx context.Context, snapshot Snapshot, uri protocol.DocumentURI) (*Metadata, error) {
	metas, err := snapshot.MetadataForFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	RemoveIntermediateTestVariants(&metas)
	if len(metas) == 0 {
		return nil, fmt.Errorf("no package metadata for file %s", uri)
	}
	return metas[0], nil
}

type XrefIndex interface {
	Lookup(targets map[PackagePath]map[objectpath.Path]struct{}) (locs []protocol.Location)
}

// NarrowestPackageForFile is a convenience function that selects the narrowest
// non-ITV package to which this file belongs, type-checks it in the requested
// mode (full or workspace), and returns it, along with the parse tree of that
// file.
//
// The "narrowest" package is the one with the fewest number of files that
// includes the given file. This solves the problem of test variants, as the
// test will have more files than the non-test package.
//
// An intermediate test variant (ITV) package has identical source to a regular
// package but resolves imports differently. gopls should never need to
// type-check them.
//
// Type-checking is expensive. Call snapshot.ParseGo if all you need is a parse
// tree, or snapshot.MetadataForFile if you only need metadata.
func NarrowestPackageForFile(ctx context.Context, snapshot Snapshot, uri protocol.DocumentURI) (Package, *ParsedGoFile, error) {
	return selectPackageForFile(ctx, snapshot, uri, func(metas []*Metadata) *Metadata { return metas[0] })
}

// WidestPackageForFile is a convenience function that selects the widest
// non-ITV package to which this file belongs, type-checks it in the requested
// mode (full or workspace), and returns it, along with the parse tree of that
// file.
//
// The "widest" package is the one with the most number of files that includes
// the given file. Which is the test variant if one exists.
//
// An intermediate test variant (ITV) package has identical source to a regular
// package but resolves imports differently. gopls should never need to
// type-check them.
//
// Type-checking is expensive. Call snapshot.ParseGo if all you need is a parse
// tree, or snapshot.MetadataForFile if you only need metadata.
func WidestPackageForFile(ctx context.Context, snapshot Snapshot, uri protocol.DocumentURI) (Package, *ParsedGoFile, error) {
	return selectPackageForFile(ctx, snapshot, uri, func(metas []*Metadata) *Metadata { return metas[len(metas)-1] })
}

func selectPackageForFile(ctx context.Context, snapshot Snapshot, uri protocol.DocumentURI, selector func([]*Metadata) *Metadata) (Package, *ParsedGoFile, error) {
	metas, err := snapshot.MetadataForFile(ctx, uri)
	if err != nil {
		return nil, nil, err
	}
	RemoveIntermediateTestVariants(&metas)
	if len(metas) == 0 {
		return nil, nil, fmt.Errorf("no package metadata for file %s", uri)
	}
	md := selector(metas)
	pkgs, err := snapshot.TypeCheck(ctx, md.ID)
	if err != nil {
		return nil, nil, err
	}
	pkg := pkgs[0]
	pgf, err := pkg.File(uri)
	if err != nil {
		return nil, nil, err // "can't happen"
	}
	return pkg, pgf, err
}

// InvocationFlags represents the settings of a particular go command invocation.
// It is a mode, plus a set of flag bits.
type InvocationFlags int

const (
	// Normal is appropriate for commands that might be run by a user and don't
	// deliberately modify go.mod files, e.g. `go test`.
	Normal InvocationFlags = iota
	// WriteTemporaryModFile is for commands that need information from a
	// modified version of the user's go.mod file, e.g. `go mod tidy` used to
	// generate diagnostics.
	WriteTemporaryModFile
	// LoadWorkspace is for packages.Load, and other operations that should
	// consider the whole workspace at once.
	LoadWorkspace
	// AllowNetwork is a flag bit that indicates the invocation should be
	// allowed to access the network.
	AllowNetwork InvocationFlags = 1 << 10
)

func (m InvocationFlags) Mode() InvocationFlags {
	return m & (AllowNetwork - 1)
}

func (m InvocationFlags) AllowNetwork() bool {
	return m&AllowNetwork != 0
}

// A FileSource maps URIs to FileHandles.
type FileSource interface {
	// ReadFile returns the FileHandle for a given URI, either by
	// reading the content of the file or by obtaining it from a cache.
	//
	// Invariant: ReadFile must only return an error in the case of context
	// cancellation. If ctx.Err() is nil, the resulting error must also be nil.
	ReadFile(ctx context.Context, uri protocol.DocumentURI) (file.Handle, error)
}

type ParsedGoFile = parsego.File

// A ParsedModule contains the results of parsing a go.mod file.
type ParsedModule struct {
	URI         protocol.DocumentURI
	File        *modfile.File
	Mapper      *protocol.Mapper
	ParseErrors []*Diagnostic
}

// A ParsedWorkFile contains the results of parsing a go.work file.
type ParsedWorkFile struct {
	URI         protocol.DocumentURI
	File        *modfile.WorkFile
	Mapper      *protocol.Mapper
	ParseErrors []*Diagnostic
}

// A TidiedModule contains the results of running `go mod tidy` on a module.
type TidiedModule struct {
	// Diagnostics representing changes made by `go mod tidy`.
	Diagnostics []*Diagnostic
	// The bytes of the go.mod file after it was tidied.
	TidiedContent []byte
}

// RemoveIntermediateTestVariants removes intermediate test variants, modifying the array.
// We use a pointer to a slice make it impossible to forget to use the result.
func RemoveIntermediateTestVariants(pmetas *[]*Metadata) {
	metas := *pmetas
	res := metas[:0]
	for _, m := range metas {
		if !m.IsIntermediateTestVariant() {
			res = append(res, m)
		}
	}
	*pmetas = res
}

const (
	ParseHeader = parsego.ParseHeader
	ParseFull   = parsego.ParseFull
)

type (
	PackageID   = metadata.PackageID
	PackagePath = metadata.PackagePath
	PackageName = metadata.PackageName
	ImportPath  = metadata.ImportPath
	Metadata    = metadata.Metadata
)

// Package represents a Go package that has been parsed and type-checked.
//
// By design, there is no way to reach from a Package to the Package
// representing one of its dependencies.
//
// Callers must not assume that two Packages share the same
// token.FileSet or types.Importer and thus have commensurable
// token.Pos values or types.Objects. Instead, use stable naming
// schemes, such as (URI, byte offset) for positions, or (PackagePath,
// objectpath.Path) for exported declarations.
type Package interface {
	Metadata() *Metadata

	// Results of parsing:
	FileSet() *token.FileSet
	CompiledGoFiles() []*ParsedGoFile // (borrowed)
	File(uri protocol.DocumentURI) (*ParsedGoFile, error)
	GetSyntax() []*ast.File // (borrowed)
	GetParseErrors() []scanner.ErrorList

	// Results of type checking:
	GetTypes() *types.Package
	GetTypeErrors() []types.Error
	GetTypesInfo() *types.Info
	DependencyTypes(PackagePath) *types.Package // nil for indirect dependency of no consequence
	DiagnosticsForFile(ctx context.Context, uri protocol.DocumentURI) ([]*Diagnostic, error)
}

type unit = struct{}

// A CriticalError is a workspace-wide error that generally prevents gopls from
// functioning correctly. In the presence of critical errors, other diagnostics
// in the workspace may not make sense.
type CriticalError struct {
	// MainError is the primary error. Must be non-nil.
	MainError error

	// Diagnostics contains any supplemental (structured) diagnostics.
	Diagnostics []*Diagnostic
}

// An Diagnostic corresponds to an LSP Diagnostic.
// https://microsoft.github.io/language-server-protocol/specification#diagnostic
type Diagnostic struct {
	URI      protocol.DocumentURI // of diagnosed file (not diagnostic documentation)
	Range    protocol.Range
	Severity protocol.DiagnosticSeverity
	Code     string
	CodeHref string

	// Source is a human-readable description of the source of the error.
	// Diagnostics generated by an analysis.Analyzer set it to Analyzer.Name.
	Source DiagnosticSource

	Message string

	Tags    []protocol.DiagnosticTag
	Related []protocol.DiagnosticRelatedInformation

	// Fields below are used internally to generate quick fixes. They aren't
	// part of the LSP spec and historically didn't leave the server.
	//
	// Update(2023-05): version 3.16 of the LSP spec included support for the
	// Diagnostic.data field, which holds arbitrary data preserved in the
	// diagnostic for codeAction requests. This field allows bundling additional
	// information for quick-fixes, and gopls can (and should) use this
	// information to avoid re-evaluating diagnostics in code-action handlers.
	//
	// In order to stage this transition incrementally, the 'BundledFixes' field
	// may store a 'bundled' (=json-serialized) form of the associated
	// SuggestedFixes. Not all diagnostics have their fixes bundled.
	BundledFixes   *json.RawMessage
	SuggestedFixes []SuggestedFix
}

func (d *Diagnostic) String() string {
	return fmt.Sprintf("%v: %s", d.Range, d.Message)
}

type DiagnosticSource string

const (
	UnknownError             DiagnosticSource = "<Unknown source>"
	ListError                DiagnosticSource = "go list"
	ParseError               DiagnosticSource = "syntax"
	TypeError                DiagnosticSource = "compiler"
	ModTidyError             DiagnosticSource = "go mod tidy"
	OptimizationDetailsError DiagnosticSource = "optimizer details"
	UpgradeNotification      DiagnosticSource = "upgrade available"
	Vulncheck                DiagnosticSource = "vulncheck imports"
	Govulncheck              DiagnosticSource = "govulncheck"
	TemplateError            DiagnosticSource = "template"
	WorkFileError            DiagnosticSource = "go.work file"
	ConsistencyInfo          DiagnosticSource = "consistency"
)

func AnalyzerErrorKind(name string) DiagnosticSource {
	return DiagnosticSource(name)
}
