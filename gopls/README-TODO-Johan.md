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

* Remove this file
* Make a PR, include the animated gif

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
* Put the right contents in our `.golden` files
* Actually start inverting if conditions in `invertifcondition.go`
* Figure out why tests are complaining about linefeed changes towards the end of
  the code
* Make the test cases valid. By putting the right thing in our `.golden` file,
  or by splitting up the test cases or something. Maybe both?
* Invert the actual condition (after we're done with switching places between
  the `if` and `else` blocks)
* Make an `else` removal test case where the new `if` block ends with a `return`
  statement
* Do `else` removal when the new `if` block ends with a `return` statement
* Pass the full test suite
* Remedy the FIXME we added in `invertifcondition.go`
* Test run our changes manually in VSCode
* `git diff -b origin/master` and ensure we're looking good
* Record an animated gif where we demonstrate inverting an `if` condition and
  losing the `else` block
