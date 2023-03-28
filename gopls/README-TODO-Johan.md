# Johan's invert-if-statement plan

## Principles

Ticket here: <https://github.com/golang/vscode-go/issues/2557>

* [Follow the contribution guidelines](doc/contributing.md)
* Run tests inside the `gopls` directory: `go test ./...`

Run our tests: `go test golang.org/x/tools/gopls/internal/lsp`

## TODO

* Figure out why `go test golang.org/x/tools/gopls/internal/lsp` complains about
  `invertifcondition/a.go:9:26: pattern if len(os.args) > 2 did not match`
* Make the test cases valid. By putting the right thing in our `.golden` file,
  or by splitting up the test cases or something. Maybe both?
* Give the user a way to actually invert the condition
* `git diff origin/master` and ensure we're looking good
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
* Move test cases into `internal/lsp/testdata/invertifcondition`
* Ensure the newly added test cases are run on `go test ./...`
* Figure out how to run only our test cases
  * `go test golang.org/x/tools/gopls/internal/lsp`
* Move `invertifcondition.go` next to `extract.go`
* Rewrite `invertifcondition.go` so it's triggered the same way as `extract.go`
