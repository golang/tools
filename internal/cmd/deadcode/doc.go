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

With the -json flag, the command prints an array of JSON Package
objects, as defined by this schema:

	type Package struct {
		Path  string
		Funcs []Function
	}

	type Function struct {
		Name      string // name (with package qualifier)
		RelName   string // name (sans package qualifier)
		Posn      string // position in form "filename:line:col"
		Generated bool   // function is declared in a generated .go file
	}

With the -format=template flag, the command executes the specified template
on each Package record. So, this template produces a result similar to the
default format:

	-format='{{println .Path}}{{range .Funcs}}{{printf "\t%s\n" .RelName}}{{end}}{{println}}'

And this template shows only the list of source positions of dead functions:

	-format='{{range .Funcs}}{{println .Posn}}{{end}}'

THIS TOOL IS EXPERIMENTAL and its interface may change.
At some point it may be published at cmd/deadcode.
In the meantime, please give us feedback at github.com/golang/go/issues.
*/
package main
