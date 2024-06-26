// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run -tags=generate generate.go

// Package command defines the interface provided by gopls for the
// workspace/executeCommand LSP request.
//
// This interface is fully specified by the Interface type, provided it
// conforms to the restrictions outlined in its doc string.
//
// Bindings for server-side command dispatch and client-side serialization are
// also provided by this package, via code generation.
package command

import (
	"context"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/vulncheck"
)

// Interface defines the interface gopls exposes for the
// workspace/executeCommand request.
//
// This interface is used to generate marshaling/unmarshaling code, dispatch,
// and documentation, and so has some additional restrictions:
//  1. All method arguments must be JSON serializable.
//  2. Methods must return either error or (T, error), where T is a
//     JSON serializable type.
//  3. The first line of the doc string is special.
//     Everything after the colon is considered the command 'Title'.
//
// The doc comment on each method is eventually published at
// https://github.com/golang/tools/blob/master/gopls/doc/commands.md,
// so please be consistent in using this form:
//
//	Command: Capitalized verb phrase with no period
//
//	Longer description here...
type Interface interface {
	// ApplyFix: Apply a fix
	//
	// Applies a fix to a region of source code.
	ApplyFix(context.Context, ApplyFixArgs) (*protocol.WorkspaceEdit, error)

	// Test: Run test(s) (legacy)
	//
	// Runs `go test` for a specific set of test or benchmark functions.
	//
	// This command is asynchronous; wait for the 'end' progress notification.
	//
	// This command is an alias for RunTests; the only difference
	// is the form of the parameters.
	//
	// TODO(adonovan): eliminate it.
	Test(context.Context, protocol.DocumentURI, []string, []string) error

	// Test: Run test(s)
	//
	// Runs `go test` for a specific set of test or benchmark functions.
	//
	// This command is asynchronous; clients must wait for the 'end' progress notification.
	RunTests(context.Context, RunTestsArgs) error

	// Generate: Run go generate
	//
	// Runs `go generate` for a given directory.
	Generate(context.Context, GenerateArgs) error

	// Doc: Browse package documentation.
	//
	// Opens the Go package documentation page for the current
	// package in a browser.
	Doc(context.Context, protocol.Location) error

	// RegenerateCgo: Regenerate cgo
	//
	// Regenerates cgo definitions.
	RegenerateCgo(context.Context, URIArg) error

	// Tidy: Run go mod tidy
	//
	// Runs `go mod tidy` for a module.
	Tidy(context.Context, URIArgs) error

	// Vendor: Run go mod vendor
	//
	// Runs `go mod vendor` for a module.
	Vendor(context.Context, URIArg) error

	// EditGoDirective: Run go mod edit -go=version
	//
	// Runs `go mod edit -go=version` for a module.
	EditGoDirective(context.Context, EditGoDirectiveArgs) error

	// UpdateGoSum: Update go.sum
	//
	// Updates the go.sum file for a module.
	UpdateGoSum(context.Context, URIArgs) error

	// CheckUpgrades: Check for upgrades
	//
	// Checks for module upgrades.
	CheckUpgrades(context.Context, CheckUpgradesArgs) error

	// AddDependency: Add a dependency
	//
	// Adds a dependency to the go.mod file for a module.
	AddDependency(context.Context, DependencyArgs) error

	// UpgradeDependency: Upgrade a dependency
	//
	// Upgrades a dependency in the go.mod file for a module.
	UpgradeDependency(context.Context, DependencyArgs) error

	// RemoveDependency: Remove a dependency
	//
	// Removes a dependency from the go.mod file of a module.
	RemoveDependency(context.Context, RemoveDependencyArgs) error

	// ResetGoModDiagnostics: Reset go.mod diagnostics
	//
	// Reset diagnostics in the go.mod file of a module.
	ResetGoModDiagnostics(context.Context, ResetGoModDiagnosticsArgs) error

	// GoGetPackage: 'go get' a package
	//
	// Runs `go get` to fetch a package.
	GoGetPackage(context.Context, GoGetPackageArgs) error

	// GCDetails: Toggle gc_details
	//
	// Toggle the calculation of gc annotations.
	GCDetails(context.Context, protocol.DocumentURI) error

	// TODO: deprecate GCDetails in favor of ToggleGCDetails below.

	// ToggleGCDetails: Toggle gc_details
	//
	// Toggle the calculation of gc annotations.
	ToggleGCDetails(context.Context, URIArg) error

	// ListKnownPackages: List known packages
	//
	// Retrieve a list of packages that are importable from the given URI.
	ListKnownPackages(context.Context, URIArg) (ListKnownPackagesResult, error)

	// ListImports: List imports of a file and its package
	//
	// Retrieve a list of imports in the given Go file, and the package it
	// belongs to.
	ListImports(context.Context, URIArg) (ListImportsResult, error)

	// AddImport: Add an import
	//
	// Ask the server to add an import path to a given Go file.  The method will
	// call applyEdit on the client so that clients don't have to apply the edit
	// themselves.
	AddImport(context.Context, AddImportArgs) error

	// ExtractToNewFile: Move selected declarations to a new file
	//
	// Used by the code action of the same name.
	ExtractToNewFile(context.Context, protocol.Location) error

	// StartDebugging: Start the gopls debug server
	//
	// Start the gopls debug server if it isn't running, and return the debug
	// address.
	StartDebugging(context.Context, DebuggingArgs) (DebuggingResult, error)

	// StartProfile: Start capturing a profile of gopls' execution
	//
	// Start a new pprof profile. Before using the resulting file, profiling must
	// be stopped with a corresponding call to StopProfile.
	//
	// This command is intended for internal use only, by the gopls benchmark
	// runner.
	StartProfile(context.Context, StartProfileArgs) (StartProfileResult, error)

	// StopProfile: Stop an ongoing profile
	//
	// This command is intended for internal use only, by the gopls benchmark
	// runner.
	StopProfile(context.Context, StopProfileArgs) (StopProfileResult, error)

	// RunGovulncheck: Run vulncheck
	//
	// Run vulnerability check (`govulncheck`).
	//
	// This command is asynchronous; clients must wait for the 'end' progress notification.
	RunGovulncheck(context.Context, VulncheckArgs) (RunVulncheckResult, error)

	// FetchVulncheckResult: Get known vulncheck result
	//
	// Fetch the result of latest vulnerability check (`govulncheck`).
	FetchVulncheckResult(context.Context, URIArg) (map[protocol.DocumentURI]*vulncheck.Result, error)

	// MemStats: Fetch memory statistics
	//
	// Call runtime.GC multiple times and return memory statistics as reported by
	// runtime.MemStats.
	//
	// This command is used for benchmarking, and may change in the future.
	MemStats(context.Context) (MemStatsResult, error)

	// WorkspaceStats: Fetch workspace statistics
	//
	// Query statistics about workspace builds, modules, packages, and files.
	//
	// This command is intended for internal use only, by the gopls stats
	// command.
	WorkspaceStats(context.Context) (WorkspaceStatsResult, error)

	// RunGoWorkCommand: Run `go work [args...]`, and apply the resulting go.work
	// edits to the current go.work file
	RunGoWorkCommand(context.Context, RunGoWorkArgs) error

	// AddTelemetryCounters: Update the given telemetry counters
	//
	// Gopls will prepend "fwd/" to all the counters updated using this command
	// to avoid conflicts with other counters gopls collects.
	AddTelemetryCounters(context.Context, AddTelemetryCountersArgs) error

	// MaybePromptForTelemetry: Prompt user to enable telemetry
	//
	// Checks for the right conditions, and then prompts the user
	// to ask if they want to enable Go telemetry uploading. If
	// the user responds 'Yes', the telemetry mode is set to "on".
	MaybePromptForTelemetry(context.Context) error

	// ChangeSignature: Perform a "change signature" refactoring
	//
	// This command is experimental, currently only supporting parameter removal.
	// Its signature will certainly change in the future (pun intended).
	ChangeSignature(context.Context, ChangeSignatureArgs) (*protocol.WorkspaceEdit, error)

	// DiagnoseFiles: Cause server to publish diagnostics for the specified files.
	//
	// This command is needed by the 'gopls {check,fix}' CLI subcommands.
	DiagnoseFiles(context.Context, DiagnoseFilesArgs) error

	// Views: List current Views on the server.
	//
	// This command is intended for use by gopls tests only.
	Views(context.Context) ([]View, error)

	// FreeSymbols: Browse free symbols referenced by the selection in a browser.
	//
	// This command is a query over a selected range of Go source
	// code. It reports the set of "free" symbols of the
	// selection: the set of symbols that are referenced within
	// the selection but are declared outside of it. This
	// information is useful for understanding at a glance what a
	// block of code depends on, perhaps as a precursor to
	// extracting it into a separate function.
	FreeSymbols(ctx context.Context, viewID string, loc protocol.Location) error

	// Assembly: Browse assembly listing of current function in a browser.
	//
	// This command opens a web-based disassembly listing of the
	// specified function symbol (plus any nested lambdas and defers).
	// The machine architecture is determined by the view.
	Assembly(_ context.Context, viewID, packageID, symbol string) error

	// ScanImports: force a sychronous scan of the imports cache.
	//
	// This command is intended for use by gopls tests only.
	ScanImports(context.Context) error
}

type RunTestsArgs struct {
	// The test file containing the tests to run.
	URI protocol.DocumentURI

	// Specific test names to run, e.g. TestFoo.
	Tests []string

	// Specific benchmarks to run, e.g. BenchmarkFoo.
	Benchmarks []string
}

type GenerateArgs struct {
	// URI for the directory to generate.
	Dir protocol.DocumentURI

	// Whether to generate recursively (go generate ./...)
	Recursive bool
}

// TODO(rFindley): document the rest of these once the docgen is fleshed out.

type ApplyFixArgs struct {
	// The name of the fix to apply.
	//
	// For fixes suggested by analyzers, this is a string constant
	// advertised by the analyzer that matches the Category of
	// the analysis.Diagnostic with a SuggestedFix containing no edits.
	//
	// For fixes suggested by code actions, this is a string agreed
	// upon by the code action and golang.ApplyFix.
	Fix string

	// The file URI for the document to fix.
	URI protocol.DocumentURI
	// The document range to scan for fixes.
	Range protocol.Range
	// Whether to resolve and return the edits.
	ResolveEdits bool
}

type URIArg struct {
	// The file URI.
	URI protocol.DocumentURI
}

type URIArgs struct {
	// The file URIs.
	URIs []protocol.DocumentURI
}

type CheckUpgradesArgs struct {
	// The go.mod file URI.
	URI protocol.DocumentURI
	// The modules to check.
	Modules []string
}

type DependencyArgs struct {
	// The go.mod file URI.
	URI protocol.DocumentURI
	// Additional args to pass to the go command.
	GoCmdArgs []string
	// Whether to add a require directive.
	AddRequire bool
}

type RemoveDependencyArgs struct {
	// The go.mod file URI.
	URI protocol.DocumentURI
	// The module path to remove.
	ModulePath string
	// If the module is tidied apart from the one unused diagnostic, we can
	// run `go get module@none`, and then run `go mod tidy`. Otherwise, we
	// must make textual edits.
	OnlyDiagnostic bool
}

type EditGoDirectiveArgs struct {
	// Any document URI within the relevant module.
	URI protocol.DocumentURI
	// The version to pass to `go mod edit -go`.
	Version string
}

type GoGetPackageArgs struct {
	// Any document URI within the relevant module.
	URI protocol.DocumentURI
	// The package to go get.
	Pkg        string
	AddRequire bool
}

type AddImportArgs struct {
	// ImportPath is the target import path that should
	// be added to the URI file
	ImportPath string
	// URI is the file that the ImportPath should be
	// added to
	URI protocol.DocumentURI
}

type ListKnownPackagesResult struct {
	// Packages is a list of packages relative
	// to the URIArg passed by the command request.
	// In other words, it omits paths that are already
	// imported or cannot be imported due to compiler
	// restrictions.
	Packages []string
}

type ListImportsResult struct {
	// Imports is a list of imports in the requested file.
	Imports []FileImport

	// PackageImports is a list of all imports in the requested file's package.
	PackageImports []PackageImport
}

type FileImport struct {
	// Path is the import path of the import.
	Path string
	// Name is the name of the import, e.g. `foo` in `import foo "strings"`.
	Name string
}

type PackageImport struct {
	// Path is the import path of the import.
	Path string
}

type DebuggingArgs struct {
	// Optional: the address (including port) for the debug server to listen on.
	// If not provided, the debug server will bind to "localhost:0", and the
	// full debug URL will be contained in the result.
	//
	// If there is more than one gopls instance along the serving path (i.e. you
	// are using a daemon), each gopls instance will attempt to start debugging.
	// If Addr specifies a port, only the daemon will be able to bind to that
	// port, and each intermediate gopls instance will fail to start debugging.
	// For this reason it is recommended not to specify a port (or equivalently,
	// to specify ":0").
	//
	// If the server was already debugging this field has no effect, and the
	// result will contain the previously configured debug URL(s).
	Addr string
}

type DebuggingResult struct {
	// The URLs to use to access the debug servers, for all gopls instances in
	// the serving path. For the common case of a single gopls instance (i.e. no
	// daemon), this will be exactly one address.
	//
	// In the case of one or more gopls instances forwarding the LSP to a daemon,
	// URLs will contain debug addresses for each server in the serving path, in
	// serving order. The daemon debug address will be the last entry in the
	// slice. If any intermediate gopls instance fails to start debugging, no
	// error will be returned but the debug URL for that server in the URLs slice
	// will be empty.
	URLs []string
}

// StartProfileArgs holds the arguments to the StartProfile command.
//
// It is a placeholder for future compatibility.
type StartProfileArgs struct {
}

// StartProfileResult holds the result of the StartProfile command.
//
// It is a placeholder for future compatibility.
type StartProfileResult struct {
}

// StopProfileArgs holds the arguments to the StopProfile command.
//
// It is a placeholder for future compatibility.
type StopProfileArgs struct {
}

// StopProfileResult holds the result to the StopProfile command.
type StopProfileResult struct {
	// File is the profile file name.
	File string
}

type ResetGoModDiagnosticsArgs struct {
	URIArg

	// Optional: source of the diagnostics to reset.
	// If not set, all resettable go.mod diagnostics will be cleared.
	DiagnosticSource string
}

type VulncheckArgs struct {
	// Any document in the directory from which govulncheck will run.
	URI protocol.DocumentURI

	// Package pattern. E.g. "", ".", "./...".
	Pattern string

	// TODO: -tests
}

// RunVulncheckResult holds the result of asynchronously starting the vulncheck
// command.
type RunVulncheckResult struct {
	// Token holds the progress token for LSP workDone reporting of the vulncheck
	// invocation.
	Token protocol.ProgressToken
}

// CallStack models a trace of function calls starting
// with a client function or method and ending with a
// call to a vulnerable symbol.
type CallStack []StackEntry

// StackEntry models an element of a call stack.
type StackEntry struct {
	// See golang.org/x/exp/vulncheck.StackEntry.

	// User-friendly representation of function/method names.
	// e.g. package.funcName, package.(recvType).methodName, ...
	Name string
	URI  protocol.DocumentURI
	Pos  protocol.Position // Start position. (0-based. Column is always 0)
}

// MemStatsResult holds selected fields from runtime.MemStats.
type MemStatsResult struct {
	HeapAlloc  uint64
	HeapInUse  uint64
	TotalAlloc uint64
}

// WorkspaceStatsResult returns information about the size and shape of the
// workspace.
type WorkspaceStatsResult struct {
	Files FileStats   // file stats for the cache
	Views []ViewStats // stats for each view in the session
}

// FileStats holds information about a set of files.
type FileStats struct {
	Total   int // total number of files
	Largest int // number of bytes in the largest file
	Errs    int // number of files that could not be read
}

// ViewStats holds information about a single View in the session.
type ViewStats struct {
	GoCommandVersion  string       // version of the Go command resolved for this view
	AllPackages       PackageStats // package info for all packages (incl. dependencies)
	WorkspacePackages PackageStats // package info for workspace packages
	Diagnostics       int          // total number of diagnostics in the workspace
}

// PackageStats holds information about a collection of packages.
type PackageStats struct {
	Packages        int // total number of packages
	LargestPackage  int // number of files in the largest package
	CompiledGoFiles int // total number of compiled Go files across all packages
	Modules         int // total number of unique modules
}

type RunGoWorkArgs struct {
	ViewID    string   // ID of the view to run the command from
	InitFirst bool     // Whether to run `go work init` first
	Args      []string // Args to pass to `go work`
}

// AddTelemetryCountersArgs holds the arguments to the AddCounters command
// that updates the telemetry counters.
type AddTelemetryCountersArgs struct {
	// Names and Values must have the same length.
	Names  []string // Name of counters.
	Values []int64  // Values added to the corresponding counters. Must be non-negative.
}

// ChangeSignatureArgs specifies a "change signature" refactoring to perform.
type ChangeSignatureArgs struct {
	RemoveParameter protocol.Location
	// Whether to resolve and return the edits.
	ResolveEdits bool
}

// DiagnoseFilesArgs specifies a set of files for which diagnostics are wanted.
type DiagnoseFilesArgs struct {
	Files []protocol.DocumentURI
}

// A View holds summary information about a cache.View.
type View struct {
	ID         string               // view ID (the index of this view among all views created)
	Type       string               // view type (via cache.ViewType.String)
	Root       protocol.DocumentURI // root dir of the view (e.g. containing go.mod or go.work)
	Folder     protocol.DocumentURI // workspace folder associated with the view
	EnvOverlay []string             // environment variable overrides
}
