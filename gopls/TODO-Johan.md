# Johan's invert-if-statement plan

## Principles

Ticket here: <https://github.com/golang/vscode-go/issues/2557>

* [Follow the contribution guidelines](doc/contributing.md)
* Run tests inside the `gopls` directory: `go test ./...`

## TODO

* Write tests in `internal/lsp/analysis/invertifstatement/`
* Implement functionality...
* Integrate with the rest of the LSP
* Remove this file
* Make a PR

### Done

* Figure out where to put tests for the new functionality
  * Inspired (arbitrarily) from `internal/lsp/analysis/fillstruct/fillstruct.go`
  * Let's just add a new neighboring directory there and start writing tests
