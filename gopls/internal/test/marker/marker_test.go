// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package marker

// This file defines the marker test framework.
// See doc.go for extensive documentation.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/token"
	"go/types"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/debug"
	"golang.org/x/tools/gopls/internal/lsprpc"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/test/compare"
	"golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/diff/myers"
	"golang.org/x/tools/internal/expect"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/jsonrpc2/servertest"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/txtar"
)

var update = flag.Bool("update", false, "if set, update test data during marker tests")

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	testenv.ExitIfSmallMachine()
	// Disable GOPACKAGESDRIVER, as it can cause spurious test failures.
	os.Setenv("GOPACKAGESDRIVER", "off")
	integration.FilterToolchainPathAndGOROOT()
	os.Exit(m.Run())
}

// Test runs the marker tests from the testdata directory.
//
// See package documentation for details on how marker tests work.
//
// These tests were inspired by (and in many places copied from) a previous
// iteration of the marker tests built on top of the packagestest framework.
// Key design decisions motivating this reimplementation are as follows:
//   - The old tests had a single global session, causing interaction at a
//     distance and several awkward workarounds.
//   - The old tests could not be safely parallelized, because certain tests
//     manipulated the server options
//   - Relatedly, the old tests did not have a logic grouping of assertions into
//     a single unit, resulting in clusters of files serving clusters of
//     entangled assertions.
//   - The old tests used locations in the source as test names and as the
//     identity of golden content, meaning that a single edit could change the
//     name of an arbitrary number of subtests, and making it difficult to
//     manually edit golden content.
//   - The old tests did not hew closely to LSP concepts, resulting in, for
//     example, each marker implementation doing its own position
//     transformations, and inventing its own mechanism for configuration.
//   - The old tests had an ad-hoc session initialization process. The integration
//     test environment has had more time devoted to its initialization, and has a
//     more convenient API.
//   - The old tests lacked documentation, and often had failures that were hard
//     to understand. By starting from scratch, we can revisit these aspects.
func Test(t *testing.T) {
	if testing.Short() {
		builder := os.Getenv("GO_BUILDER_NAME")
		// Note that HasPrefix(builder, "darwin-" only matches legacy builders.
		// LUCI builder names start with x_tools-goN.NN.
		// We want to exclude solaris on both legacy and LUCI builders, as
		// it is timing out.
		if strings.HasPrefix(builder, "darwin-") || strings.Contains(builder, "solaris") {
			t.Skip("golang/go#64473: skipping with -short: this test is too slow on darwin and solaris builders")
		}
	}
	// The marker tests must be able to run go/packages.Load.
	testenv.NeedsGoPackages(t)

	const dir = "testdata"
	tests, err := loadMarkerTests(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Opt: use a shared cache.
	cache := cache.New(nil)

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if test.skipReason != "" {
				t.Skip(test.skipReason)
			}
			if slices.Contains(test.skipGOOS, runtime.GOOS) {
				t.Skipf("skipping on %s due to -skip_goos", runtime.GOOS)
			}
			if slices.Contains(test.skipGOARCH, runtime.GOARCH) {
				t.Skipf("skipping on %s due to -skip_goarch", runtime.GOARCH)
			}

			// TODO(rfindley): it may be more useful to have full support for build
			// constraints.
			if test.minGoVersion != "" {
				var go1point int
				if _, err := fmt.Sscanf(test.minGoVersion, "go1.%d", &go1point); err != nil {
					t.Fatalf("parsing -min_go version: %v", err)
				}
				testenv.NeedsGo1Point(t, go1point)
			}
			if test.minGoCommandVersion != "" {
				var go1point int
				if _, err := fmt.Sscanf(test.minGoCommandVersion, "go1.%d", &go1point); err != nil {
					t.Fatalf("parsing -min_go_command version: %v", err)
				}
				testenv.NeedsGoCommand1Point(t, go1point)
			}
			if test.maxGoCommandVersion != "" {
				var go1point int
				if _, err := fmt.Sscanf(test.maxGoCommandVersion, "go1.%d", &go1point); err != nil {
					t.Fatalf("parsing -max_go_command version: %v", err)
				}
				testenv.SkipAfterGoCommand1Point(t, go1point)
			}
			if test.cgo {
				if os.Getenv("CGO_ENABLED") == "0" {
					// NeedsTool causes the test to fail if cgo is available but disabled
					// on the current platform through the environment. I'm not sure why it
					// behaves this way, but if CGO_ENABLED=0 is set, we want to skip.
					t.Skip("skipping due to CGO_ENABLED=0")
				}
				testenv.NeedsTool(t, "cgo")
			}

			config := fake.EditorConfig{
				Settings:         test.settings,
				CapabilitiesJSON: test.capabilities,
				Env:              test.env,
			}

			if _, ok := config.Settings["diagnosticsDelay"]; !ok {
				if config.Settings == nil {
					config.Settings = make(map[string]any)
				}
				config.Settings["diagnosticsDelay"] = "10ms"
			}

			// inv: config.Settings != nil

			run := &markerTestRun{
				test:       test,
				env:        newEnv(t, cache, test.files, test.proxyFiles, test.writeGoSum, config),
				settings:   config.Settings,
				values:     make(map[expect.Identifier]any),
				diags:      make(map[protocol.Location][]protocol.Diagnostic),
				extraNotes: make(map[protocol.DocumentURI]map[string][]*expect.Note),
			}

			// TODO(rfindley): make it easier to clean up the integration test environment.
			defer run.env.Editor.Shutdown(context.Background()) // ignore error
			defer run.env.Sandbox.Close()                       // ignore error

			// Open all files so that we operate consistently with LSP clients, and
			// (pragmatically) so that we have a Mapper available via the fake
			// editor.
			//
			// This also allows avoiding mutating the editor state in tests.
			for file := range test.files {
				run.env.OpenFile(file)
			}

			allDiags := make(map[string][]protocol.Diagnostic)
			if run.env.Editor.ServerCapabilities().DiagnosticProvider != nil {
				for name := range test.files {
					// golang/go#53275: support pull diagnostics for go.mod and go.work
					// files.
					if strings.HasSuffix(name, ".go") {
						allDiags[name] = run.env.Diagnostics(name)
					}
				}
			} else {
				// Wait for the didOpen notifications to be processed, then collect
				// diagnostics.

				run.env.AfterChange()
				var diags map[string]*protocol.PublishDiagnosticsParams
				run.env.AfterChange(integration.ReadAllDiagnostics(&diags))
				for path, params := range diags {
					allDiags[path] = params.Diagnostics
				}
			}

			for path, diags := range allDiags {
				uri := run.env.Sandbox.Workdir.URI(path)
				for _, diag := range diags {
					loc := protocol.Location{
						URI: uri,
						Range: protocol.Range{
							Start: diag.Range.Start,
							End:   diag.Range.Start, // ignore end positions
						},
					}
					run.diags[loc] = append(run.diags[loc], diag)
				}
			}

			var markers []marker
			for _, note := range test.notes {
				mark := marker{run: run, note: note}
				if fn, ok := valueMarkerFuncs[note.Name]; ok {
					fn(mark)
				} else if _, ok := actionMarkerFuncs[note.Name]; ok {
					markers = append(markers, mark) // save for later
				} else {
					uri := mark.uri()
					if run.extraNotes[uri] == nil {
						run.extraNotes[uri] = make(map[string][]*expect.Note)
					}
					run.extraNotes[uri][note.Name] = append(run.extraNotes[uri][note.Name], note)
				}
			}

			// Invoke each remaining marker in the test.
			for _, mark := range markers {
				actionMarkerFuncs[mark.note.Name](mark)
			}

			// Any remaining (un-eliminated) diagnostics are an error.
			if !test.ignoreExtraDiags {
				for loc, diags := range run.diags {
					for _, diag := range diags {
						// Note that loc is collapsed (start==end).
						// For formatting, show the exact span.
						exactLoc := protocol.Location{
							URI:   loc.URI,
							Range: diag.Range,
						}
						t.Errorf("%s: unexpected diagnostic: %q", run.fmtLoc(exactLoc), diag.Message)
					}
				}
			}

			// TODO(rfindley): use these for whole-file marker tests.
			for uri, extras := range run.extraNotes {
				for name, extra := range extras {
					if len(extra) > 0 {
						t.Errorf("%s: %d unused %q markers", run.env.Sandbox.Workdir.URIToPath(uri), len(extra), name)
					}
				}
			}

			// Now that all markers have executed, check whether there where any
			// unexpected error logs.
			// This guards against noisiness: see golang/go#66746)
			if !test.errorsOK {
				run.env.AfterChange(integration.NoErrorLogs())
			}

			formatted, err := formatTest(test)
			if err != nil {
				t.Errorf("formatTest: %v", err)
			} else if *update {
				filename := filepath.Join(dir, test.name)
				if err := os.WriteFile(filename, formatted, 0644); err != nil {
					t.Error(err)
				}
			} else if !t.Failed() {
				// Verify that the testdata has not changed.
				//
				// Only check this if the test hasn't already failed, otherwise we'd
				// report duplicate mismatches of golden data.
				// Otherwise, verify that formatted content matches.
				if diff := compare.NamedText("formatted", "on-disk", string(formatted), string(test.content)); diff != "" {
					t.Errorf("formatted test does not match on-disk content:\n%s", diff)
				}
			}
		})
	}

	if abs, err := filepath.Abs(dir); err == nil && t.Failed() {
		t.Logf("(Filenames are relative to %s.)", abs)
	}
}

// A marker holds state for the execution of a single @marker
// annotation in the source.
type marker struct {
	run  *markerTestRun
	note *expect.Note
}

// ctx returns the mark context.
func (m marker) ctx() context.Context { return m.run.env.Ctx }

// T returns the testing.TB for this mark.
func (m marker) T() testing.TB { return m.run.env.T }

// server returns the LSP server for the marker test run.
func (m marker) editor() *fake.Editor { return m.run.env.Editor }

// server returns the LSP server for the marker test run.
func (m marker) server() protocol.Server { return m.run.env.Editor.Server }

// uri returns the URI of the file containing the marker.
func (mark marker) uri() protocol.DocumentURI {
	return mark.run.env.Sandbox.Workdir.URI(mark.run.test.fset.File(mark.note.Pos).Name())
}

// document returns a protocol.TextDocumentIdentifier for the current file.
func (mark marker) document() protocol.TextDocumentIdentifier {
	return protocol.TextDocumentIdentifier{URI: mark.uri()}
}

// path returns the relative path to the file containing the marker.
func (mark marker) path() string {
	return mark.run.env.Sandbox.Workdir.RelPath(mark.run.test.fset.File(mark.note.Pos).Name())
}

// mapper returns a *protocol.Mapper for the current file.
func (mark marker) mapper() *protocol.Mapper {
	mapper, err := mark.editor().Mapper(mark.path())
	if err != nil {
		mark.T().Fatalf("failed to get mapper for current mark: %v", err)
	}
	return mapper
}

// error reports an error with a prefix indicating the position of the marker
// note.
func (mark marker) error(args ...any) {
	mark.T().Helper()
	msg := fmt.Sprint(args...)
	mark.T().Errorf("%s: %s", mark.run.fmtPos(mark.note.Pos), msg)
}

// errorf reports a formatted error with a prefix indicating the position of
// the marker note.
//
// It formats the error message using mark.sprintf.
func (mark marker) errorf(format string, args ...any) {
	mark.T().Helper()
	msg := mark.sprintf(format, args...)
	// TODO(adonovan): consider using fmt.Fprintf(os.Stderr)+t.Fail instead of
	// t.Errorf to avoid reporting uninteresting positions in the Go source of
	// the driver. However, this loses the order of stderr wrt "FAIL: TestFoo"
	// subtest dividers.
	mark.T().Errorf("%s: %s", mark.run.fmtPos(mark.note.Pos), msg)
}

// valueMarkerFunc returns a wrapper around a function that allows it to be
// called during the processing of value markers (e.g. @value(v, 123)) with marker
// arguments converted to function parameters. The provided function's first
// parameter must be of type 'marker', and it must return a value.
//
// Unlike action markers, which are executed for actions such as test
// assertions, value markers are all evaluated first, and each computes
// a value that is recorded by its identifier, which is the marker's first
// argument. These values may be referred to from an action marker by
// this identifier, e.g. @action(... , v, ...).
//
// For example, given a fn with signature
//
//	func(mark marker, label, details, kind string) CompletionItem
//
// The result of valueMarkerFunc can associated with @item notes, and invoked
// as follows:
//
//	//@item(FooCompletion, "Foo", "func() int", "func")
//
// The provided fn should not mutate the test environment.
func valueMarkerFunc(fn any) func(marker) {
	ftype := reflect.TypeOf(fn)
	if ftype.NumIn() == 0 || ftype.In(0) != markerType {
		panic(fmt.Sprintf("value marker function %#v must accept marker as its first argument", ftype))
	}
	if ftype.NumOut() != 1 {
		panic(fmt.Sprintf("value marker function %#v must have exactly 1 result", ftype))
	}

	return func(mark marker) {
		if len(mark.note.Args) == 0 || !is[expect.Identifier](mark.note.Args[0]) {
			mark.errorf("first argument to a value marker function must be an identifier")
			return
		}
		id := mark.note.Args[0].(expect.Identifier)
		if alt, ok := mark.run.values[id]; ok {
			mark.errorf("%s already declared as %T", id, alt)
			return
		}
		args := append([]any{mark}, mark.note.Args[1:]...)
		argValues, err := convertArgs(mark, ftype, args)
		if err != nil {
			mark.error(err)
			return
		}
		results := reflect.ValueOf(fn).Call(argValues)
		mark.run.values[id] = results[0].Interface()
	}
}

// actionMarkerFunc returns a wrapper around a function that allows it to be
// called during the processing of action markers (e.g. @action("abc", 123))
// with marker arguments converted to function parameters. The provided
// function's first parameter must be of type 'marker', and it must not return
// any values. Any named arguments that may be used by the marker func must be
// listed in allowedNames.
//
// The provided fn should not mutate the test environment.
func actionMarkerFunc(fn any, allowedNames ...string) func(marker) {
	ftype := reflect.TypeOf(fn)
	if ftype.NumIn() == 0 || ftype.In(0) != markerType {
		panic(fmt.Sprintf("action marker function %#v must accept marker as its first argument", ftype))
	}
	if ftype.NumOut() != 0 {
		panic(fmt.Sprintf("action marker function %#v cannot have results", ftype))
	}

	var allowed map[string]bool
	if len(allowedNames) > 0 {
		allowed = make(map[string]bool)
		for _, name := range allowedNames {
			allowed[name] = true
		}
	}

	return func(mark marker) {
		for name := range mark.note.NamedArgs {
			if !allowed[name] {
				mark.errorf("unexpected named argument %q", name)
			}
		}

		args := append([]any{mark}, mark.note.Args...)
		argValues, err := convertArgs(mark, ftype, args)
		if err != nil {
			mark.error(err)
			return
		}
		reflect.ValueOf(fn).Call(argValues)
	}
}

func convertArgs(mark marker, ftype reflect.Type, args []any) ([]reflect.Value, error) {
	var (
		argValues []reflect.Value
		pnext     int          // next param index
		p         reflect.Type // current param
	)
	for i, arg := range args {
		if i < ftype.NumIn() {
			p = ftype.In(pnext)
			pnext++
		} else if p == nil || !ftype.IsVariadic() {
			// The actual number of arguments expected by the mark varies, depending
			// on whether this is a value marker or an action marker.
			//
			// Since this error indicates a bug, probably OK to have an imprecise
			// error message here.
			return nil, fmt.Errorf("too many arguments to %s", mark.note.Name)
		}
		elemType := p
		if ftype.IsVariadic() && pnext == ftype.NumIn() {
			elemType = p.Elem()
		}
		var v reflect.Value
		if id, ok := arg.(expect.Identifier); ok && id == "_" {
			v = reflect.Zero(elemType)
		} else {
			a, err := convert(mark, arg, elemType)
			if err != nil {
				return nil, err
			}
			v = reflect.ValueOf(a)
		}
		argValues = append(argValues, v)
	}
	// Check that we have sufficient arguments. If the function is variadic, we
	// do not need arguments for the final parameter.
	if pnext < ftype.NumIn()-1 || pnext == ftype.NumIn()-1 && !ftype.IsVariadic() {
		// Same comment as above: OK to be vague here.
		return nil, fmt.Errorf("not enough arguments to %s", mark.note.Name)
	}
	return argValues, nil
}

// namedArg returns the named argument for name, or the default value.
func namedArg[T any](mark marker, name string, dflt T) T {
	if v, ok := mark.note.NamedArgs[name]; ok {
		if e, ok := v.(T); ok {
			return e
		} else {
			v, err := convert(mark, v, reflect.TypeOf(dflt))
			if err != nil {
				mark.errorf("invalid value for %q: could not convert %v (%T) to %T", name, v, v, dflt)
				return dflt
			}
			return v.(T)
		}
	}
	return dflt
}

func namedArgFunc[T any](mark marker, name string, f func(marker, any) (T, error), dflt T) T {
	if v, ok := mark.note.NamedArgs[name]; ok {
		if v2, err := f(mark, v); err == nil {
			return v2
		} else {
			mark.errorf("invalid value for %q: %v: %v", name, v, err)
		}
	}
	return dflt
}

func exactlyOneNamedArg(mark marker, names ...string) bool {
	var found []string
	for _, name := range names {
		if _, ok := mark.note.NamedArgs[name]; ok {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		mark.errorf("need exactly one of %v to be set, got %v", names, found)
		return false
	}
	return true
}

// is reports whether arg is a T.
func is[T any](arg any) bool {
	_, ok := arg.(T)
	return ok
}

// Supported value marker functions. See [valueMarkerFunc] for more details.
var valueMarkerFuncs = map[string]func(marker){
	"loc":    valueMarkerFunc(locMarker),
	"item":   valueMarkerFunc(completionItemMarker),
	"hiloc":  valueMarkerFunc(highlightLocationMarker),
	"defloc": valueMarkerFunc(defLocMarker),
}

// Supported action marker functions. See [actionMarkerFunc] for more details.
//
// See doc.go for marker documentation.
var actionMarkerFuncs = map[string]func(marker){
	"acceptcompletion": actionMarkerFunc(acceptCompletionMarker),
	"codeaction":       actionMarkerFunc(codeActionMarker, "end", "result", "edit", "err"),
	"codelenses":       actionMarkerFunc(codeLensesMarker),
	"complete":         actionMarkerFunc(completeMarker),
	"def":              actionMarkerFunc(defMarker),
	"diag":             actionMarkerFunc(diagMarker, "exact"),
	"documentlink":     actionMarkerFunc(documentLinkMarker),
	"foldingrange":     actionMarkerFunc(foldingRangeMarker),
	"format":           actionMarkerFunc(formatMarker),
	"highlight":        actionMarkerFunc(highlightMarker),
	"highlightall":     actionMarkerFunc(highlightAllMarker),
	"hover":            actionMarkerFunc(hoverMarker),
	"hovererr":         actionMarkerFunc(hoverErrMarker),
	"implementation":   actionMarkerFunc(implementationMarker),
	"incomingcalls":    actionMarkerFunc(incomingCallsMarker),
	"inlayhints":       actionMarkerFunc(inlayhintsMarker),
	"outgoingcalls":    actionMarkerFunc(outgoingCallsMarker),
	"preparerename":    actionMarkerFunc(prepareRenameMarker, "span"),
	"rank":             actionMarkerFunc(rankMarker),
	"refs":             actionMarkerFunc(refsMarker),
	"rename":           actionMarkerFunc(renameMarker),
	"renameerr":        actionMarkerFunc(renameErrMarker),
	"selectionrange":   actionMarkerFunc(selectionRangeMarker),
	"signature":        actionMarkerFunc(signatureMarker),
	"snippet":          actionMarkerFunc(snippetMarker),
	"quickfix":         actionMarkerFunc(quickfixMarker),
	"quickfixerr":      actionMarkerFunc(quickfixErrMarker),
	"symbol":           actionMarkerFunc(symbolMarker),
	"token":            actionMarkerFunc(tokenMarker),
	"typedef":          actionMarkerFunc(typedefMarker),
	"workspacesymbol":  actionMarkerFunc(workspaceSymbolMarker),
}

// markerTest holds all the test data extracted from a test txtar archive.
//
// See the documentation for RunMarkerTests for more information on the archive
// format.
type markerTest struct {
	name         string                        // relative path to the txtar file in the testdata dir
	fset         *token.FileSet                // fileset used for parsing notes
	content      []byte                        // raw test content
	archive      *txtar.Archive                // original test archive
	settings     map[string]any                // gopls settings
	capabilities []byte                        // content of capabilities.json file
	env          map[string]string             // editor environment
	proxyFiles   map[string][]byte             // proxy content
	files        map[string][]byte             // data files from the archive (excluding special files)
	notes        []*expect.Note                // extracted notes from data files
	golden       map[expect.Identifier]*Golden // extracted golden content, by identifier name

	skipReason string   // the skip reason extracted from the "skip" archive file
	flags      []string // flags extracted from the special "flags" archive file.

	// Parsed flags values. See the flag definitions below for documentation.
	minGoVersion        string // minimum Go runtime version; max should never be needed
	minGoCommandVersion string
	maxGoCommandVersion string
	cgo                 bool
	writeGoSum          []string
	skipGOOS            []string
	skipGOARCH          []string
	ignoreExtraDiags    bool
	filterBuiltins      bool
	filterKeywords      bool
	errorsOK            bool
}

// flagSet returns the flagset used for parsing the special "flags" file in the
// test archive.
func (t *markerTest) flagSet() *flag.FlagSet {
	flags := flag.NewFlagSet(t.name, flag.ContinueOnError)
	flags.StringVar(&t.minGoVersion, "min_go", "", "if set, the minimum go1.X version required for this test")
	flags.StringVar(&t.minGoCommandVersion, "min_go_command", "", "if set, the minimum go1.X go command version required for this test")
	flags.StringVar(&t.maxGoCommandVersion, "max_go_command", "", "if set, the maximum go1.X go command version required for this test")
	flags.BoolVar(&t.cgo, "cgo", false, "if set, requires cgo (both the cgo tool and CGO_ENABLED=1)")
	flags.Var((*stringListValue)(&t.writeGoSum), "write_sumfile", "if set, write the sumfile for these directories")
	flags.Var((*stringListValue)(&t.skipGOOS), "skip_goos", "if set, skip this test on these GOOS values")
	flags.Var((*stringListValue)(&t.skipGOARCH), "skip_goarch", "if set, skip this test on these GOARCH values")
	flags.BoolVar(&t.ignoreExtraDiags, "ignore_extra_diags", false, "if set, suppress errors for unmatched diagnostics")
	flags.BoolVar(&t.filterBuiltins, "filter_builtins", true, "if set, filter builtins from completion results")
	flags.BoolVar(&t.filterKeywords, "filter_keywords", true, "if set, filter keywords from completion results")
	flags.BoolVar(&t.errorsOK, "errors_ok", false, "if set, Error level log messages are acceptable in this test")
	return flags
}

// stringListValue implements flag.Value.
type stringListValue []string

func (l *stringListValue) Set(s string) error {
	if s != "" {
		for _, d := range strings.Split(s, ",") {
			*l = append(*l, strings.TrimSpace(d))
		}
	}
	return nil
}

func (l stringListValue) String() string {
	return strings.Join([]string(l), ",")
}

func (mark *marker) getGolden(id expect.Identifier) *Golden {
	t := mark.run.test
	golden, ok := t.golden[id]
	// If there was no golden content for this identifier, we must create one
	// to handle the case where -update is set: we need a place to store
	// the updated content.
	if !ok {
		golden = &Golden{id: id}

		// TODO(adonovan): the separation of markerTest (the
		// static aspects) from markerTestRun (the dynamic
		// ones) is evidently bogus because here we modify
		// markerTest during execution. Let's merge the two.
		t.golden[id] = golden
	}
	if golden.firstReference == "" {
		golden.firstReference = mark.path()
	}
	return golden
}

// Golden holds extracted golden content for a single @<name> prefix.
//
// When -update is set, golden captures the updated golden contents for later
// writing.
type Golden struct {
	id             expect.Identifier
	firstReference string            // file name first referencing this golden content
	data           map[string][]byte // key "" => @id itself
	updated        map[string][]byte
}

// Get returns golden content for the given name, which corresponds to the
// relative path following the golden prefix @<name>/. For example, to access
// the content of @foo/path/to/result.json from the Golden associated with
// @foo, name should be "path/to/result.json".
//
// If -update is set, the given update function will be called to get the
// updated golden content that should be written back to testdata.
//
// Marker functions must use this method instead of accessing data entries
// directly otherwise the -update operation will delete those entries.
//
// TODO(rfindley): rethink the logic here. We may want to separate Get and Set,
// and not delete golden content that isn't set.
func (g *Golden) Get(t testing.TB, name string, updated []byte) ([]byte, bool) {
	if existing, ok := g.updated[name]; ok {
		// Multiple tests may reference the same golden data, but if they do they
		// must agree about its expected content.
		if diff := compare.NamedText("existing", "updated", string(existing), string(updated)); diff != "" {
			t.Errorf("conflicting updates for golden data %s/%s:\n%s", g.id, name, diff)
		}
	}
	if g.updated == nil {
		g.updated = make(map[string][]byte)
	}
	g.updated[name] = updated
	if *update {
		return updated, true
	}

	res, ok := g.data[name]
	return res, ok
}

// loadMarkerTests walks the given dir looking for .txt files, which it
// interprets as a txtar archive.
//
// See the documentation for RunMarkerTests for more details on the test data
// archive.
func loadMarkerTests(dir string) ([]*markerTest, error) {
	var tests []*markerTest
	err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, err error) error {
		if strings.HasSuffix(path, ".txt") {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			name := strings.TrimPrefix(path, dir+string(filepath.Separator))
			test, err := loadMarkerTest(name, content)
			if err != nil {
				return fmt.Errorf("%s: %v", path, err)
			}
			tests = append(tests, test)
		}
		return err
	})
	return tests, err
}

func loadMarkerTest(name string, content []byte) (*markerTest, error) {
	archive := txtar.Parse(content)
	if len(archive.Files) == 0 {
		return nil, fmt.Errorf("txtar file has no '-- filename --' sections")
	}
	if bytes.Contains(archive.Comment, []byte("\n-- ")) {
		// This check is conservative, but the comment is only a comment.
		return nil, fmt.Errorf("ill-formed '-- filename --' header in comment")
	}
	test := &markerTest{
		name:    name,
		fset:    token.NewFileSet(),
		content: content,
		archive: archive,
		files:   make(map[string][]byte),
		golden:  make(map[expect.Identifier]*Golden),
	}
	for _, file := range archive.Files {
		switch {
		case file.Name == "skip":
			reason := strings.ReplaceAll(string(file.Data), "\n", " ")
			reason = strings.TrimSpace(reason)
			test.skipReason = reason

		case file.Name == "flags":
			test.flags = strings.Fields(string(file.Data))

		case file.Name == "settings.json":
			if err := json.Unmarshal(file.Data, &test.settings); err != nil {
				return nil, err
			}

		case file.Name == "capabilities.json":
			test.capabilities = file.Data // lazily unmarshalled by the editor

		case file.Name == "env":
			test.env = make(map[string]string)
			fields := strings.Fields(string(file.Data))
			for _, field := range fields {
				key, value, ok := strings.Cut(field, "=")
				if !ok {
					return nil, fmt.Errorf("env vars must be formatted as var=value, got %q", field)
				}
				test.env[key] = value
			}

		case strings.HasPrefix(file.Name, "@"): // golden content
			idstring, name, _ := strings.Cut(file.Name[len("@"):], "/")
			id := expect.Identifier(idstring)
			// Note that a file.Name of just "@id" gives (id, name) = ("id", "").
			if _, ok := test.golden[id]; !ok {
				test.golden[id] = &Golden{
					id:   id,
					data: make(map[string][]byte),
				}
			}
			test.golden[id].data[name] = file.Data

		case strings.HasPrefix(file.Name, "proxy/"):
			name := file.Name[len("proxy/"):]
			if test.proxyFiles == nil {
				test.proxyFiles = make(map[string][]byte)
			}
			test.proxyFiles[name] = file.Data

		default: // ordinary file content
			notes, err := expect.Parse(test.fset, file.Name, file.Data)
			if err != nil {
				return nil, fmt.Errorf("parsing notes in %q: %v", file.Name, err)
			}

			// Reject common misspelling: "// @mark".
			// TODO(adonovan): permit "// @" within a string. Detect multiple spaces.
			if i := bytes.Index(file.Data, []byte("// @")); i >= 0 {
				line := 1 + bytes.Count(file.Data[:i], []byte("\n"))
				return nil, fmt.Errorf("%s:%d: unwanted space before marker (// @)", file.Name, line)
			}

			// The 'go list' command doesn't work correct with modules named
			// testdata", so don't allow it as a module name (golang/go#65406).
			// (Otherwise files within it will end up in an ad hoc
			// package, "command-line-arguments/$TMPDIR/...".)
			if filepath.Base(file.Name) == "go.mod" &&
				bytes.Contains(file.Data, []byte("module testdata")) {
				return nil, fmt.Errorf("'testdata' is not a valid module name")
			}

			test.notes = append(test.notes, notes...)
			test.files[file.Name] = file.Data
		}

		// Print a warning if we see what looks like "-- filename --"
		// without the second "--". It's not necessarily wrong,
		// but it should almost never appear in our test inputs.
		if bytes.Contains(file.Data, []byte("\n-- ")) {
			log.Printf("ill-formed '-- filename --' header in %s?", file.Name)
		}
	}

	// Parse flags after loading files, as they may have been set by the "flags"
	// file.
	if err := test.flagSet().Parse(test.flags); err != nil {
		return nil, fmt.Errorf("parsing flags: %v", err)
	}

	return test, nil
}

// formatTest formats the test as a txtar archive.
func formatTest(test *markerTest) ([]byte, error) {
	arch := &txtar.Archive{
		Comment: test.archive.Comment,
	}

	updatedGolden := make(map[string][]byte)
	firstReferences := make(map[string]string)
	for id, g := range test.golden {
		for name, data := range g.updated {
			filename := "@" + path.Join(string(id), name) // name may be ""
			updatedGolden[filename] = data
			firstReferences[filename] = g.firstReference
		}
	}

	// Preserve the original ordering of archive files.
	for _, file := range test.archive.Files {
		switch file.Name {
		// Preserve configuration files exactly as they were. They must have parsed
		// if we got this far.
		case "skip", "flags", "settings.json", "capabilities.json", "env":
			arch.Files = append(arch.Files, file)
		default:
			if _, ok := test.files[file.Name]; ok { // ordinary file
				arch.Files = append(arch.Files, file)
			} else if strings.HasPrefix(file.Name, "proxy/") { // proxy file
				arch.Files = append(arch.Files, file)
			} else if data, ok := updatedGolden[file.Name]; ok { // golden file
				arch.Files = append(arch.Files, txtar.File{Name: file.Name, Data: data})
				delete(updatedGolden, file.Name)
			}
		}
	}

	// ...but insert new golden files after their first reference.
	var newGoldenFiles []txtar.File
	for filename, data := range updatedGolden {
		// TODO(rfindley): it looks like this implicitly removes trailing newlines
		// from golden content. Is there any way to fix that? Perhaps we should
		// just make the diff tolerant of missing newlines?
		newGoldenFiles = append(newGoldenFiles, txtar.File{Name: filename, Data: data})
	}
	// Sort new golden files lexically.
	sort.Slice(newGoldenFiles, func(i, j int) bool {
		return newGoldenFiles[i].Name < newGoldenFiles[j].Name
	})
	for _, g := range newGoldenFiles {
		insertAt := len(arch.Files)
		if firstRef := firstReferences[g.Name]; firstRef != "" {
			for i, f := range arch.Files {
				if f.Name == firstRef {
					// Insert alphabetically among golden files following the test file.
					for i++; i < len(arch.Files); i++ {
						f := arch.Files[i]
						if !strings.HasPrefix(f.Name, "@") || f.Name >= g.Name {
							insertAt = i
							break
						}
					}
					break
				}
			}
		}
		arch.Files = slices.Insert(arch.Files, insertAt, g)
	}

	return txtar.Format(arch), nil
}

// newEnv creates a new environment for a marker test.
//
// TODO(rfindley): simplify and refactor the construction of testing
// environments across integration tests, marker tests, and benchmarks.
func newEnv(t *testing.T, cache *cache.Cache, files, proxyFiles map[string][]byte, writeGoSum []string, config fake.EditorConfig) *integration.Env {
	sandbox, err := fake.NewSandbox(&fake.SandboxConfig{
		RootDir:    t.TempDir(),
		Files:      files,
		ProxyFiles: proxyFiles,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range writeGoSum {
		if _, err := sandbox.RunGoCommand(context.Background(), dir, "list", []string{"-mod=mod", "..."}, []string{"GOWORK=off"}, true); err != nil {
			t.Fatal(err)
		}
	}

	// Put a debug instance in the context to prevent logging to stderr.
	// See associated TODO in runner.go: we should revisit this pattern.
	ctx := context.Background()
	ctx = debug.WithInstance(ctx, "off")

	awaiter := integration.NewAwaiter(sandbox.Workdir)
	ss := lsprpc.NewStreamServer(cache, false, nil)
	server := servertest.NewPipeServer(ss, jsonrpc2.NewRawStream)
	editor, err := fake.NewEditor(sandbox, config).Connect(ctx, server, awaiter.Hooks())
	if err != nil {
		sandbox.Close() // ignore error
		t.Fatal(err)
	}
	if err := awaiter.Await(ctx, integration.InitialWorkspaceLoad); err != nil {
		sandbox.Close() // ignore error
		t.Fatal(err)
	}
	return &integration.Env{
		T:       t,
		Ctx:     ctx,
		Editor:  editor,
		Sandbox: sandbox,
		Awaiter: awaiter,
	}
}

// A markerTestRun holds the state of one run of a marker test archive.
type markerTestRun struct {
	test     *markerTest
	env      *integration.Env
	settings map[string]any

	// Collected information.
	// Each @diag/@quickfix marker eliminates an entry from diags.
	values map[expect.Identifier]any
	diags  map[protocol.Location][]protocol.Diagnostic // diagnostics by position; location end == start

	// Notes that weren't associated with a top-level marker func. They may be
	// consumed by another marker (e.g. @codelenses collects @codelens markers).
	// Any notes that aren't consumed are flagged as an error.
	extraNotes map[protocol.DocumentURI]map[string][]*expect.Note
}

// sprintf returns a formatted string after applying pre-processing to
// arguments of the following types:
//   - token.Pos: formatted using (*markerTestRun).fmtPos
//   - protocol.Location: formatted using (*markerTestRun).fmtLoc
func (c *marker) sprintf(format string, args ...any) string {
	if false {
		_ = fmt.Sprintf(format, args...) // enable vet printf checker
	}
	var args2 []any
	for _, arg := range args {
		switch arg := arg.(type) {
		case token.Pos:
			args2 = append(args2, c.run.fmtPos(arg))
		case protocol.Location:
			args2 = append(args2, c.run.fmtLoc(arg))
		default:
			args2 = append(args2, arg)
		}
	}
	return fmt.Sprintf(format, args2...)
}

// fmtPos formats the given pos in the context of the test, using
// archive-relative paths for files and including the line number in the full
// archive file.
func (run *markerTestRun) fmtPos(pos token.Pos) string {
	file := run.test.fset.File(pos)
	if file == nil {
		run.env.T.Errorf("position %d not in test fileset", pos)
		return "<invalid location>"
	}
	m, err := run.env.Editor.Mapper(file.Name())
	if err != nil {
		run.env.T.Errorf("%s", err)
		return "<invalid location>"
	}
	loc, err := m.PosLocation(file, pos, pos)
	if err != nil {
		run.env.T.Errorf("Mapper(%s).PosLocation failed: %v", file.Name(), err)
	}
	return run.fmtLoc(loc)
}

// fmtLoc formats the given location in the context of the test, using
// archive-relative paths for files and including the line number in the full
// archive file.
func (run *markerTestRun) fmtLoc(loc protocol.Location) string {
	if loc == (protocol.Location{}) {
		run.env.T.Errorf("unable to find %s in test archive", loc)
		return "<invalid location>"
	}
	lines := bytes.Count(run.test.archive.Comment, []byte("\n"))
	var name string
	for _, f := range run.test.archive.Files {
		lines++ // -- separator --
		uri := run.env.Sandbox.Workdir.URI(f.Name)
		if uri == loc.URI {
			name = f.Name
			break
		}
		lines += bytes.Count(f.Data, []byte("\n"))
	}
	if name == "" {
		// Fall back to formatting the "lsp" location.
		// These will be in UTF-16, but we probably don't need to clarify that,
		// since it will be implied by the file:// URI format.
		return summarizeLoc(string(loc.URI),
			int(loc.Range.Start.Line), int(loc.Range.Start.Character),
			int(loc.Range.End.Line), int(loc.Range.End.Character))
	}
	name, startLine, startCol, endLine, endCol := run.mapLocation(loc)
	innerSpan := summarizeLoc(name, startLine, startCol, endLine, endCol)
	outerSpan := summarizeLoc(run.test.name, lines+startLine, startCol, lines+endLine, endCol)
	return fmt.Sprintf("%s (%s)", innerSpan, outerSpan)
}

// mapLocation returns the relative path and utf8 span of the corresponding
// location, which must be a valid location in an archive file.
func (run *markerTestRun) mapLocation(loc protocol.Location) (name string, startLine, startCol, endLine, endCol int) {
	// Note: Editor.Mapper fails if loc.URI is not open, but we always open all
	// archive files, so this is probably OK.
	//
	// In the future, we may want to have the editor read contents from disk if
	// the URI is not open.
	name = run.env.Sandbox.Workdir.URIToPath(loc.URI)
	m, err := run.env.Editor.Mapper(name)
	if err != nil {
		run.env.T.Errorf("internal error: %v", err)
		return
	}
	start, end, err := m.RangeOffsets(loc.Range)
	if err != nil {
		run.env.T.Errorf("error formatting location %s: %v", loc, err)
		return
	}
	startLine, startCol = m.OffsetLineCol8(start)
	endLine, endCol = m.OffsetLineCol8(end)
	return name, startLine, startCol, endLine, endCol
}

// fmtLocForGolden is like fmtLoc, but chooses more succinct and stable
// formatting, such as would be used for formatting locations in Golden
// content.
func (run *markerTestRun) fmtLocForGolden(loc protocol.Location) string {
	if loc == (protocol.Location{}) {
		return "<invalid location>"
	}
	name := run.env.Sandbox.Workdir.URIToPath(loc.URI)
	// Note: we check IsAbs on filepaths rather than the slash-ified name for
	// accurate handling of windows drive letters.
	if filepath.IsAbs(filepath.FromSlash(name)) {
		// Don't format any position information in this case, since it will be
		// volatile.
		return "<external>"
	}
	return summarizeLoc(run.mapLocation(loc))
}

// summarizeLoc formats a summary of the given location, in the form
//
//	<name>:<startLine>:<startCol>[-[<endLine>:]endCol]
func summarizeLoc(name string, startLine, startCol, endLine, endCol int) string {
	span := fmt.Sprintf("%s:%d:%d", name, startLine, startCol)
	if startLine != endLine || startCol != endCol {
		span += "-"
		if endLine != startLine {
			span += fmt.Sprintf("%d:", endLine)
		}
		span += fmt.Sprintf("%d", endCol)
	}
	return span
}

// ---- converters ----

// Types with special handling.
var (
	goldenType        = reflect.TypeOf(&Golden{})
	markerType        = reflect.TypeOf(marker{})
	stringMatcherType = reflect.TypeOf(stringMatcher{})
)

// Custom conversions.
//
// These functions are called after valueMarkerFuncs have run to convert
// arguments into the desired parameter types.
//
// Converters should return an error rather than calling marker.errorf().
var customConverters = map[reflect.Type]func(marker, any) (any, error){
	reflect.TypeOf(protocol.Location{}): converter(convertLocation),
	reflect.TypeOf(completionLabel("")): converter(convertCompletionLabel),
}

// converter transforms a typed argument conversion function to an untyped
// conversion function.
func converter[T any](f func(marker, any) (T, error)) func(marker, any) (any, error) {
	return func(m marker, arg any) (any, error) {
		return f(m, arg)
	}
}

func convert(mark marker, arg any, paramType reflect.Type) (any, error) {
	// Handle stringMatcher and golden parameters before resolving identifiers,
	// because golden content lives in a separate namespace from other
	// identifiers.
	// TODO(rfindley): simplify by flattening the namespace. This interacts
	// poorly with named argument resolution.
	switch paramType {
	case stringMatcherType:
		return convertStringMatcher(mark, arg)
	case goldenType:
		id, ok := arg.(expect.Identifier)
		if !ok {
			return nil, fmt.Errorf("invalid input type %T: golden key must be an identifier", arg)
		}
		return mark.getGolden(id), nil
	}
	if id, ok := arg.(expect.Identifier); ok {
		if arg2, ok := mark.run.values[id]; ok {
			arg = arg2
		}
	}
	if converter, ok := customConverters[paramType]; ok {
		arg2, err := converter(mark, arg)
		if err != nil {
			return nil, err
		}
		arg = arg2
	}
	if reflect.TypeOf(arg).AssignableTo(paramType) {
		return arg, nil // no conversion required
	}
	return nil, fmt.Errorf("cannot convert %v (%T) to %s", arg, arg, paramType)
}

// convertNamedArgLocation is a workaround for converting locations referenced
// by a named argument. See the TODO in [convert]: this wouldn't be necessary
// if we flattened the namespace such that golden content lived in the same
// namespace as values.
func convertNamedArgLocation(mark marker, arg any) (protocol.Location, error) {
	if id, ok := arg.(expect.Identifier); ok {
		if v, ok := mark.run.values[id]; ok {
			if loc, ok := v.(protocol.Location); ok {
				return loc, nil
			} else {
				return protocol.Location{}, fmt.Errorf("invalid location value %v", v)
			}
		}
	}
	return convertLocation(mark, arg)
}

// convertLocation converts a string or regexp argument into the protocol
// location corresponding to the first position of the string (or first match
// of the regexp) in the line preceding the note.
func convertLocation(mark marker, arg any) (protocol.Location, error) {
	// matchContent is used to match the given argument against the file content
	// starting at the marker line.
	var matchContent func([]byte) (int, int, error)

	switch arg := arg.(type) {
	case protocol.Location:
		return arg, nil // nothing to do
	case string:
		matchContent = func(content []byte) (int, int, error) {
			idx := bytes.Index(content, []byte(arg))
			if idx < 0 {
				return 0, 0, fmt.Errorf("substring %q not found", arg)
			}
			return idx, idx + len(arg), nil
		}
	case *regexp.Regexp:
		matchContent = func(content []byte) (int, int, error) {
			matches := arg.FindSubmatchIndex(content)
			if len(matches) == 0 {
				return 0, 0, fmt.Errorf("no match for regexp %q", arg)
			}
			switch len(matches) {
			case 2:
				// no subgroups: return the range of the regexp expression
				return matches[0], matches[1], nil
			case 4:
				// one subgroup: return its range
				return matches[2], matches[3], nil
			default:
				return 0, 0, fmt.Errorf("invalid location regexp %q: expect either 0 or 1 subgroups, got %d", arg, len(matches)/2-1)
			}
		}
	default:
		return protocol.Location{}, fmt.Errorf("cannot convert argument type %T to location (must be a string or regexp to match the preceding line)", arg)
	}

	// Now use matchFunc to match a range starting on the marker line.

	file := mark.run.test.fset.File(mark.note.Pos)
	posn := safetoken.Position(file, mark.note.Pos)
	lineStart := file.LineStart(posn.Line)
	lineStartOff, lineEndOff, err := safetoken.Offsets(file, lineStart, mark.note.Pos)
	if err != nil {
		return protocol.Location{}, err
	}
	m := mark.mapper()
	start, end, err := matchContent(m.Content[lineStartOff:])
	if err != nil {
		return protocol.Location{}, err
	}
	startOff, endOff := lineStartOff+start, lineStartOff+end
	if startOff > lineEndOff {
		// The start of the match must be between the start of the line and the
		// marker position (inclusive).
		return protocol.Location{}, fmt.Errorf("no matching range found starting on the current line")
	}
	return m.OffsetLocation(startOff, endOff)
}

// completionLabel is a special parameter type that may be converted from a
// string literal, or extracted from a completion item.
//
// See [convertCompletionLabel].
type completionLabel string

// convertCompletionLabel coerces an argument to a [completionLabel] parameter
// type.
//
// If the arg is a string, it is trivially converted. If the arg is a
// completionItem, its label is extracted.
//
// This allows us to stage a migration of the "snippet" marker to a simpler
// model where the completion label can just be listed explicitly.
func convertCompletionLabel(mark marker, arg any) (completionLabel, error) {
	switch arg := arg.(type) {
	case string:
		return completionLabel(arg), nil
	case completionItem:
		return completionLabel(arg.Label), nil
	default:
		return "", fmt.Errorf("cannot convert argument type %T to completion label (must be a string or completion item)", arg)
	}
}

// convertStringMatcher converts a string, regexp, or identifier
// argument into a stringMatcher. The string is a substring of the
// expected error, the regexp is a pattern than matches the expected
// error, and the identifier is a golden file containing the expected
// error.
func convertStringMatcher(mark marker, arg any) (stringMatcher, error) {
	switch arg := arg.(type) {
	case string:
		return stringMatcher{substr: arg}, nil
	case *regexp.Regexp:
		return stringMatcher{pattern: arg}, nil
	case expect.Identifier:
		golden := mark.getGolden(arg)
		return stringMatcher{golden: golden}, nil
	default:
		return stringMatcher{}, fmt.Errorf("cannot convert %T to wantError (want: string, regexp, or identifier)", arg)
	}
}

// A stringMatcher represents an expectation of a specific string value.
//
// It may be indicated in one of three ways, in 'expect' notation:
//   - an identifier 'foo', to compare (exactly) with the contents of the golden
//     section @foo;
//   - a pattern expression re"ab.*c", to match against a regular expression;
//   - a string literal "abc", to check for a substring.
type stringMatcher struct {
	golden  *Golden
	pattern *regexp.Regexp
	substr  string
}

// empty reports whether the receiver is an empty stringMatcher.
func (sm stringMatcher) empty() bool {
	return sm.golden == nil && sm.pattern == nil && sm.substr == ""
}

func (sm stringMatcher) String() string {
	if sm.golden != nil {
		return fmt.Sprintf("content from @%s entry", sm.golden.id)
	} else if sm.pattern != nil {
		return fmt.Sprintf("content matching %#q", sm.pattern)
	} else {
		return fmt.Sprintf("content with substring %q", sm.substr)
	}
}

// checkErr asserts that the given error matches the stringMatcher's expectations.
func (sm stringMatcher) checkErr(mark marker, err error) {
	if err == nil {
		mark.errorf("@%s succeeded unexpectedly, want %v", mark.note.Name, sm)
		return
	}
	sm.check(mark, err.Error())
}

// check asserts that the given content matches the stringMatcher's expectations.
func (sm stringMatcher) check(mark marker, got string) {
	if sm.golden != nil {
		compareGolden(mark, []byte(got), sm.golden)
	} else if sm.pattern != nil {
		// Content must match the regular expression pattern.
		if !sm.pattern.MatchString(got) {
			mark.errorf("got %q, does not match pattern %#q", got, sm.pattern)
		}

	} else if !strings.Contains(got, sm.substr) {
		// Content must contain the expected substring.
		mark.errorf("got %q, want substring %q", got, sm.substr)
	}
}

// checkChangedFiles compares the files changed by an operation with their expected (golden) state.
func checkChangedFiles(mark marker, changed map[string][]byte, golden *Golden) {
	// Check changed files match expectations.
	for filename, got := range changed {
		if want, ok := golden.Get(mark.T(), filename, got); !ok {
			mark.errorf("%s: unexpected change to file %s; got:\n%s",
				mark.note.Name, filename, got)

		} else if string(got) != string(want) {
			mark.errorf("%s: wrong file content for %s: got:\n%s\nwant:\n%s\ndiff:\n%s",
				mark.note.Name, filename, got, want,
				compare.Bytes(want, got))
		}
	}

	// Report unmet expectations.
	for filename := range golden.data {
		if _, ok := changed[filename]; !ok {
			want, _ := golden.Get(mark.T(), filename, nil)
			mark.errorf("%s: missing change to file %s; want:\n%s",
				mark.note.Name, filename, want)
		}
	}
}

// checkDiffs computes unified diffs for each changed file, and compares with
// the diff content stored in the given golden directory.
func checkDiffs(mark marker, changed map[string][]byte, golden *Golden) {
	diffs := make(map[string]string)
	for name, after := range changed {
		before := mark.run.env.FileContent(name)
		// TODO(golang/go#64023): switch back to diff.Strings.
		// The attached issue is only one obstacle to switching.
		// Another is that different diff algorithms produce
		// different results, so if we commit diffs in test
		// expectations, then we need to either (1) state
		// which diff implementation they use and never change
		// it, or (2) don't compare diffs, but instead apply
		// the "want" diff and check that it produces the
		// "got" output. Option 2 is more robust, as it allows
		// the test expectation to use any valid diff.
		edits := myers.ComputeEdits(before, string(after))
		d, err := diff.ToUnified("before", "after", before, edits, 0)
		if err != nil {
			// Can't happen: edits are consistent.
			log.Fatalf("internal error in diff.ToUnified: %v", err)
		}
		// Trim the unified header from diffs, as it is unnecessary and repetitive.
		difflines := strings.Split(d, "\n")
		if len(difflines) >= 2 && strings.HasPrefix(difflines[1], "+++") {
			diffs[name] = strings.Join(difflines[2:], "\n")
		} else {
			diffs[name] = d
		}
	}
	// Check changed files match expectations.
	for filename, got := range diffs {
		if want, ok := golden.Get(mark.T(), filename, []byte(got)); !ok {
			mark.errorf("%s: unexpected change to file %s; got diff:\n%s",
				mark.note.Name, filename, got)

		} else if got != string(want) {
			mark.errorf("%s: wrong diff for %s:\n\ngot:\n%s\n\nwant:\n%s\n",
				mark.note.Name, filename, got, want)
		}
	}
	// Report unmet expectations.
	for filename := range golden.data {
		if _, ok := changed[filename]; !ok {
			want, _ := golden.Get(mark.T(), filename, nil)
			mark.errorf("%s: missing change to file %s; want:\n%s",
				mark.note.Name, filename, want)
		}
	}
}

// ---- marker functions ----

// TODO(rfindley): consolidate documentation of these markers. They are already
// documented above, so much of the documentation here is redundant.

// completionItem is a simplified summary of a completion item.
type completionItem struct {
	Label, Detail, Kind, Documentation string
}

func completionItemMarker(mark marker, label string, other ...string) completionItem {
	if len(other) > 3 {
		mark.errorf("too many arguments to @item: expect at most 4")
	}
	item := completionItem{
		Label: label,
	}
	if len(other) > 0 {
		item.Detail = other[0]
	}
	if len(other) > 1 {
		item.Kind = other[1]
	}
	if len(other) > 2 {
		item.Documentation = other[2]
	}
	return item
}

func rankMarker(mark marker, src protocol.Location, items ...completionLabel) {
	// Separate positive and negative items (expectations).
	var pos, neg []completionLabel
	for _, item := range items {
		if strings.HasPrefix(string(item), "!") {
			neg = append(neg, item)
		} else {
			pos = append(pos, item)
		}
	}

	// Collect results that are present in items, preserving their order.
	list := mark.run.env.Completion(src)
	var got []string
	for _, g := range list.Items {
		for _, w := range pos {
			if g.Label == string(w) {
				got = append(got, g.Label)
				break
			}
		}
		for _, w := range neg {
			if g.Label == string(w[len("!"):]) {
				mark.errorf("got unwanted completion: %s", g.Label)
				break
			}
		}
	}
	var want []string
	for _, w := range pos {
		want = append(want, string(w))
	}
	if diff := cmp.Diff(want, got); diff != "" {
		mark.errorf("completion rankings do not match (-want +got):\n%s", diff)
	}
}

func snippetMarker(mark marker, src protocol.Location, label completionLabel, want string) {
	list := mark.run.env.Completion(src)
	var (
		found bool
		got   string
		all   []string // for errors
	)
	items := filterBuiltinsAndKeywords(mark, list.Items)
	for _, i := range items {
		all = append(all, i.Label)
		if i.Label == string(label) {
			found = true
			if i.TextEdit != nil {
				if edit, err := protocol.SelectCompletionTextEdit(i, false); err == nil {
					got = edit.NewText
				}
			}
			break
		}
	}
	if !found {
		mark.errorf("no completion item found matching %s (got: %v)", label, all)
		return
	}
	if got != want {
		mark.errorf("snippets do not match: got:\n%q\nwant:\n%q", got, want)
	}
}

// completeMarker implements the @complete marker, running
// textDocument/completion at the given src location and asserting that the
// results match the expected results.
func completeMarker(mark marker, src protocol.Location, want ...completionItem) {
	list := mark.run.env.Completion(src)
	items := filterBuiltinsAndKeywords(mark, list.Items)
	var got []completionItem
	for i, item := range items {
		simplified := completionItem{
			Label:  item.Label,
			Detail: item.Detail,
			Kind:   fmt.Sprint(item.Kind),
		}
		if item.Documentation != nil {
			switch v := item.Documentation.Value.(type) {
			case string:
				simplified.Documentation = v
			case protocol.MarkupContent:
				simplified.Documentation = strings.TrimSpace(v.Value) // trim newlines
			}
		}
		// Support short-hand notation: if Detail, Kind, or Documentation are omitted from the
		// item, don't match them.
		if i < len(want) {
			if want[i].Detail == "" {
				simplified.Detail = ""
			}
			if want[i].Kind == "" {
				simplified.Kind = ""
			}
			if want[i].Documentation == "" {
				simplified.Documentation = ""
			}
		}
		got = append(got, simplified)
	}
	if len(want) == 0 {
		want = nil // got is nil if empty
	}
	if diff := cmp.Diff(want, got); diff != "" {
		mark.errorf("Completion(...) returned unexpect results (-want +got):\n%s", diff)
	}
}

// filterBuiltinsAndKeywords filters out builtins and keywords from completion
// results.
//
// It over-approximates, and does not detect if builtins are shadowed.
func filterBuiltinsAndKeywords(mark marker, items []protocol.CompletionItem) []protocol.CompletionItem {
	keep := 0
	for _, item := range items {
		if mark.run.test.filterKeywords && item.Kind == protocol.KeywordCompletion {
			continue
		}
		if mark.run.test.filterBuiltins && types.Universe.Lookup(item.Label) != nil {
			continue
		}
		items[keep] = item
		keep++
	}
	return items[:keep]
}

// acceptCompletionMarker implements the @acceptCompletion marker, running
// textDocument/completion at the given src location and accepting the
// candidate with the given label. The resulting source must match the provided
// golden content.
func acceptCompletionMarker(mark marker, src protocol.Location, label string, golden *Golden) {
	list := mark.run.env.Completion(src)
	var selected *protocol.CompletionItem
	for _, item := range list.Items {
		if item.Label == label {
			selected = &item
			break
		}
	}
	if selected == nil {
		mark.errorf("Completion(...) did not return an item labeled %q", label)
		return
	}
	edit, err := protocol.SelectCompletionTextEdit(*selected, false)
	if err != nil {
		mark.errorf("Completion(...) did not return a valid edit: %v", err)
		return
	}
	filename := mark.path()
	mapper := mark.mapper()
	patched, _, err := protocol.ApplyEdits(mapper, append([]protocol.TextEdit{edit}, selected.AdditionalTextEdits...))

	if err != nil {
		mark.errorf("ApplyProtocolEdits failed: %v", err)
		return
	}
	changes := map[string][]byte{filename: patched}
	// Check the file state.
	checkChangedFiles(mark, changes, golden)
}

// defMarker implements the @def marker, running textDocument/definition at
// the given src location and asserting that there is exactly one resulting
// location, matching dst.
//
// TODO(rfindley): support a variadic destination set.
func defMarker(mark marker, src, dst protocol.Location) {
	got := mark.run.env.GoToDefinition(src)
	if got != dst {
		mark.errorf("definition location does not match:\n\tgot: %s\n\twant %s",
			mark.run.fmtLoc(got), mark.run.fmtLoc(dst))
	}
}

func typedefMarker(mark marker, src, dst protocol.Location) {
	got := mark.run.env.TypeDefinition(src)
	if got != dst {
		mark.errorf("type definition location does not match:\n\tgot: %s\n\twant %s",
			mark.run.fmtLoc(got), mark.run.fmtLoc(dst))
	}
}

func foldingRangeMarker(mark marker, g *Golden) {
	env := mark.run.env
	ranges, err := mark.server().FoldingRange(env.Ctx, &protocol.FoldingRangeParams{
		TextDocument: mark.document(),
	})
	if err != nil {
		mark.errorf("foldingRange failed: %v", err)
		return
	}
	var edits []protocol.TextEdit
	insert := func(line, char uint32, text string) {
		pos := protocol.Position{Line: line, Character: char}
		edits = append(edits, protocol.TextEdit{
			Range: protocol.Range{
				Start: pos,
				End:   pos,
			},
			NewText: text,
		})
	}
	for i, rng := range ranges {
		insert(rng.StartLine, rng.StartCharacter, fmt.Sprintf("<%d kind=%q>", i, rng.Kind))
		insert(rng.EndLine, rng.EndCharacter, fmt.Sprintf("</%d>", i))
	}
	filename := mark.path()
	mapper, err := env.Editor.Mapper(filename)
	if err != nil {
		mark.errorf("Editor.Mapper(%s) failed: %v", filename, err)
		return
	}
	got, _, err := protocol.ApplyEdits(mapper, edits)
	if err != nil {
		mark.errorf("ApplyProtocolEdits failed: %v", err)
		return
	}
	want, _ := g.Get(mark.T(), "", got)
	if diff := compare.Bytes(want, got); diff != "" {
		mark.errorf("foldingRange mismatch:\n%s", diff)
	}
}

// formatMarker implements the @format marker.
func formatMarker(mark marker, golden *Golden) {
	edits, err := mark.server().Formatting(mark.ctx(), &protocol.DocumentFormattingParams{
		TextDocument: mark.document(),
	})
	var got []byte
	if err != nil {
		got = []byte(err.Error() + "\n") // all golden content is newline terminated
	} else {
		env := mark.run.env
		filename := mark.path()
		mapper, err := env.Editor.Mapper(filename)
		if err != nil {
			mark.errorf("Editor.Mapper(%s) failed: %v", filename, err)
		}

		got, _, err = protocol.ApplyEdits(mapper, edits)
		if err != nil {
			mark.errorf("ApplyProtocolEdits failed: %v", err)
			return
		}
	}

	compareGolden(mark, got, golden)
}

func highlightLocationMarker(mark marker, loc protocol.Location, kindName expect.Identifier) protocol.DocumentHighlight {
	var kind protocol.DocumentHighlightKind
	switch kindName {
	case "read":
		kind = protocol.Read
	case "write":
		kind = protocol.Write
	case "text":
		kind = protocol.Text
	default:
		mark.errorf("invalid highlight kind: %q", kindName)
	}

	return protocol.DocumentHighlight{
		Range: loc.Range,
		Kind:  kind,
	}
}
func sortDocumentHighlights(s []protocol.DocumentHighlight) {
	sort.Slice(s, func(i, j int) bool {
		return protocol.CompareRange(s[i].Range, s[j].Range) < 0
	})
}

// highlightAllMarker makes textDocument/highlight
// requests at locations of equivalence classes. Given input
// highlightall(X1, X2, ..., Xn), the marker checks
// highlight(X1) = highlight(X2) = ... = highlight(Xn) = {X1, X2, ..., Xn}.
// It is not the general rule for all highlighting, and use @highlight
// for asymmetric cases.
//
// TODO(b/288111111): this is a bit of a hack. We should probably
// have a more general way of testing that a function is idempotent.
func highlightAllMarker(mark marker, all ...protocol.DocumentHighlight) {
	sortDocumentHighlights(all)
	for _, src := range all {
		loc := protocol.Location{URI: mark.uri(), Range: src.Range}
		got := mark.run.env.DocumentHighlight(loc)
		sortDocumentHighlights(got)

		if d := cmp.Diff(all, got); d != "" {
			mark.errorf("DocumentHighlight(%v) mismatch (-want +got):\n%s", loc, d)
		}
	}
}

func highlightMarker(mark marker, src protocol.DocumentHighlight, dsts ...protocol.DocumentHighlight) {
	loc := protocol.Location{URI: mark.uri(), Range: src.Range}
	got := mark.run.env.DocumentHighlight(loc)

	sortDocumentHighlights(got)
	sortDocumentHighlights(dsts)

	if diff := cmp.Diff(dsts, got, cmpopts.EquateEmpty()); diff != "" {
		mark.errorf("DocumentHighlight(%v) mismatch (-want +got):\n%s", src, diff)
	}
}

func hoverMarker(mark marker, src, dst protocol.Location, sc stringMatcher) {
	content, gotDst := mark.run.env.Hover(src)
	if gotDst != dst {
		mark.errorf("hover location does not match:\n\tgot: %s\n\twant %s)", mark.run.fmtLoc(gotDst), mark.run.fmtLoc(dst))
	}
	gotMD := ""
	if content != nil {
		gotMD = content.Value
	}
	sc.check(mark, gotMD)
}

func hoverErrMarker(mark marker, src protocol.Location, em stringMatcher) {
	_, _, err := mark.editor().Hover(mark.ctx(), src)
	em.checkErr(mark, err)
}

// locMarker implements the @loc marker.
func locMarker(mark marker, loc protocol.Location) protocol.Location { return loc }

// defLocMarker implements the @defloc marker, which binds a location to the
// (first) result of a jump-to-definition request.
func defLocMarker(mark marker, loc protocol.Location) protocol.Location {
	return mark.run.env.GoToDefinition(loc)
}

// diagMarker implements the @diag marker. It eliminates diagnostics from
// the observed set in mark.test.
func diagMarker(mark marker, loc protocol.Location, re *regexp.Regexp) {
	exact := namedArg(mark, "exact", false)
	if _, ok := removeDiagnostic(mark, loc, exact, re); !ok {
		mark.errorf("no diagnostic at %v matches %q", loc, re)
	}
}

// removeDiagnostic looks for a diagnostic matching loc at the given position.
//
// If found, it returns (diag, true), and eliminates the matched diagnostic
// from the unmatched set.
//
// If not found, it returns (protocol.Diagnostic{}, false).
func removeDiagnostic(mark marker, loc protocol.Location, matchEnd bool, re *regexp.Regexp) (protocol.Diagnostic, bool) {
	key := loc
	key.Range.End = key.Range.Start // diagnostics ignore end position.
	diags := mark.run.diags[key]
	for i, diag := range diags {
		if re.MatchString(diag.Message) && (!matchEnd || diag.Range.End == loc.Range.End) {
			mark.run.diags[key] = append(diags[:i], diags[i+1:]...)
			return diag, true
		}
	}
	return protocol.Diagnostic{}, false
}

// renameMarker implements the @rename(location, new, golden) marker.
func renameMarker(mark marker, loc protocol.Location, newName string, golden *Golden) {
	changed, err := rename(mark.run.env, loc, newName)
	if err != nil {
		mark.errorf("rename failed: %v. (Use @renameerr for expected errors.)", err)
		return
	}
	checkDiffs(mark, changed, golden)
}

// renameErrMarker implements the @renamererr(location, new, error) marker.
func renameErrMarker(mark marker, loc protocol.Location, newName string, wantErr stringMatcher) {
	_, err := rename(mark.run.env, loc, newName)
	wantErr.checkErr(mark, err)
}

func selectionRangeMarker(mark marker, loc protocol.Location, g *Golden) {
	ranges, err := mark.server().SelectionRange(mark.ctx(), &protocol.SelectionRangeParams{
		TextDocument: mark.document(),
		Positions:    []protocol.Position{loc.Range.Start},
	})
	if err != nil {
		mark.errorf("SelectionRange failed: %v", err)
		return
	}
	var buf bytes.Buffer
	m := mark.mapper()
	for i, path := range ranges {
		fmt.Fprintf(&buf, "Ranges %d:", i)
		rng := path
		for {
			s, e, err := m.RangeOffsets(rng.Range)
			if err != nil {
				mark.errorf("RangeOffsets failed: %v", err)
				return
			}

			var snippet string
			if e-s < 30 {
				snippet = string(m.Content[s:e])
			} else {
				snippet = string(m.Content[s:s+15]) + "..." + string(m.Content[e-15:e])
			}

			fmt.Fprintf(&buf, "\n\t%v %q", rng.Range, strings.ReplaceAll(snippet, "\n", "\\n"))

			if rng.Parent == nil {
				break
			}
			rng = *rng.Parent
		}
		buf.WriteRune('\n')
	}
	compareGolden(mark, buf.Bytes(), g)
}

func tokenMarker(mark marker, loc protocol.Location, tokenType, mod string) {
	tokens := mark.run.env.SemanticTokensRange(loc)
	if len(tokens) != 1 {
		mark.errorf("got %d tokens, want 1", len(tokens))
		return
	}
	tok := tokens[0]
	if tok.TokenType != tokenType {
		mark.errorf("token type = %q, want %q", tok.TokenType, tokenType)
	}
	if tok.Mod != mod {
		mark.errorf("token mod = %q, want %q", tok.Mod, mod)
	}
}

func signatureMarker(mark marker, src protocol.Location, label string, active int64) {
	got := mark.run.env.SignatureHelp(src)
	var gotLabels []string // for better error messages
	if got != nil {
		for _, s := range got.Signatures {
			gotLabels = append(gotLabels, s.Label)
		}
	}
	if label == "" {
		// A null result is expected.
		// (There's no point having a @signatureerr marker
		// because the server handler suppresses all errors.)
		if got != nil && len(gotLabels) > 0 {
			mark.errorf("signatureHelp = %v, want 0 signatures", gotLabels)
		}
		return
	}
	if got == nil || len(got.Signatures) != 1 {
		mark.errorf("signatureHelp = %v, want exactly 1 signature", gotLabels)
		return
	}
	if got := gotLabels[0]; got != label {
		mark.errorf("signatureHelp: got label %q, want %q", got, label)
	}
	if got := int64(got.ActiveParameter); got != active {
		mark.errorf("signatureHelp: got active parameter %d, want %d", got, active)
	}
}

// rename returns the new contents of the files that would be modified
// by renaming the identifier at loc to newName.
func rename(env *integration.Env, loc protocol.Location, newName string) (map[string][]byte, error) {
	// We call Server.Rename directly, instead of
	//   env.Editor.Rename(env.Ctx, loc, newName)
	// to isolate Rename from PrepareRename, and because we don't
	// want to modify the file system in a scenario with multiple
	// @rename markers.

	wsedit, err := env.Editor.Server.Rename(env.Ctx, &protocol.RenameParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: loc.URI},
		Position:     loc.Range.Start,
		NewName:      newName,
	})
	if err != nil {
		return nil, err
	}
	return changedFiles(env, wsedit.DocumentChanges)
}

// changedFiles applies the given sequence of document changes to the
// editor buffer content, recording the final contents in the returned map.
// The actual editor state is not changed.
// Deleted files are indicated by a content of []byte(nil).
//
// See also:
//   - Editor.applyWorkspaceEdit ../integration/fake/editor.go for the
//     implementation of this operation used in normal testing.
//   - cmdClient.applyWorkspaceEdit in ../../../cmd/cmd.go for the
//     CLI variant.
func changedFiles(env *integration.Env, changes []protocol.DocumentChange) (map[string][]byte, error) {
	uriToPath := env.Sandbox.Workdir.URIToPath

	// latest maps each updated file name to a mapper holding its
	// current contents, or nil if the file has been deleted.
	latest := make(map[protocol.DocumentURI]*protocol.Mapper)

	// read reads a file. It returns an error if the file never
	// existed or was deleted.
	read := func(uri protocol.DocumentURI) (*protocol.Mapper, error) {
		if m, ok := latest[uri]; ok {
			if m == nil {
				return nil, fmt.Errorf("read: file %s was deleted", uri)
			}
			return m, nil
		}
		return env.Editor.Mapper(uriToPath(uri))
	}

	// write (over)writes a file. A nil content indicates a deletion.
	write := func(uri protocol.DocumentURI, content []byte) {
		var m *protocol.Mapper
		if content != nil {
			m = protocol.NewMapper(uri, content)
		}
		latest[uri] = m
	}

	// Process the sequence of changes.
	for _, change := range changes {
		switch {
		case change.TextDocumentEdit != nil:
			uri := change.TextDocumentEdit.TextDocument.URI
			m, err := read(uri)
			if err != nil {
				return nil, err // missing
			}
			patched, _, err := protocol.ApplyEdits(m, protocol.AsTextEdits(change.TextDocumentEdit.Edits))
			if err != nil {
				return nil, err // bad edit
			}
			write(uri, patched)

		case change.RenameFile != nil:
			old := change.RenameFile.OldURI
			m, err := read(old)
			if err != nil {
				return nil, err // missing
			}
			write(old, nil)

			new := change.RenameFile.NewURI
			if _, err := read(old); err == nil {
				return nil, fmt.Errorf("RenameFile: destination %s exists", new)
			}
			write(new, m.Content)

		case change.CreateFile != nil:
			uri := change.CreateFile.URI
			if _, err := read(uri); err == nil {
				return nil, fmt.Errorf("CreateFile %s: file exists", uri)
			}
			write(uri, []byte("")) // initially empty

		case change.DeleteFile != nil:
			uri := change.DeleteFile.URI
			if _, err := read(uri); err != nil {
				return nil, fmt.Errorf("DeleteFile %s: file does not exist", uri)
			}
			write(uri, nil)

		default:
			return nil, fmt.Errorf("invalid DocumentChange")
		}
	}

	// Convert into result form.
	result := make(map[string][]byte)
	for uri, mapper := range latest {
		var content []byte
		if mapper != nil {
			content = mapper.Content
		}
		result[uriToPath(uri)] = content
	}

	return result, nil
}

func codeActionMarker(mark marker, loc protocol.Location, kind string) {
	if !exactlyOneNamedArg(mark, "edit", "result", "err") {
		return
	}

	if end := namedArgFunc(mark, "end", convertNamedArgLocation, protocol.Location{}); end.URI != "" {
		if end.URI != loc.URI {
			panic("unreachable")
		}
		loc.Range.End = end.Range.End
	}

	var (
		edit    = namedArg(mark, "edit", expect.Identifier(""))
		result  = namedArg(mark, "result", expect.Identifier(""))
		wantErr = namedArgFunc(mark, "err", convertStringMatcher, stringMatcher{})
	)

	changed, err := codeAction(mark.run.env, loc.URI, loc.Range, protocol.CodeActionKind(kind), nil)
	if err != nil && wantErr.empty() {
		mark.errorf("codeAction failed: %v", err)
		return
	}

	switch {
	case edit != "":
		g := mark.getGolden(edit)
		checkDiffs(mark, changed, g)
	case result != "":
		g := mark.getGolden(result)
		// Check the file state.
		checkChangedFiles(mark, changed, g)
	case !wantErr.empty():
		wantErr.checkErr(mark, err)
	default:
		panic("unreachable")
	}
}

// codeLensesMarker runs the @codelenses() marker, collecting @codelens marks
// in the current file and comparing with the result of the
// textDocument/codeLens RPC.
func codeLensesMarker(mark marker) {
	type codeLens struct {
		Range protocol.Range
		Title string
	}

	lenses := mark.run.env.CodeLens(mark.path())
	var got []codeLens
	for _, lens := range lenses {
		title := ""
		if lens.Command != nil {
			title = lens.Command.Title
		}
		got = append(got, codeLens{lens.Range, title})
	}

	var want []codeLens
	mark.consumeExtraNotes("codelens", actionMarkerFunc(func(_ marker, loc protocol.Location, title string) {
		want = append(want, codeLens{loc.Range, title})
	}))

	for _, s := range [][]codeLens{got, want} {
		sort.Slice(s, func(i, j int) bool {
			li, lj := s[i], s[j]
			if c := protocol.CompareRange(li.Range, lj.Range); c != 0 {
				return c < 0
			}
			return li.Title < lj.Title
		})
	}

	if diff := cmp.Diff(want, got); diff != "" {
		mark.errorf("codelenses: unexpected diff (-want +got):\n%s", diff)
	}
}

func documentLinkMarker(mark marker, g *Golden) {
	var b bytes.Buffer
	links := mark.run.env.DocumentLink(mark.path())
	for _, l := range links {
		if l.Target == nil {
			mark.errorf("%s: nil link target", l.Range)
			continue
		}
		loc := protocol.Location{URI: mark.uri(), Range: l.Range}
		fmt.Fprintln(&b, mark.run.fmtLocForGolden(loc), *l.Target)
	}

	compareGolden(mark, b.Bytes(), g)
}

// consumeExtraNotes runs the provided func for each extra note with the given
// name, and deletes all matching notes.
func (mark marker) consumeExtraNotes(name string, f func(marker)) {
	uri := mark.uri()
	notes := mark.run.extraNotes[uri][name]
	delete(mark.run.extraNotes[uri], name)

	for _, note := range notes {
		f(marker{run: mark.run, note: note})
	}
}

// quickfixMarker implements the @quickfix(location, regexp,
// kind, golden) marker. It acts like @diag(location, regexp), to set
// the expectation of a diagnostic, but then it applies the "quickfix"
// code action (which must be unique) suggested by the matched diagnostic.
func quickfixMarker(mark marker, loc protocol.Location, re *regexp.Regexp, golden *Golden) {
	loc.Range.End = loc.Range.Start // diagnostics ignore end position.
	// Find and remove the matching diagnostic.
	diag, ok := removeDiagnostic(mark, loc, false, re)
	if !ok {
		mark.errorf("no diagnostic at %v matches %q", loc, re)
		return
	}

	// Apply the fix it suggests.
	changed, err := codeAction(mark.run.env, loc.URI, diag.Range, "quickfix", &diag)
	if err != nil {
		mark.errorf("quickfix failed: %v. (Use @quickfixerr for expected errors.)", err)
		return
	}

	// Check the file state.
	checkDiffs(mark, changed, golden)
}

func quickfixErrMarker(mark marker, loc protocol.Location, re *regexp.Regexp, wantErr stringMatcher) {
	loc.Range.End = loc.Range.Start // diagnostics ignore end position.
	// Find and remove the matching diagnostic.
	diag, ok := removeDiagnostic(mark, loc, false, re)
	if !ok {
		mark.errorf("no diagnostic at %v matches %q", loc, re)
		return
	}

	// Apply the fix it suggests.
	_, err := codeAction(mark.run.env, loc.URI, diag.Range, "quickfix", &diag)
	wantErr.checkErr(mark, err)
}

// codeAction executes a textDocument/codeAction request for the specified
// location and kind. If diag is non-nil, it is used as the code action
// context.
//
// The resulting map contains resulting file contents after the code action is
// applied. Currently, this function does not support code actions that return
// edits directly; it only supports code action commands.
func codeAction(env *integration.Env, uri protocol.DocumentURI, rng protocol.Range, kind protocol.CodeActionKind, diag *protocol.Diagnostic) (map[string][]byte, error) {
	changes, err := codeActionChanges(env, uri, rng, kind, diag)
	if err != nil {
		return nil, err
	}
	return changedFiles(env, changes)
}

// codeActionChanges executes a textDocument/codeAction request for the
// specified location and kind, and captures the resulting document changes.
// If diag is non-nil, it is used as the code action context.
func codeActionChanges(env *integration.Env, uri protocol.DocumentURI, rng protocol.Range, kind protocol.CodeActionKind, diag *protocol.Diagnostic) ([]protocol.DocumentChange, error) {
	// Collect any server-initiated changes created by workspace/applyEdit.
	//
	// We set up this handler immediately, not right before executing the code
	// action command, so we can assert that neither the codeAction request nor
	// codeAction resolve request cause edits as a side effect (golang/go#71405).
	var changes []protocol.DocumentChange
	restore := env.Editor.Client().SetApplyEditHandler(func(ctx context.Context, wsedit *protocol.WorkspaceEdit) error {
		changes = append(changes, wsedit.DocumentChanges...)
		return nil
	})
	defer restore()

	// Request all code actions that apply to the diagnostic.
	// A production client would set Only=[kind],
	// but we can give a better error if we don't filter.
	params := &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Range:        rng,
		Context: protocol.CodeActionContext{
			Only: []protocol.CodeActionKind{protocol.Empty}, // => all
		},
	}
	if diag != nil {
		params.Context.Diagnostics = []protocol.Diagnostic{*diag}
	}

	actions, err := env.Editor.Server.CodeAction(env.Ctx, params)
	if err != nil {
		return nil, err
	}

	// Find the sole candidate CodeAction of exactly the specified kind
	// (e.g. refactor.inline.call).
	var candidates []protocol.CodeAction
	for _, act := range actions {
		if act.Kind == kind {
			candidates = append(candidates, act)
		}
	}
	if len(candidates) != 1 {
		var msg bytes.Buffer
		fmt.Fprintf(&msg, "found %d CodeActions of kind %s for this diagnostic, want 1", len(candidates), kind)
		for _, act := range actions {
			fmt.Fprintf(&msg, "\n\tfound %q (%s)", act.Title, act.Kind)
		}
		return nil, errors.New(msg.String())
	}
	action := candidates[0]

	// Apply the codeAction.
	//
	// Spec:
	//  "If a code action provides an edit and a command, first the edit is
	//  executed and then the command."
	// An action may specify an edit and/or a command, to be
	// applied in that order. But since applyDocumentChanges(env,
	// action.Edit.DocumentChanges) doesn't compose, for now we
	// assert that actions return one or the other.

	// Resolve code action edits first if the client has resolve support
	// and the code action has no edits.
	if action.Edit == nil {
		editSupport, err := env.Editor.EditResolveSupport()
		if err != nil {
			return nil, err
		}
		if editSupport {
			resolved, err := env.Editor.Server.ResolveCodeAction(env.Ctx, &action)
			if err != nil {
				return nil, err
			}
			action.Edit = resolved.Edit
		}
	}

	if action.Edit != nil {
		if len(action.Edit.Changes) > 0 {
			env.T.Errorf("internal error: discarding unexpected CodeAction{Kind=%s, Title=%q}.Edit.Changes", action.Kind, action.Title)
		}
		if action.Edit.DocumentChanges != nil {
			if action.Command != nil {
				env.T.Errorf("internal error: discarding unexpected CodeAction{Kind=%s, Title=%q}.Command", action.Kind, action.Title)
			}
			return action.Edit.DocumentChanges, nil
		}
	}

	if action.Command != nil {
		// This is a typical CodeAction command:
		//
		//   Title:     "Implement error"
		//   Command:   gopls.apply_fix
		//   Arguments: [{"Fix":"stub_methods","URI":".../a.go","Range":...}}]
		//
		// The client makes an ExecuteCommand RPC to the server,
		// which dispatches it to the ApplyFix handler.
		// ApplyFix dispatches to the "stub_methods" fixer (the meat).
		// The server then makes an ApplyEdit RPC to the client,
		// whose WorkspaceEditFunc hook temporarily gathers the edits
		// instead of applying them.

		if _, err := env.Editor.Server.ExecuteCommand(env.Ctx, &protocol.ExecuteCommandParams{
			Command:   action.Command.Command,
			Arguments: action.Command.Arguments,
		}); err != nil {
			return nil, err
		}
		return changes, nil // populated as a side effect of ExecuteCommand
	}

	return nil, nil
}

// refsMarker implements the @refs marker.
func refsMarker(mark marker, src protocol.Location, want ...protocol.Location) {
	refs := func(includeDeclaration bool, want []protocol.Location) error {
		got, err := mark.server().References(mark.ctx(), &protocol.ReferenceParams{
			TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(src),
			Context: protocol.ReferenceContext{
				IncludeDeclaration: includeDeclaration,
			},
		})
		if err != nil {
			return err
		}

		return compareLocations(mark, got, want)
	}

	for _, includeDeclaration := range []bool{false, true} {
		// Ignore first 'want' location if we didn't request the declaration.
		// TODO(adonovan): don't assume a single declaration:
		// there may be >1 if corresponding methods are considered.
		want := want
		if !includeDeclaration && len(want) > 0 {
			want = want[1:]
		}
		if err := refs(includeDeclaration, want); err != nil {
			mark.errorf("refs(includeDeclaration=%t) failed: %v",
				includeDeclaration, err)
		}
	}
}

// implementationMarker implements the @implementation marker.
func implementationMarker(mark marker, src protocol.Location, want ...protocol.Location) {
	got, err := mark.server().Implementation(mark.ctx(), &protocol.ImplementationParams{
		TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(src),
	})
	if err != nil {
		mark.errorf("implementation at %s failed: %v", src, err)
		return
	}
	if err := compareLocations(mark, got, want); err != nil {
		mark.errorf("implementation: %v", err)
	}
}

func itemLocation(item protocol.CallHierarchyItem) protocol.Location {
	return protocol.Location{
		URI:   item.URI,
		Range: item.Range,
	}
}

func incomingCallsMarker(mark marker, src protocol.Location, want ...protocol.Location) {
	getCalls := func(item protocol.CallHierarchyItem) ([]protocol.Location, error) {
		calls, err := mark.server().IncomingCalls(mark.ctx(), &protocol.CallHierarchyIncomingCallsParams{Item: item})
		if err != nil {
			return nil, err
		}
		var locs []protocol.Location
		for _, call := range calls {
			locs = append(locs, itemLocation(call.From))
		}
		return locs, nil
	}
	callHierarchy(mark, src, getCalls, want)
}

func outgoingCallsMarker(mark marker, src protocol.Location, want ...protocol.Location) {
	getCalls := func(item protocol.CallHierarchyItem) ([]protocol.Location, error) {
		calls, err := mark.server().OutgoingCalls(mark.ctx(), &protocol.CallHierarchyOutgoingCallsParams{Item: item})
		if err != nil {
			return nil, err
		}
		var locs []protocol.Location
		for _, call := range calls {
			locs = append(locs, itemLocation(call.To))
		}
		return locs, nil
	}
	callHierarchy(mark, src, getCalls, want)
}

type callHierarchyFunc = func(protocol.CallHierarchyItem) ([]protocol.Location, error)

func callHierarchy(mark marker, src protocol.Location, getCalls callHierarchyFunc, want []protocol.Location) {
	items, err := mark.server().PrepareCallHierarchy(mark.ctx(), &protocol.CallHierarchyPrepareParams{
		TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(src),
	})
	if err != nil {
		mark.errorf("PrepareCallHierarchy failed: %v", err)
		return
	}
	if nitems := len(items); nitems != 1 {
		mark.errorf("PrepareCallHierarchy returned %d items, want exactly 1", nitems)
		return
	}
	if loc := itemLocation(items[0]); loc != src {
		mark.errorf("PrepareCallHierarchy found call %v, want %v", loc, src)
		return
	}
	calls, err := getCalls(items[0])
	if err != nil {
		mark.errorf("call hierarchy failed: %v", err)
		return
	}
	if calls == nil {
		calls = []protocol.Location{}
	}
	// TODO(rfindley): why aren't call hierarchy results stable?
	sortLocs := func(locs []protocol.Location) {
		sort.Slice(locs, func(i, j int) bool {
			return protocol.CompareLocation(locs[i], locs[j]) < 0
		})
	}
	sortLocs(want)
	sortLocs(calls)
	if d := cmp.Diff(want, calls); d != "" {
		mark.errorf("call hierarchy: unexpected results (-want +got):\n%s", d)
	}
}

func inlayhintsMarker(mark marker, g *Golden) {
	hints := mark.run.env.InlayHints(mark.path())

	// Map inlay hints to text edits.
	edits := make([]protocol.TextEdit, len(hints))
	for i, hint := range hints {
		var paddingLeft, paddingRight string
		if hint.PaddingLeft {
			paddingLeft = " "
		}
		if hint.PaddingRight {
			paddingRight = " "
		}
		edits[i] = protocol.TextEdit{
			Range:   protocol.Range{Start: hint.Position, End: hint.Position},
			NewText: fmt.Sprintf("<%s%s%s>", paddingLeft, hint.Label[0].Value, paddingRight),
		}
	}

	m := mark.mapper()
	got, _, err := protocol.ApplyEdits(m, edits)
	if err != nil {
		mark.errorf("ApplyProtocolEdits: %v", err)
		return
	}

	compareGolden(mark, got, g)
}

func prepareRenameMarker(mark marker, src protocol.Location, placeholder string) {
	params := &protocol.PrepareRenameParams{
		TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(src),
	}
	got, err := mark.server().PrepareRename(mark.ctx(), params)
	if err != nil {
		mark.T().Fatal(err)
	}
	if placeholder == "" {
		if got != nil {
			mark.errorf("PrepareRename(...) = %v, want nil", got)
		}
		return
	}

	want := &protocol.PrepareRenameResult{
		Placeholder: placeholder,
	}
	if span := namedArg(mark, "span", protocol.Location{}); span != (protocol.Location{}) {
		want.Range = span.Range
	} else {
		got.Range = protocol.Range{} // ignore Range
	}
	if diff := cmp.Diff(want, got); diff != "" {
		mark.errorf("mismatching PrepareRename result:\n%s", diff)
	}
}

// symbolMarker implements the @symbol marker.
func symbolMarker(mark marker, golden *Golden) {
	// Retrieve information about all symbols in this file.
	symbols, err := mark.server().DocumentSymbol(mark.ctx(), &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: mark.uri()},
	})
	if err != nil {
		mark.errorf("DocumentSymbol request failed: %v", err)
		return
	}

	// Format symbols one per line, sorted (in effect) by first column, a dotted name.
	var lines []string
	for _, symbol := range symbols {
		// Each result element is a union of (legacy)
		// SymbolInformation and (new) DocumentSymbol,
		// so we ascertain which one and then transcode.
		data, err := json.Marshal(symbol)
		if err != nil {
			mark.T().Fatal(err)
		}
		if _, ok := symbol.(map[string]any)["location"]; ok {
			// This case is not reached because Editor initialization
			// enables HierarchicalDocumentSymbolSupport.
			// TODO(adonovan): test this too.
			var sym protocol.SymbolInformation
			if err := json.Unmarshal(data, &sym); err != nil {
				mark.T().Fatal(err)
			}
			mark.errorf("fake Editor doesn't support SymbolInformation")

		} else {
			var sym protocol.DocumentSymbol // new hierarchical hotness
			if err := json.Unmarshal(data, &sym); err != nil {
				mark.T().Fatal(err)
			}

			// Print each symbol in the response tree.
			var visit func(sym protocol.DocumentSymbol, prefix []string)
			visit = func(sym protocol.DocumentSymbol, prefix []string) {
				var out strings.Builder
				out.WriteString(strings.Join(prefix, "."))
				fmt.Fprintf(&out, " %q", sym.Detail)
				if delta := sym.Range.End.Line - sym.Range.Start.Line; delta > 0 {
					fmt.Fprintf(&out, " +%d lines", delta)
				}
				lines = append(lines, out.String())

				for _, child := range sym.Children {
					visit(child, append(prefix, child.Name))
				}
			}
			visit(sym, []string{sym.Name})
		}
	}
	sort.Strings(lines)
	lines = append(lines, "") // match trailing newline in .txtar file
	got := []byte(strings.Join(lines, "\n"))

	// Compare with golden.
	want, ok := golden.Get(mark.T(), "", got)
	if !ok {
		mark.errorf("%s: missing golden file @%s", mark.note.Name, golden.id)
	} else if diff := cmp.Diff(string(got), string(want)); diff != "" {
		mark.errorf("%s: unexpected output: got:\n%s\nwant:\n%s\ndiff:\n%s",
			mark.note.Name, got, want, diff)
	}
}

// compareLocations returns an error message if got and want are not
// the same set of locations. The marker is used only for fmtLoc.
func compareLocations(mark marker, got, want []protocol.Location) error {
	toStrings := func(locs []protocol.Location) []string {
		strs := make([]string, len(locs))
		for i, loc := range locs {
			strs[i] = mark.run.fmtLoc(loc)
		}
		sort.Strings(strs)
		return strs
	}
	if diff := cmp.Diff(toStrings(want), toStrings(got)); diff != "" {
		return fmt.Errorf("incorrect result locations: (got %d, want %d):\n%s",
			len(got), len(want), diff)
	}
	return nil
}

func workspaceSymbolMarker(mark marker, query string, golden *Golden) {
	params := &protocol.WorkspaceSymbolParams{
		Query: query,
	}

	gotSymbols, err := mark.server().Symbol(mark.ctx(), params)
	if err != nil {
		mark.errorf("Symbol(%q) failed: %v", query, err)
		return
	}
	var got bytes.Buffer
	for _, s := range gotSymbols {
		// Omit the txtar position of the symbol location; otherwise edits to the
		// txtar archive lead to unexpected failures.
		loc := mark.run.fmtLocForGolden(s.Location)
		if loc == "" {
			loc = "<unknown>"
		}
		fmt.Fprintf(&got, "%s %s %s\n", loc, s.Name, s.Kind)
	}

	compareGolden(mark, got.Bytes(), golden)
}

// compareGolden compares the content of got with that of g.Get(""), reporting
// errors on any mismatch.
//
// TODO(rfindley): use this helper in more places.
func compareGolden(mark marker, got []byte, g *Golden) {
	want, ok := g.Get(mark.T(), "", got)
	if !ok {
		mark.errorf("missing golden file @%s", g.id)
		return
	}
	// Normalize newline termination: archive files (i.e. Golden content) can't
	// contain non-newline terminated files, except in the special case where the
	// file is completely empty.
	//
	// Note that txtar partitions a contiguous byte slice, so we must copy before
	// appending.
	normalize := func(s []byte) []byte {
		if n := len(s); n > 0 && s[n-1] != '\n' {
			s = append(s[:n:n], '\n') // don't mutate array
		}
		return s
	}
	got = normalize(got)
	want = normalize(want)
	if diff := compare.Bytes(want, got); diff != "" {
		mark.errorf("%s does not match @%s:\n%s", mark.note.Name, g.id, diff)
	}
}
