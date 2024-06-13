# Commands

This document describes the LSP-level commands supported by `gopls`. They cannot be invoked directly by users, and all the details are subject to change, so nobody should rely on this information.

<!-- BEGIN Commands: DO NOT MANUALLY EDIT THIS SECTION -->
## `gopls.add_dependency`: **Add a dependency**

Adds a dependency to the go.mod file for a module.

Args:

```
{
	// The go.mod file URI.
	"URI": string,
	// Additional args to pass to the go command.
	"GoCmdArgs": []string,
	// Whether to add a require directive.
	"AddRequire": bool,
}
```

## `gopls.add_import`: **Add an import**

Ask the server to add an import path to a given Go file.  The method will
call applyEdit on the client so that clients don't have to apply the edit
themselves.

Args:

```
{
	// ImportPath is the target import path that should
	// be added to the URI file
	"ImportPath": string,
	// URI is the file that the ImportPath should be
	// added to
	"URI": string,
}
```

## `gopls.add_telemetry_counters`: **Update the given telemetry counters**

Gopls will prepend "fwd/" to all the counters updated using this command
to avoid conflicts with other counters gopls collects.

Args:

```
{
	// Names and Values must have the same length.
	"Names": []string,
	"Values": []int64,
}
```

## `gopls.apply_fix`: **Apply a fix**

Applies a fix to a region of source code.

Args:

```
{
	// The name of the fix to apply.
	//
	// For fixes suggested by analyzers, this is a string constant
	// advertised by the analyzer that matches the Category of
	// the analysis.Diagnostic with a SuggestedFix containing no edits.
	//
	// For fixes suggested by code actions, this is a string agreed
	// upon by the code action and golang.ApplyFix.
	"Fix": string,
	// The file URI for the document to fix.
	"URI": string,
	// The document range to scan for fixes.
	"Range": {
		"start": {
			"line": uint32,
			"character": uint32,
		},
		"end": {
			"line": uint32,
			"character": uint32,
		},
	},
	// Whether to resolve and return the edits.
	"ResolveEdits": bool,
}
```

Result:

```
{
	// Holds changes to existing resources.
	"changes": map[golang.org/x/tools/gopls/internal/protocol.DocumentURI][]golang.org/x/tools/gopls/internal/protocol.TextEdit,
	// Depending on the client capability `workspace.workspaceEdit.resourceOperations` document changes
	// are either an array of `TextDocumentEdit`s to express changes to n different text documents
	// where each text document edit addresses a specific version of a text document. Or it can contain
	// above `TextDocumentEdit`s mixed with create, rename and delete file / folder operations.
	//
	// Whether a client supports versioned document edits is expressed via
	// `workspace.workspaceEdit.documentChanges` client capability.
	//
	// If a client neither supports `documentChanges` nor `workspace.workspaceEdit.resourceOperations` then
	// only plain `TextEdit`s using the `changes` property are supported.
	"documentChanges": []{
		"TextDocumentEdit": {
			"textDocument": { ... },
			"edits": { ... },
		},
		"CreateFile": {
			"kind": string,
			"uri": string,
			"options": { ... },
			"ResourceOperation": { ... },
		},
		"RenameFile": {
			"kind": string,
			"oldUri": string,
			"newUri": string,
			"options": { ... },
			"ResourceOperation": { ... },
		},
		"DeleteFile": {
			"kind": string,
			"uri": string,
			"options": { ... },
			"ResourceOperation": { ... },
		},
	},
	// A map of change annotations that can be referenced in `AnnotatedTextEdit`s or create, rename and
	// delete file / folder operations.
	//
	// Whether clients honor this property depends on the client capability `workspace.changeAnnotationSupport`.
	//
	// @since 3.16.0
	"changeAnnotations": map[string]golang.org/x/tools/gopls/internal/protocol.ChangeAnnotation,
}
```

## `gopls.assembly`: **Browse assembly listing of current function in a browser.**

This command opens a web-based disassembly listing of the
specified function symbol (plus any nested lambdas and defers).
The machine architecture is determined by the view.

Args:

```
string,
string,
string
```

## `gopls.change_signature`: **Perform a "change signature" refactoring**

This command is experimental, currently only supporting parameter removal.
Its signature will certainly change in the future (pun intended).

Args:

```
{
	"RemoveParameter": {
		"uri": string,
		"range": {
			"start": { ... },
			"end": { ... },
		},
	},
	// Whether to resolve and return the edits.
	"ResolveEdits": bool,
}
```

Result:

```
{
	// Holds changes to existing resources.
	"changes": map[golang.org/x/tools/gopls/internal/protocol.DocumentURI][]golang.org/x/tools/gopls/internal/protocol.TextEdit,
	// Depending on the client capability `workspace.workspaceEdit.resourceOperations` document changes
	// are either an array of `TextDocumentEdit`s to express changes to n different text documents
	// where each text document edit addresses a specific version of a text document. Or it can contain
	// above `TextDocumentEdit`s mixed with create, rename and delete file / folder operations.
	//
	// Whether a client supports versioned document edits is expressed via
	// `workspace.workspaceEdit.documentChanges` client capability.
	//
	// If a client neither supports `documentChanges` nor `workspace.workspaceEdit.resourceOperations` then
	// only plain `TextEdit`s using the `changes` property are supported.
	"documentChanges": []{
		"TextDocumentEdit": {
			"textDocument": { ... },
			"edits": { ... },
		},
		"CreateFile": {
			"kind": string,
			"uri": string,
			"options": { ... },
			"ResourceOperation": { ... },
		},
		"RenameFile": {
			"kind": string,
			"oldUri": string,
			"newUri": string,
			"options": { ... },
			"ResourceOperation": { ... },
		},
		"DeleteFile": {
			"kind": string,
			"uri": string,
			"options": { ... },
			"ResourceOperation": { ... },
		},
	},
	// A map of change annotations that can be referenced in `AnnotatedTextEdit`s or create, rename and
	// delete file / folder operations.
	//
	// Whether clients honor this property depends on the client capability `workspace.changeAnnotationSupport`.
	//
	// @since 3.16.0
	"changeAnnotations": map[string]golang.org/x/tools/gopls/internal/protocol.ChangeAnnotation,
}
```

## `gopls.check_upgrades`: **Check for upgrades**

Checks for module upgrades.

Args:

```
{
	// The go.mod file URI.
	"URI": string,
	// The modules to check.
	"Modules": []string,
}
```

## `gopls.diagnose_files`: **Cause server to publish diagnostics for the specified files.**

This command is needed by the 'gopls {check,fix}' CLI subcommands.

Args:

```
{
	"Files": []string,
}
```

## `gopls.doc`: **Browse package documentation.**

Opens the Go package documentation page for the current
package in a browser.

Args:

```
{
	"uri": string,
	"range": {
		"start": {
			"line": uint32,
			"character": uint32,
		},
		"end": {
			"line": uint32,
			"character": uint32,
		},
	},
}
```

## `gopls.edit_go_directive`: **Run go mod edit -go=version**

Runs `go mod edit -go=version` for a module.

Args:

```
{
	// Any document URI within the relevant module.
	"URI": string,
	// The version to pass to `go mod edit -go`.
	"Version": string,
}
```

## `gopls.fetch_vulncheck_result`: **Get known vulncheck result**

Fetch the result of latest vulnerability check (`govulncheck`).

Args:

```
{
	// The file URI.
	"URI": string,
}
```

Result:

```
map[golang.org/x/tools/gopls/internal/protocol.DocumentURI]*golang.org/x/tools/gopls/internal/vulncheck.Result
```

## `gopls.free_symbols`: **Browse free symbols referenced by the selection in a browser.**

This command is a query over a selected range of Go source
code. It reports the set of "free" symbols of the
selection: the set of symbols that are referenced within
the selection but are declared outside of it. This
information is useful for understanding at a glance what a
block of code depends on, perhaps as a precursor to
extracting it into a separate function.

Args:

```
string,
{
	"uri": string,
	"range": {
		"start": {
			"line": uint32,
			"character": uint32,
		},
		"end": {
			"line": uint32,
			"character": uint32,
		},
	},
}
```

## `gopls.gc_details`: **Toggle gc_details**

Toggle the calculation of gc annotations.

Args:

```
string
```

## `gopls.generate`: **Run go generate**

Runs `go generate` for a given directory.

Args:

```
{
	// URI for the directory to generate.
	"Dir": string,
	// Whether to generate recursively (go generate ./...)
	"Recursive": bool,
}
```

## `gopls.go_get_package`: **'go get' a package**

Runs `go get` to fetch a package.

Args:

```
{
	// Any document URI within the relevant module.
	"URI": string,
	// The package to go get.
	"Pkg": string,
	"AddRequire": bool,
}
```

## `gopls.list_imports`: **List imports of a file and its package**

Retrieve a list of imports in the given Go file, and the package it
belongs to.

Args:

```
{
	// The file URI.
	"URI": string,
}
```

Result:

```
{
	// Imports is a list of imports in the requested file.
	"Imports": []{
		"Path": string,
		"Name": string,
	},
	// PackageImports is a list of all imports in the requested file's package.
	"PackageImports": []{
		"Path": string,
	},
}
```

## `gopls.list_known_packages`: **List known packages**

Retrieve a list of packages that are importable from the given URI.

Args:

```
{
	// The file URI.
	"URI": string,
}
```

Result:

```
{
	// Packages is a list of packages relative
	// to the URIArg passed by the command request.
	// In other words, it omits paths that are already
	// imported or cannot be imported due to compiler
	// restrictions.
	"Packages": []string,
}
```

## `gopls.maybe_prompt_for_telemetry`: **Prompt user to enable telemetry**

Checks for the right conditions, and then prompts the user
to ask if they want to enable Go telemetry uploading. If
the user responds 'Yes', the telemetry mode is set to "on".

## `gopls.mem_stats`: **Fetch memory statistics**

Call runtime.GC multiple times and return memory statistics as reported by
runtime.MemStats.

This command is used for benchmarking, and may change in the future.

Result:

```
{
	"HeapAlloc": uint64,
	"HeapInUse": uint64,
	"TotalAlloc": uint64,
}
```

## `gopls.regenerate_cgo`: **Regenerate cgo**

Regenerates cgo definitions.

Args:

```
{
	// The file URI.
	"URI": string,
}
```

## `gopls.remove_dependency`: **Remove a dependency**

Removes a dependency from the go.mod file of a module.

Args:

```
{
	// The go.mod file URI.
	"URI": string,
	// The module path to remove.
	"ModulePath": string,
	// If the module is tidied apart from the one unused diagnostic, we can
	// run `go get module@none`, and then run `go mod tidy`. Otherwise, we
	// must make textual edits.
	"OnlyDiagnostic": bool,
}
```

## `gopls.reset_go_mod_diagnostics`: **Reset go.mod diagnostics**

Reset diagnostics in the go.mod file of a module.

Args:

```
{
	"URIArg": {
		"URI": string,
	},
	// Optional: source of the diagnostics to reset.
	// If not set, all resettable go.mod diagnostics will be cleared.
	"DiagnosticSource": string,
}
```

## `gopls.run_go_work_command`: **Run `go work [args...]`, and apply the resulting go.work**

edits to the current go.work file

Args:

```
{
	"ViewID": string,
	"InitFirst": bool,
	"Args": []string,
}
```

## `gopls.run_govulncheck`: **Run vulncheck**

Run vulnerability check (`govulncheck`).

This command is asynchronous; clients must wait for the 'end' progress notification.

Args:

```
{
	// Any document in the directory from which govulncheck will run.
	"URI": string,
	// Package pattern. E.g. "", ".", "./...".
	"Pattern": string,
}
```

Result:

```
{
	// Token holds the progress token for LSP workDone reporting of the vulncheck
	// invocation.
	"Token": interface{},
}
```

## `gopls.run_tests`: **Run test(s)**

Runs `go test` for a specific set of test or benchmark functions.

This command is asynchronous; clients must wait for the 'end' progress notification.

Args:

```
{
	// The test file containing the tests to run.
	"URI": string,
	// Specific test names to run, e.g. TestFoo.
	"Tests": []string,
	// Specific benchmarks to run, e.g. BenchmarkFoo.
	"Benchmarks": []string,
}
```

## `gopls.scan_imports`: **force a sychronous scan of the imports cache.**

This command is intended for use by gopls tests only.

## `gopls.start_debugging`: **Start the gopls debug server**

Start the gopls debug server if it isn't running, and return the debug
address.

Args:

```
{
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
	"Addr": string,
}
```

Result:

```
{
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
	"URLs": []string,
}
```

## `gopls.start_profile`: **Start capturing a profile of gopls' execution**

Start a new pprof profile. Before using the resulting file, profiling must
be stopped with a corresponding call to StopProfile.

This command is intended for internal use only, by the gopls benchmark
runner.

Args:

```
struct{}
```

Result:

```
struct{}
```

## `gopls.stop_profile`: **Stop an ongoing profile**

This command is intended for internal use only, by the gopls benchmark
runner.

Args:

```
struct{}
```

Result:

```
{
	// File is the profile file name.
	"File": string,
}
```

## `gopls.test`: **Run test(s) (legacy)**

Runs `go test` for a specific set of test or benchmark functions.

This command is asynchronous; wait for the 'end' progress notification.

This command is an alias for RunTests; the only difference
is the form of the parameters.

TODO(adonovan): eliminate it.

Args:

```
string,
[]string,
[]string
```

## `gopls.tidy`: **Run go mod tidy**

Runs `go mod tidy` for a module.

Args:

```
{
	// The file URIs.
	"URIs": []string,
}
```

## `gopls.toggle_gc_details`: **Toggle gc_details**

Toggle the calculation of gc annotations.

Args:

```
{
	// The file URI.
	"URI": string,
}
```

## `gopls.update_go_sum`: **Update go.sum**

Updates the go.sum file for a module.

Args:

```
{
	// The file URIs.
	"URIs": []string,
}
```

## `gopls.upgrade_dependency`: **Upgrade a dependency**

Upgrades a dependency in the go.mod file for a module.

Args:

```
{
	// The go.mod file URI.
	"URI": string,
	// Additional args to pass to the go command.
	"GoCmdArgs": []string,
	// Whether to add a require directive.
	"AddRequire": bool,
}
```

## `gopls.vendor`: **Run go mod vendor**

Runs `go mod vendor` for a module.

Args:

```
{
	// The file URI.
	"URI": string,
}
```

## `gopls.views`: **List current Views on the server.**

This command is intended for use by gopls tests only.

Result:

```
[]{
	"ID": string,
	"Type": string,
	"Root": string,
	"Folder": string,
	"EnvOverlay": []string,
}
```

## `gopls.workspace_stats`: **Fetch workspace statistics**

Query statistics about workspace builds, modules, packages, and files.

This command is intended for internal use only, by the gopls stats
command.

Result:

```
{
	"Files": {
		"Total": int,
		"Largest": int,
		"Errs": int,
	},
	"Views": []{
		"GoCommandVersion": string,
		"AllPackages": {
			"Packages": int,
			"LargestPackage": int,
			"CompiledGoFiles": int,
			"Modules": int,
		},
		"WorkspacePackages": {
			"Packages": int,
			"LargestPackage": int,
			"CompiledGoFiles": int,
			"Modules": int,
		},
		"Diagnostics": int,
	},
}
```

<!-- END Commands: DO NOT MANUALLY EDIT THIS SECTION -->
