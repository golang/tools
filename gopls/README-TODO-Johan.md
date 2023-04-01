# Johan's invert-if-statement plan

## Principles

Ticket here: <https://github.com/golang/vscode-go/issues/2557>

* [Follow the contribution guidelines](doc/contributing.md)
* Run tests inside the `gopls` directory: `go test ./...`

Run our tests:
```
go test golang.org/x/tools/gopls/internal/lsp -test.run TestLSP/Modules/SuggestedFix
```

Or for a somewhat more complete version:
```
go test golang.org/x/tools/gopls/internal/lsp
```

## TODO

* Put the right contents in our `iic.go.golden` file
* Actually start inverting if conditions in `invertifcondition.go`
* Make the test cases valid. By putting the right thing in our `.golden` file,
  or by splitting up the test cases or something. Maybe both?
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
* Figure out why `go test golang.org/x/tools/gopls/internal/lsp` complains about
  `invertifcondition/a.go:9:26: pattern if len(os.args) > 2 did not match`
* Figure out why `go test golang.org/x/tools/gopls/internal/lsp -test.run
  TestLSP/Modules/SuggestedFix` complains about not getting any code actions.
  Maybe by looking how `extractionFixes()` is set up in `code_action.go`? Run
  `TestLSP` in a debugger over and over!
* Figure out why we get one `got 0 code actions` when running the tests
* Give the user a way to actually invert the condition
* Make the test cases care about our `a.go.golden` file
