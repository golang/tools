# Go Tools

[![PkgGoDev](https://pkg.go.dev/badge/golang.org/x/tools)](https://pkg.go.dev/golang.org/x/tools)

This **Repository** offers the [`golang.org/x/tools`](https://pkg.go.dev/golang.org/x/tools) module, which includes a collection of tools and packages primarily designed for static analysis of Go programs.
Below are some of the tools available


It also contains the
[`golang.org/x/tools/gopls`](https://pkg.go.dev/golang.org/x/tools/gopls)
module, whose root package is a language-server protocol (LSP) server for Go.
An LSP server analyses the source code of a project and
responds to requests from a wide range of editors such as VSCode and
Vim, allowing them to support IDE-like functionality.

For further details about any specific package, please refer to the [`reference`](https://pkg.go.dev/golang.org/x/tools) 

<!-- List only packages of general interest below. -->

### Selected commands:

- `cmd/goimports` formats a Go program like `go fmt` and additionally
  inserts import statements for any packages required by the file
  after it is edited.
- `cmd/callgraph` prints the call graph of a Go program.
- `cmd/digraph` is a utility for manipulating directed graphs in textual notation.
- `cmd/stringer` generates declarations (including a `String` method) for "enum" types.
- `cmd/toolstash` is a utility to simplify working with multiple versions of the Go toolchain.

### Commands may be fetched with a `Command` 
```
go install golang.org/x/tools/cmd/goimports@latest
```

### Selected packages:

- `go/ssa` provides a static single-assignment form (SSA) intermediate
  representation (IR) for Go programs, similar to a typical compiler,
  for use by analysis tools.

- `go/packages` provides a simple interface for loading, parsing, and
  type checking a complete Go program from source code.

- `go/analysis` provides a framework for modular static analysis of Go
  programs.

- `go/callgraph` provides call graphs of Go programs using a variety
  of algorithms with different trade-offs.

- `go/ast/inspector` provides an optimized means of traversing a Go
  parse tree for use in analysis tools.

- `go/cfg` provides a simple control-flow graph (CFG) for a Go function.

- `go/gcexportdata` and `go/gccgoexportdata` read and write the binary
  files containing type information used by the standard and `gccgo` compilers.

- `go/types/objectpath` provides a stable naming scheme for named
  entities ("objects") in the `go/types` API.

- Numerous other packages provide more esoteric functionality.

<!-- Some that didn't make the cut:

golang.org/x/tools/benchmark/parse
golang.org/x/tools/go/ast/astutil
golang.org/x/tools/go/types/typeutil
golang.org/x/tools/playground
golang.org/x/tools/present
golang.org/x/tools/refactor/importgraph
golang.org/x/tools/refactor/rename
golang.org/x/tools/refactor/satisfy
golang.org/x/tools/txtar

-->

## Contributing

 - This repository uses Gerrit for code changes.
 
 - To learn how to submit changes, see [this](https://golang.org/doc/contribute.html)

## Issues

 - The main issue tracker for the tools repository is located at
https://github.com/golang/go/issues. Prefix your issue with "x/tools/(your
subdir):" in the subject line, so it is easy to find.

## JavaScript and CSS Formatting

 - This repository uses [prettier](https://prettier.io/) to format JS and CSS files.

 - The version of `prettier` used is 1.18.2.

## Note

 - Although not strictly enforced by CI, we highly encourage running all JS and CSS code through this before submitting changes. It helps maintain code quality and consistency in the project.
