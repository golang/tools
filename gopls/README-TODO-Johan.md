# Johan's invert-if-statement plan

## Principles

Ticket here: <https://github.com/golang/vscode-go/issues/2557>

* [Follow the contribution guidelines](doc/contributing.md)
* Run tests inside the `gopls` directory: `go test ./...`

Code lives [here](internal/lsp/analysis/invertifcondition/invertifcondition.go).

## TODO

* Move everything next to `extract.go`.
  * Inversion logic
  * Test cases
* Give the user a way to actually invert the condition
* Integrate with the rest of the LSP
* Remove this file
* Make a PR

### Done

* Figure out where to put tests for the new functionality
  * Inspired (arbitrarily) from `internal/lsp/analysis/fillstruct/fillstruct.go`
  * Let's just add a new neighboring directory there and start writing tests
* Write tests in `internal/lsp/analysis/invertifcondition/`
* Add test cases in
  `internal/lsp/analysis/invertifcondition/testdata/src/a/a.go`, like in its
  neighboring analysis directories
* Add diagnostic for the right if statements only
* Realize we should be doing what `extract.go` is doing but for inverting if
  conditions. What we're doing now is adding diagnostics, and this is not a
  diagnostic.
