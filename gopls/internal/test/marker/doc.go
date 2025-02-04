// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package marker defines a framework for running "marker" tests, each
defined by a file in the testdata subdirectory.

Use this command to run the tests, from the gopls module:

	$ go test ./internal/test/marker [-update]

A marker test uses the '//@' syntax of the x/tools/internal/expect package to
annotate source code with various information such as locations and arguments
of LSP operations to be executed by the test. The syntax following '@' is
parsed as a comma-separated list of Go-like function calls, which we refer to
as 'markers' (or sometimes 'marks'), for example

	//@ foo(a, "b", 3), bar(0)

Unlike ordinary Go, the marker syntax also supports optional named arguments
using the syntax name=value. If provided, named arguments must appear after all
positional arguments, though their ordering with respect to other named
arguments does not matter. For example

	//@ foo(a, "b", d=4, c=3)

Each marker causes a corresponding function to be called in the test. Some
markers are declarations; for example, @loc declares a name for a source
location. Others have effects, such as executing an LSP operation and asserting
that it behaved as expected. See the Marker types documentation below for the
list of all supported markers.

Each call argument is converted to the type of the corresponding parameter of
the designated function. The conversion logic may use the surrounding context,
such as the position or nearby text. See the Argument conversion section below
for the full set of special conversions. As a special case, the blank
identifier '_' is treated as the zero value of the parameter type.

The test runner collects test cases by searching the given directory for
files with the .txt extension. Each file is interpreted as a txtar archive,
which is extracted to a temporary directory. The relative path to the .txt
file is used as the subtest name. The preliminary section of the file
(before the first archive entry) is a free-form comment.

# Special files

There are several types of file within the test archive that are given special
treatment by the test runner:

  - "skip": the presence of this file causes the test to be skipped, with
    its content used as the skip message.

  - "flags": this file is treated as a whitespace-separated list of flags
    that configure the MarkerTest instance. Supported flags:

    -{min,max}_go=go1.20 sets the {min,max}imum Go runtime version for the test
    (inclusive).
    -{min,max}_go_command=go1.20 sets the {min,max}imum Go command version for
    the test (inclusive).
    -cgo requires that CGO_ENABLED is set and the cgo tool is available.
    -write_sumfile=a,b,c instructs the test runner to generate go.sum files
    in these directories before running the test.
    -skip_goos=a,b,c instructs the test runner to skip the test for the
    listed GOOS values.
    -skip_goarch=a,b,c does the same for GOARCH.
    TODO(rfindley): using build constraint expressions for -skip_go{os,arch} would
    be clearer.
    -ignore_extra_diags suppresses errors for unmatched diagnostics
    -filter_builtins=false disables the filtering of builtins from
    completion results.
    -filter_keywords=false disables the filtering of keywords from
    completion results.
    -errors_ok=true suppresses errors for Error level log entries.

    TODO(rfindley): support flag values containing whitespace.

  - "settings.json": this file is parsed as JSON, and used as the
    session configuration (see gopls/doc/settings.md)

  - "capabilities.json": this file is parsed as JSON client capabilities,
    and applied as an overlay over the default editor client capabilities.
    see https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#clientCapabilities
    for more details.

  - "env": this file is parsed as a list of VAR=VALUE fields specifying the
    editor environment.

  - Golden files: Within the archive, file names starting with '@' are
    treated as "golden" content, and are not written to disk, but instead are
    made available to test methods expecting an argument of type *Golden,
    using the identifier following '@'. For example, if the first parameter of
    Foo were of type *Golden, the test runner would convert the identifier a
    in the call @foo(a, "b", 3) into a *Golden by collecting golden file
    data starting with "@a/". As a special case, for tests that only need one
    golden file, the data contained in the file "@a" is indexed in the *Golden
    value by the empty string "".

  - proxy files: any file starting with proxy/ is treated as a Go proxy
    file. If present, these files are written to a separate temporary
    directory and GOPROXY is set to file://<proxy directory>.

# Marker types

Markers are of two kinds: "value markers" and "action markers". Value markers
are processed in a first pass, and define named values that may be referred to
as arguments to action markers. For example, the @loc marker defines a named
location that may be used wherever a location is expected. Value markers cannot
refer to names defined by other value markers. Action markers are processed in
a second pass and perform some action such as testing an LSP operation.

Below, we list supported markers using function signatures, augmented with the
named argument support name=value, as described above. The types referred to in
the signatures below are described in the Argument conversion section.

Here is the list of supported value markers:

  - loc(name, location): specifies the name for a location in the source. These
    locations may be referenced by other markers. Naturally, the location
    argument may be specified only as a string or regular expression in the
    first pass.

  - defloc(name, location): performs a textDocument/definition request at the
    src location, and binds the result to the given name. This may be used to
    refer to positions in the standard library.

  - hiloc(name, location, kind): defines a documentHighlight value of the
    given location and kind. Use its label in a @highlightall marker to
    indicate the expected result of a highlight query.

  - item(name, details, kind): defines a completionItem with the provided
    fields. This information is not positional, and therefore @item markers
    may occur anywhere in the source. Use in conjunction with @complete,
    @snippet, or @rank.

    TODO(rfindley): rethink whether floating @item annotations are the best
    way to specify completion results.

Here is the list of supported action markers:

  - acceptcompletion(location, label, golden): specifies that accepting the
    completion candidate produced at the given location with provided label
    results in the given golden state.

  - codeaction(start location, kind string, end=location, edit=golden, result=golden, err=stringMatcher)

    Specifies a code action to request at the location, with given kind.

    If end is set, the location is defined to be between start.Start and end.End.

    Exactly one of edit, result, or err must be set. If edit is set, it is a
    golden reference to the edits resulting from the code action. If result is
    set, it is a golden reference to the full set of changed files resulting
    from the code action. If err is set, it is the code action error.

  - codelens(location, title): specifies that a codelens is expected at the
    given location, with given title. Must be used in conjunction with
    @codelenses.

  - codelenses(): specifies that textDocument/codeLens should be run for the
    current document, with results compared to the @codelens annotations in
    the current document.

  - complete(location, ...items): specifies expected completion results at
    the given location. Must be used in conjunction with @item.

  - diag(location, regexp, exact=bool): specifies an expected diagnostic
    matching the given regexp at the given location. The test runner requires a
    1:1 correspondence between observed diagnostics and diag annotations. The
    diagnostics source and kind fields are ignored, to reduce fuss.

    The specified location must match the start position of the diagnostic,
    but end positions are ignored unless exact=true.

    TODO(adonovan): in the older marker framework, the annotation asserted two
    additional fields (source="compiler", kind="error"). Restore them using
    optional named arguments.

  - def(src, dst location): performs a textDocument/definition request at
    the src location, and check the result points to the dst location.

  - documentLink(golden): asserts that textDocument/documentLink returns
    links as described by the golden file.

  - foldingrange(golden): performs a textDocument/foldingRange for the
    current document, and compare with the golden content, which is the
    original source annotated with numbered tags delimiting the resulting
    ranges (e.g. <1 kind="..."> ... </1>).

  - format(golden): performs a textDocument/format request for the enclosing
    file, and compare against the named golden file. If the formatting
    request succeeds, the golden file must contain the resulting formatted
    source. If the formatting request fails, the golden file must contain
    the error message.

  - highlightall(all ...documentHighlight): makes a textDocument/highlight
    request at each location in "all" and checks that the result is "all".
    In other words, given highlightall(X1, X2, ..., Xn), it checks that
    highlight(X1) = highlight(X2) = ... = highlight(Xn) = {X1, X2, ..., Xn}.
    In general, highlight sets are not equivalence classes; for asymmetric
    cases, use @highlight instead.
    Each element of "all" is the label of a @hiloc marker.

  - highlight(src location, dsts ...documentHighlight): makes a
    textDocument/highlight request at the given src location, which should
    highlight the provided dst locations and kinds.

  - hover(src, dst location, sm stringMatcher): performs a textDocument/hover
    at the src location, and checks that the result is the dst location, with
    matching hover content.

  - hovererr(src, sm stringMatcher): performs a textDocument/hover at the src
    location, and checks that the error matches the given stringMatcher.

  - implementations(src location, want ...location): makes a
    textDocument/implementation query at the src location and
    checks that the resulting set of locations matches want.

  - incomingcalls(src location, want ...location): makes a
    callHierarchy/incomingCalls query at the src location, and checks that
    the set of call.From locations matches want.
    (These locations are the declarations of the functions enclosing
    the calls, not the calls themselves.)

  - outgoingcalls(src location, want ...location): makes a
    callHierarchy/outgoingCalls query at the src location, and checks that
    the set of call.To locations matches want.

  - preparerename(src location, placeholder string, span=location): asserts
    that a textDocument/prepareRename request at the src location has the given
    placeholder text. If present, the optional span argument is verified to be
    the span of the prepareRename result. If placeholder is "", this is treated
    as a negative assertion and prepareRename should return nil.

  - quickfix(location, regexp, golden): like diag, the location and
    regexp identify an expected diagnostic, which must have exactly one
    associated "quickfix" code action.
    This action is executed for its editing effects on the source files.
    Like rename, the golden directory contains the expected transformed files.

  - quickfixerr(location, regexp, wantError): specifies that the
    quickfix operation should fail with an error that matches the expectation.
    (Failures in the computation to offer a fix do not generally result
    in LSP errors, so this marker is not appropriate for testing them.)

  - rank(location, ...string OR completionItem): executes a
    textDocument/completion request at the given location, and verifies that
    each expected completion item occurs in the results, in the expected order.
    Items may be specified as string literal completion labels, or as
    references to a completion item created with the @item marker.
    Other unexpected completion items are allowed to occur in the results, and
    are ignored. A "!" prefix on a label asserts that the symbol is not a
    completion candidate.

  - refs(location, want ...location): executes a textDocument/references
    request at the first location and asserts that the result is the set of
    'want' locations. The first want location must be the declaration
    (assumedly unique).

  - rename(location, new, golden): specifies a renaming of the
    identifier at the specified location to the new name.
    The golden directory contains the transformed files.

  - renameerr(location, new, wantError): specifies a renaming that
    fails with an error that matches the expectation.

  - signature(location, label, active): specifies that
    signatureHelp at the given location should match the provided string, with
    the active parameter (an index) highlighted.

  - snippet(location, string OR completionItem, snippet): executes a
    textDocument/completion request at the location, and searches for a result
    with label matching that its second argument, which may be a string literal
    or a reference to a completion item created by the @item marker (in which
    case the item's label is used). It checks that the resulting snippet
    matches the provided snippet.

  - symbol(golden): makes a textDocument/documentSymbol request
    for the enclosing file, formats the response with one symbol
    per line, sorts it, and compares against the named golden file.
    Each line is of the form:

    dotted.symbol.name kind "detail" +n lines

    where the "+n lines" part indicates that the declaration spans
    several lines. The test otherwise makes no attempt to check
    location information. There is no point to using more than one
    @symbol marker in a given file.

  - token(location, tokenType, mod): makes a textDocument/semanticTokens/range
    request at the given location, and asserts that the result includes
    exactly one token with the given token type and modifier string.

  - workspacesymbol(query, golden): makes a workspace/symbol request for the
    given query, formats the response with one symbol per line, and compares
    against the named golden file. As workspace symbols are by definition a
    workspace-wide request, the location of the workspace symbol marker does
    not matter. Each line is of the form:

    location name kind

# Argument conversion

Marker arguments are first parsed by the internal/expect package, which accepts
the following tokens as defined by the Go spec:
  - string, int64, float64, and rune literals
  - true and false
  - nil
  - identifiers (type expect.Identifier)
  - regular expressions, denoted the two tokens re"abc" (type *regexp.Regexp)

These values are passed as arguments to the corresponding parameter of the
test function. Additional value conversions may occur for these argument ->
parameter type pairs:

  - string->regexp: the argument is parsed as a regular expressions.

  - string->location: the argument is converted to the location of the first
    instance of the argument in the file content starting from the beginning of
    the line containing the note. Multi-line matches are permitted, but the
    match must begin before the note.

  - regexp->location: the argument is converted to the location of the first
    match for the argument in the file content starting from the beginning of
    the line containing the note. Multi-line matches are permitted, but the
    match must begin before the note. If the regular expression contains
    exactly one subgroup, the position of the subgroup is used rather than the
    position of the submatch.

  - name->location: the argument is replaced by the named location.

  - name->Golden: the argument is used to look up golden content prefixed by
    @<argument>.

  - {string,regexp,identifier}->stringMatcher: a stringMatcher type
    specifies an expected string, either in the form of a substring
    that must be present, a regular expression that it must match, or an
    identifier (e.g. foo) such that the archive entry @foo exists and
    contains the exact expected string.
    stringMatchers are used by some markers to match positive results
    (outputs) and by other markers to match error messages.

# Example

Here is a complete example:

	This test checks hovering over constants.

	-- a.go --
	package a

	const abc = 0x2a //@hover("b", "abc", abc),hover(" =", "abc", abc)

	-- @abc --
	```go
	const abc untyped int = 42
	```

	@hover("b", "abc", abc),hover(" =", "abc", abc)

In this example, the @hover annotation tells the test runner to run the
hoverMarker function, which has parameters:

	(mark marker, src, dst protocol.Location, g *Golden).

The first argument holds the test context, including fake editor with open
files, and sandboxed directory.

Argument converters translate the "b" and "abc" arguments into locations by
interpreting each one as a substring (or as a regular expression, if of the
form re"a|b") and finding the location of its first occurrence starting on the
preceding portion of the line, and the abc identifier into a the golden content
contained in the file @abc. Then the hoverMarker method executes a
textDocument/hover LSP request at the src position, and ensures the result
spans "abc", with the markdown content from @abc. (Note that the markdown
content includes the expect annotation as the doc comment.)

The next hover on the same line asserts the same result, but initiates the
hover immediately after "abc" in the source. This tests that we find the
preceding identifier when hovering.

# Updating golden files

To update golden content in the test archive, it is easier to regenerate
content automatically rather than edit it by hand. To do this, run the
tests with the -update flag. Only tests that actually run will be updated.

In some cases, golden content will vary by Go version (for example, gopls
produces different markdown at Go versions before the 1.19 go/doc update).
By convention, the golden content in test archives should match the output
at Go tip. Each test function can normalize golden content for older Go
versions.

Note that -update does not cause missing @diag or @loc markers to be added.

# TODO

  - Rename the files .txtar.
  - Eliminate all *err markers, preferring named arguments.
*/
package marker
