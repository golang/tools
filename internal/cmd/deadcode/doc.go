// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
The deadcode command reports unreachable functions in Go programs.

Usage: deadcode [flags] package...

The deadcode command loads a Go program from source then uses Rapid
Type Analysis (RTA) to build a call graph of all the functions
reachable from the program's main function. Any functions that are not
reachable are reported as dead code, grouped by package.

Packages are expressed in the notation of 'go list' (or other
underlying build system if you are using an alternative
golang.org/x/go/packages driver). Only executable (main) packages are
considered starting points for the analysis.

The -test flag causes it to analyze test executables too. Tests
sometimes make use of functions that would otherwise appear to be dead
code, and public API functions reported as dead with -test indicate
possible gaps in your test coverage. Bear in mind that an Example test
function without an "Output:" comment is merely documentation:
it is dead code, and does not contribute coverage.

The -filter flag restricts results to packages that match the provided
regular expression; its default value is the module name of the first
package. Use -filter= to display all results.

Example: show all dead code within the gopls module:

	$ deadcode -test golang.org/x/tools/gopls/...

The analysis can soundly analyze dynamic calls though func values,
interface methods, and reflection. However, it does not currently
understand the aliasing created by //go:linkname directives, so it
will fail to recognize that calls to a linkname-annotated function
with no body in fact dispatch to the function named in the annotation.
This may result in the latter function being spuriously reported as dead.

By default, the tool does not report dead functions in generated files,
as determined by the special comment described in
https://go.dev/s/generatedcode. Use the -generated flag to include them.

In any case, just because a function is reported as dead does not mean
it is unconditionally safe to delete it. For example, a dead function
may be referenced (by another dead function), and a dead method may be
required to satisfy an interface (that is never called).
Some judgement is required.

The analysis is valid only for a single GOOS/GOARCH/-tags configuration,
so a function reported as dead may be live in a different configuration.
Consider running the tool once for each configuration of interest.
Consider using a line-oriented output format (see below) to make it
easier to compute the intersection of results across all runs.

# Output

The command supports three output formats.

With no flags, the command prints dead functions grouped by package.

With the -json flag, the command prints an array of Package
objects, as defined by the JSON schema (see below).

With the -f=template flag, the command executes the specified template
on each Package record. So, this template produces a result similar to the
default format:

	-f='{{println .Path}}{{range .Funcs}}{{printf "\t%s\n" .RelName}}{{end}}{{println}}'

And this template shows only the list of source positions of dead functions:

	-f='{{range .Funcs}}{{println .Posn}}{{end}}'

# Why is a function not dead?

The -whylive=function flag explain why the named function is not dead
by showing an arbitrary shortest path to it from one of the main functions.
(To enumerate the functions in a program, or for more sophisticated
call graph queries, use golang.org/x/tools/cmd/callgraph.)

Fully static call paths are preferred over paths involving dynamic
edges, even if longer. Paths starting from a non-test package are
preferred over those from tests. Paths from main functions are
preferred over paths from init functions.

The result is a list of Edge objects (see JSON schema below).
Again, the -json and -f=template flags may be used to control
the formatting of the list of Edge objects.
The default format shows, for each edge in the path, whether the call
is static or dynamic, and its source line number. For example:

	$ deadcode -whylive="(*bytes.Buffer).String" -test ./internal/cmd/deadcode/...
	                  golang.org/x/tools/internal/cmd/deadcode.main
	 static@L0321 --> (*golang.org/x/tools/go/ssa.Function).RelString
	 static@L0428 --> (*golang.org/x/tools/go/ssa.Function).relMethod
	 static@L0452 --> golang.org/x/tools/go/ssa.relType
	 static@L0047 --> go/types.TypeString
	 static@L0051 --> (*bytes.Buffer).String

# JSON schema

	type Package struct {
		Path  string       // import path of package
		Funcs []Function   // list of dead functions within it
	}

	type Function struct {
		Name      string   // name (with package qualifier)
		RelName   string   // name (sans package qualifier)
		Posn      Position // file/line/column of function declaration
		Generated bool     // function is declared in a generated .go file
	}

	type Edge struct {
		Initial string     // initial entrypoint (main or init); first edge only
		Kind    string     // = static | dynamic
		Posn    Position   // file/line/column of call site
		Callee  string     // target of the call
	}

	type Position struct {
		File      string   // name of file
		Line, Col int      // line and byte index, both 1-based
	}

THIS TOOL IS EXPERIMENTAL and its interface may change.
At some point it may be published at cmd/deadcode.
In the meantime, please give us feedback at github.com/golang/go/issues.
*/
package main
