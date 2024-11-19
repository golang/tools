# Gopls: Documentation for contributors

This documentation augments the general documentation for contributing to the
x/tools repository, described at the [repository root](../../CONTRIBUTING.md).

Contributions are welcome! However, development is fast moving,
and we are limited in our capacity to review contributions.
So, before sending a CL, please please please:

- **file an issue** for a bug or feature request, if one does not
  exist already. This allows us to identify redundant requests, or to
  merge a specific problem into a more general one, and to assess the
  importance of the problem.

- **claim it for yourself** by commenting on the issue or, if you are
  able, by assigning the issue to yourself. This helps us avoid two
  people working on the same problem.

- **propose an implementation plan** in the issue tracker for CLs of
  any complexity. It is much more efficient to discuss the plan at a
  high level before we start getting bogged down in the details of
  a code review.

When you send a CL, it should include:

- a **CL description** that summarizes the change,
  motivates why it is necessary,
  explains it at a high level,
  contrasts it with more obvious or simpler approaches, and
  links to relevant issues;
- **tests** (integration tests or marker tests);
- **documentation**, for new or modified features; and
- **release notes**, for new features or significant changes.

During code review, please address all reviewer comments.
Some comments result in straightforward code changes;
others demand a more complex response.
When a reviewer asks a question, the best response is
often not to respond to it directly, but to change the
code to avoid raising the question,
for example by making the code self-explanatory.
It's fine to disagree with a comment,
point out a reviewer's mistake,
or offer to address a comment in a follow-up change,
leaving a TODO comment in the current CL.
But please don't dismiss or quietly ignore a comment without action,
as it may lead reviewers to repeat themselves,
or to serious problems being neglected.

For more detail, see the Go project's
[contribution guidelines](https://golang.org/doc/contribute.html).

## Finding issues

All `gopls` issues are labeled as such (see the [`gopls` label][issue-gopls]).
Issues that are suitable for contributors are additionally tagged with the
[`help-wanted` label][issue-wanted].

Before you begin working on an issue, please leave a comment that you are
claiming it.

## Getting started

Most of the `gopls` logic is in the `golang.org/x/tools/gopls/internal`
directory. See [design/implementation.md](./design/implementation.md) for an overview of the code organization.

## Build

To build a version of `gopls` with your changes applied:

```bash
cd /path/to/tools/gopls
go install
```

To confirm that you are testing with the correct `gopls` version, check that
your `gopls` version looks like this:

```bash
$ gopls version
golang.org/x/tools/gopls master
    golang.org/x/tools/gopls@(devel)
```

## Getting help

The best way to contact the gopls team directly is via the
[#gopls-dev](https://app.slack.com/client/T029RQSE6/CRWSN9NCD) channel on the
gophers slack. Please feel free to ask any questions about your contribution or
about contributing in general.


## Error handling

It is important for the user experience that, whenever practical,
minor logic errors in a particular feature don't cause the server to
crash.

The representation of a Go program is complex. The import graph of
package metadata, the syntax trees of parsed files, and their
associated type information together form a huge API surface area.
Even when the input is valid, there are many edge cases to consider,
and this grows by an order of magnitude when you consider missing
imports, parse errors, and type errors.

What should you do when your logic must handle an error that you
believe "can't happen"?

- If it's possible to return an error, then use the `bug.Errorf`
  function to return an error to the user, but also record the bug in
  gopls' cache so that it is less likely to be ignored.

- If it's safe to proceed, you can call `bug.Reportf` to record the
  error and continue as normal.

- If there's no way to proceed, call `bug.Fatalf` to record the error
  and then stop the program with `log.Fatalf`. You can also use
  `bug.Panicf` if there's a chance that a recover handler might save
  the situation.

- Only if you can prove locally that an error is impossible should you
  call `log.Fatal`. If the error may happen for some input, however
  unlikely, then you should use one of the approaches above. Also, if
  the proof of safety depends on invariants broadly distributed across
  the code base, then you should instead use `bug.Panicf`.

Note also that panicking is preferable to `log.Fatal` because it
allows VS Code's crash reporting to recognize and capture the stack.

Bugs reported through `bug.Errorf` and friends are retrieved using the
`gopls bug` command, which opens a GitHub Issue template and populates
it with a summary of each bug and its frequency.
The text of the bug is rather fastidiously printed to stdout to avoid
sharing user names and error message strings (which could contain
project identifiers) with GitHub.
Users are invited to share it if they are willing.

## Testing

The normal command you should use to run the tests after a change is:

```bash
gopls$ go test -short ./...
```

(The `-short` flag skips some slow-running ones. The trybot builders
run the complete set, on a wide range of platforms.)

Gopls tests are a mix of two kinds.

- [Marker tests](../internal/test/marker) express each test scenario
  in a standalone text file that contains the target .go, go.mod, and
  go.work files, in which special annotations embedded in comments
  drive the test. These tests are generally easy to write and fast
  to iterate, but have limitations on what they can express.

- [Integration tests](../internal/test/integration) are regular Go
  `func Test(*testing.T)` functions that make a series of calls to an
  API for a fake LSP-enabled client editor. The API allows you to open
  and edit a file, navigate to a definition, invoke other LSP
  operations, and assert properties about the state.

  Due to the asynchronous nature of the LSP, integration tests make
  assertions about states that the editor must achieve eventually,
  even when the program goes wrong quickly, it may take a while before
  the error is reported as a failure to achieve the desired state
  within several minutes. We recommend that you set
  `GOPLS_INTEGRATION_TEST_TIMEOUT=10s` to reduce the timeout for
  integration tests when debugging.
  
  When they fail, the integration tests print the log of the LSP
  session between client and server. Though verbose, they are very
  helpful for debugging once you know how to read them.

Don't hesitate to [reach out](#getting-help) to the gopls team if you
need help.

### CI

When you mail your CL and you or a fellow contributor assigns the
`Run-TryBot=1` label in Gerrit, the
[TryBots](https://golang.org/doc/contribute.html#trybots) will run tests in
both the `golang.org/x/tools` and `golang.org/x/tools/gopls` modules, as
described above.

Furthermore, an additional "gopls-CI" pass will be run by _Kokoro_, which is a
Jenkins-like Google infrastructure for running Dockerized tests. This allows us
to run gopls tests in various environments that would be difficult to add to
the TryBots. Notably, Kokoro runs tests on
[older Go versions](../README.md#supported-go-versions) that are no longer supported
by the TryBots. Per that that policy, support for these older Go versions is
best-effort, and test failures may be skipped rather than fixed.

Kokoro runs are triggered by the `Run-TryBot=1` label, just like TryBots, but
unlike TryBots they do not automatically re-run if the "gopls-CI" result is
removed in Gerrit. To force a re-run of the Kokoro CI on a CL containing the
`Run-TryBot=1` label, you can reply in Gerrit with the comment "kokoro rerun".

## Debugging

The easiest way to debug your change is to run a single `gopls` test with a
debugger.

See also [Troubleshooting](troubleshooting.md#troubleshooting).

<!--TODO(rstambler): Add more details about the debug server and viewing
telemetry.-->

[issue-gopls]: https://github.com/golang/go/issues?utf8=%E2%9C%93&q=is%3Aissue+is%3Aopen+label%3Agopls "gopls issues"
[issue-wanted]: https://github.com/golang/go/issues?utf8=✓&q=is%3Aissue+is%3Aopen+label%3Agopls+label%3A"help+wanted" "help wanted"

## Documentation

Each CL that adds or changes a feature should include, in addition to
a test that exercises the new behavior:

- a **release note** that briefly explains the change, and
- **comprehensive documentation** in the [index of features](features/README.md).

The release note should go in the file named for the forthcoming
release, for example [release/v0.16.0.md](release/v0.16.0.md). (Create
the file if your feature is the first to be added after a release.)


