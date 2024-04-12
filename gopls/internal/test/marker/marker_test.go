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
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"golang.org/x/tools/go/expect"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/debug"
	"golang.org/x/tools/gopls/internal/lsprpc"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/test/compare"
	"golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/gopls/internal/util/slices"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/diff/myers"
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
				t.Skipf("skipping on %s due to -skip_goos", runtime.GOOS)
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
			if test.maxGoVersion != "" {
				var go1point int
				if _, err := fmt.Sscanf(test.maxGoVersion, "go1.%d", &go1point); err != nil {
					t.Fatalf("parsing -max_go version: %v", err)
				}
				testenv.SkipAfterGo1Point(t, go1point)
			}
			if test.cgo {
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

			// Wait for the didOpen notifications to be processed, then collect
			// diagnostics.
			var diags map[string]*protocol.PublishDiagnosticsParams
			run.env.AfterChange(integration.ReadAllDiagnostics(&diags))
			for path, params := range diags {
				uri := run.env.Sandbox.Workdir.URI(path)
				for _, diag := range params.Diagnostics {
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
						t.Errorf("%s: unexpected diagnostic: %q", run.fmtLoc(loc), diag.Message)
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

// errorf reports an error with a prefix indicating the position of the marker note.
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
			mark.errorf("converting args: %v", err)
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
// any values.
//
// The provided fn should not mutate the test environment.
func actionMarkerFunc(fn any) func(marker) {
	ftype := reflect.TypeOf(fn)
	if ftype.NumIn() == 0 || ftype.In(0) != markerType {
		panic(fmt.Sprintf("action marker function %#v must accept marker as its first argument", ftype))
	}
	if ftype.NumOut() != 0 {
		panic(fmt.Sprintf("action marker function %#v cannot have results", ftype))
	}

	return func(mark marker) {
		args := append([]any{mark}, mark.note.Args...)
		argValues, err := convertArgs(mark, ftype, args)
		if err != nil {
			mark.errorf("converting args: %v", err)
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

// is reports whether arg is a T.
func is[T any](arg any) bool {
	_, ok := arg.(T)
	return ok
}

// Supported value marker functions. See [valueMarkerFunc] for more details.
var valueMarkerFuncs = map[string]func(marker){
	"loc":  valueMarkerFunc(locMarker),
	"item": valueMarkerFunc(completionItemMarker),
}

// Supported action marker functions. See [actionMarkerFunc] for more details.
var actionMarkerFuncs = map[string]func(marker){
	"acceptcompletion": actionMarkerFunc(acceptCompletionMarker),
	"codeaction":       actionMarkerFunc(codeActionMarker),
	"codeactionedit":   actionMarkerFunc(codeActionEditMarker),
	"codeactionerr":    actionMarkerFunc(codeActionErrMarker),
	"codelenses":       actionMarkerFunc(codeLensesMarker),
	"complete":         actionMarkerFunc(completeMarker),
	"def":              actionMarkerFunc(defMarker),
	"diag":             actionMarkerFunc(diagMarker),
	"documentlink":     actionMarkerFunc(documentLinkMarker),
	"foldingrange":     actionMarkerFunc(foldingRangeMarker),
	"format":           actionMarkerFunc(formatMarker),
	"highlight":        actionMarkerFunc(highlightMarker),
	"hover":            actionMarkerFunc(hoverMarker),
	"hovererr":         actionMarkerFunc(hoverErrMarker),
	"implementation":   actionMarkerFunc(implementationMarker),
	"incomingcalls":    actionMarkerFunc(incomingCallsMarker),
	"inlayhints":       actionMarkerFunc(inlayhintsMarker),
	"outgoingcalls":    actionMarkerFunc(outgoingCallsMarker),
	"preparerename":    actionMarkerFunc(prepareRenameMarker),
	"rank":             actionMarkerFunc(rankMarker),
	"rankl":            actionMarkerFunc(ranklMarker),
	"refs":             actionMarkerFunc(refsMarker),
	"rename":           actionMarkerFunc(renameMarker),
	"renameerr":        actionMarkerFunc(renameErrMarker),
	"selectionrange":   actionMarkerFunc(selectionRangeMarker),
	"signature":        actionMarkerFunc(signatureMarker),
	"snippet":          actionMarkerFunc(snippetMarker),
	"suggestedfix":     actionMarkerFunc(suggestedfixMarker),
	"suggestedfixerr":  actionMarkerFunc(suggestedfixErrMarker),
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
	minGoVersion     string
	maxGoVersion     string
	cgo              bool
	writeGoSum       []string
	skipGOOS         []string
	skipGOARCH       []string
	ignoreExtraDiags bool
	filterBuiltins   bool
	filterKeywords   bool
	errorsOK         bool
}

// flagSet returns the flagset used for parsing the special "flags" file in the
// test archive.
func (t *markerTest) flagSet() *flag.FlagSet {
	flags := flag.NewFlagSet(t.name, flag.ContinueOnError)
	flags.StringVar(&t.minGoVersion, "min_go", "", "if set, the minimum go1.X version required for this test")
	flags.StringVar(&t.maxGoVersion, "max_go", "", "if set, the maximum go1.X version required for this test")
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

func (t *markerTest) getGolden(id expect.Identifier) *Golden {
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
	return golden
}

// Golden holds extracted golden content for a single @<name> prefix.
//
// When -update is set, golden captures the updated golden contents for later
// writing.
type Golden struct {
	id      expect.Identifier
	data    map[string][]byte // key "" => @id itself
	updated map[string][]byte
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
	for id, g := range test.golden {
		for name, data := range g.updated {
			filename := "@" + path.Join(string(id), name) // name may be ""
			updatedGolden[filename] = data
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

	// ...followed by any new golden files.
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
	arch.Files = append(arch.Files, newGoldenFiles...)

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
		if err := sandbox.RunGoCommand(context.Background(), dir, "list", []string{"-mod=mod", "..."}, []string{"GOWORK=off"}, true); err != nil {
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
	const skipApplyEdits = true // capture edits but don't apply them
	editor, err := fake.NewEditor(sandbox, config).Connect(ctx, server, awaiter.Hooks(), skipApplyEdits)
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
	// Each @diag/@suggestedfix marker eliminates an entry from diags.
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

// fmtLoc formats the given pos in the context of the test, using
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
	formatted := run.fmtLocDetails(loc, true)
	if formatted == "" {
		run.env.T.Errorf("unable to find %s in test archive", loc)
		return "<invalid location>"
	}
	return formatted
}

// See fmtLoc. If includeTxtPos is not set, the position in the full archive
// file is omitted.
//
// If the location cannot be found within the archive, fmtLocDetails returns "".
func (run *markerTestRun) fmtLocDetails(loc protocol.Location, includeTxtPos bool) string {
	if loc == (protocol.Location{}) {
		return ""
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
		return ""
	}
	m, err := run.env.Editor.Mapper(name)
	if err != nil {
		run.env.T.Errorf("internal error: %v", err)
		return "<invalid location>"
	}
	start, end, err := m.RangeOffsets(loc.Range)
	if err != nil {
		run.env.T.Errorf("error formatting location %s: %v", loc, err)
		return "<invalid location>"
	}
	var (
		startLine, startCol8 = m.OffsetLineCol8(start)
		endLine, endCol8     = m.OffsetLineCol8(end)
	)
	innerSpan := fmt.Sprintf("%d:%d", startLine, startCol8)       // relative to the embedded file
	outerSpan := fmt.Sprintf("%d:%d", lines+startLine, startCol8) // relative to the archive file
	if start != end {
		if endLine == startLine {
			innerSpan += fmt.Sprintf("-%d", endCol8)
			outerSpan += fmt.Sprintf("-%d", endCol8)
		} else {
			innerSpan += fmt.Sprintf("-%d:%d", endLine, endCol8)
			outerSpan += fmt.Sprintf("-%d:%d", lines+endLine, endCol8)
		}
	}

	if includeTxtPos {
		return fmt.Sprintf("%s:%s (%s:%s)", name, innerSpan, run.test.name, outerSpan)
	} else {
		return fmt.Sprintf("%s:%s", name, innerSpan)
	}
}

// ---- converters ----

// converter is the signature of argument converters.
// A converter should return an error rather than calling marker.errorf().
//
// type converter func(marker, any) (any, error)

// Types with special conversions.
var (
	goldenType        = reflect.TypeOf(&Golden{})
	locationType      = reflect.TypeOf(protocol.Location{})
	markerType        = reflect.TypeOf(marker{})
	stringMatcherType = reflect.TypeOf(stringMatcher{})
)

func convert(mark marker, arg any, paramType reflect.Type) (any, error) {
	// Handle stringMatcher and golden parameters before resolving identifiers,
	// because golden content lives in a separate namespace from other
	// identifiers.
	switch paramType {
	case stringMatcherType:
		return convertStringMatcher(mark, arg)
	case goldenType:
		id, ok := arg.(expect.Identifier)
		if !ok {
			return nil, fmt.Errorf("invalid input type %T: golden key must be an identifier", arg)
		}
		return mark.run.test.getGolden(id), nil
	}
	if id, ok := arg.(expect.Identifier); ok {
		if arg, ok := mark.run.values[id]; ok {
			if !reflect.TypeOf(arg).AssignableTo(paramType) {
				return nil, fmt.Errorf("cannot convert %v (%T) to %s", arg, arg, paramType)
			}
			return arg, nil
		}
	}
	if paramType == locationType {
		return convertLocation(mark, arg)
	}
	if reflect.TypeOf(arg).AssignableTo(paramType) {
		return arg, nil // no conversion required
	}
	return nil, fmt.Errorf("cannot convert %v (%T) to %s", arg, arg, paramType)
}

// convertLocation converts a string or regexp argument into the protocol
// location corresponding to the first position of the string (or first match
// of the regexp) in the line preceding the note.
func convertLocation(mark marker, arg any) (protocol.Location, error) {
	switch arg := arg.(type) {
	case string:
		startOff, preceding, m, err := linePreceding(mark.run, mark.note.Pos)
		if err != nil {
			return protocol.Location{}, err
		}
		idx := bytes.Index(preceding, []byte(arg))
		if idx < 0 {
			return protocol.Location{}, fmt.Errorf("substring %q not found in %q", arg, preceding)
		}
		off := startOff + idx
		return m.OffsetLocation(off, off+len(arg))
	case *regexp.Regexp:
		return findRegexpInLine(mark.run, mark.note.Pos, arg)
	default:
		return protocol.Location{}, fmt.Errorf("cannot convert argument type %T to location (must be a string to match the preceding line)", arg)
	}
}

// findRegexpInLine searches the partial line preceding pos for a match for the
// regular expression re, returning a location spanning the first match. If re
// contains exactly one subgroup, the position of this subgroup match is
// returned rather than the position of the full match.
func findRegexpInLine(run *markerTestRun, pos token.Pos, re *regexp.Regexp) (protocol.Location, error) {
	startOff, preceding, m, err := linePreceding(run, pos)
	if err != nil {
		return protocol.Location{}, err
	}

	matches := re.FindSubmatchIndex(preceding)
	if len(matches) == 0 {
		return protocol.Location{}, fmt.Errorf("no match for regexp %q found in %q", re, string(preceding))
	}
	var start, end int
	switch len(matches) {
	case 2:
		// no subgroups: return the range of the regexp expression
		start, end = matches[0], matches[1]
	case 4:
		// one subgroup: return its range
		start, end = matches[2], matches[3]
	default:
		return protocol.Location{}, fmt.Errorf("invalid location regexp %q: expect either 0 or 1 subgroups, got %d", re, len(matches)/2-1)
	}

	return m.OffsetLocation(start+startOff, end+startOff)
}

func linePreceding(run *markerTestRun, pos token.Pos) (int, []byte, *protocol.Mapper, error) {
	file := run.test.fset.File(pos)
	posn := safetoken.Position(file, pos)
	lineStart := file.LineStart(posn.Line)
	startOff, endOff, err := safetoken.Offsets(file, lineStart, pos)
	if err != nil {
		return 0, nil, nil, err
	}
	m, err := run.env.Editor.Mapper(file.Name())
	if err != nil {
		return 0, nil, nil, err
	}
	return startOff, m.Content[startOff:endOff], m, nil
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
		golden := mark.run.test.getGolden(arg)
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

func (sc stringMatcher) String() string {
	if sc.golden != nil {
		return fmt.Sprintf("content from @%s entry", sc.golden.id)
	} else if sc.pattern != nil {
		return fmt.Sprintf("content matching %#q", sc.pattern)
	} else {
		return fmt.Sprintf("content with substring %q", sc.substr)
	}
}

// checkErr asserts that the given error matches the stringMatcher's expectations.
func (sc stringMatcher) checkErr(mark marker, err error) {
	if err == nil {
		mark.errorf("@%s succeeded unexpectedly, want %v", mark.note.Name, sc)
		return
	}
	sc.check(mark, err.Error())
}

// check asserts that the given content matches the stringMatcher's expectations.
func (sc stringMatcher) check(mark marker, got string) {
	if sc.golden != nil {
		compareGolden(mark, []byte(got), sc.golden)
	} else if sc.pattern != nil {
		// Content must match the regular expression pattern.
		if !sc.pattern.MatchString(got) {
			mark.errorf("got %q, does not match pattern %#q", got, sc.pattern)
		}

	} else if !strings.Contains(got, sc.substr) {
		// Content must contain the expected substring.
		mark.errorf("got %q, want substring %q", got, sc.substr)
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

func rankMarker(mark marker, src protocol.Location, items ...completionItem) {
	// Separate positive and negative items (expectations).
	var pos, neg []completionItem
	for _, item := range items {
		if strings.HasPrefix(item.Label, "!") {
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
			if g.Label == w.Label {
				got = append(got, g.Label)
				break
			}
		}
		for _, w := range neg {
			if g.Label == w.Label[len("!"):] {
				mark.errorf("got unwanted completion: %s", g.Label)
				break
			}
		}
	}
	var want []string
	for _, w := range pos {
		want = append(want, w.Label)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		mark.errorf("completion rankings do not match (-want +got):\n%s", diff)
	}
}

func ranklMarker(mark marker, src protocol.Location, labels ...string) {
	// Separate positive and negative labels (expectations).
	var pos, neg []string
	for _, label := range labels {
		if strings.HasPrefix(label, "!") {
			neg = append(neg, label[len("!"):])
		} else {
			pos = append(pos, label)
		}
	}

	// Collect results that are present in items, preserving their order.
	list := mark.run.env.Completion(src)
	var got []string
	for _, g := range list.Items {
		if slices.Contains(pos, g.Label) {
			got = append(got, g.Label)
		} else if slices.Contains(neg, g.Label) {
			mark.errorf("got unwanted completion: %s", g.Label)
		}
	}
	if diff := cmp.Diff(pos, got); diff != "" {
		mark.errorf("completion rankings do not match (-want +got):\n%s", diff)
	}
}

func snippetMarker(mark marker, src protocol.Location, item completionItem, want string) {
	list := mark.run.env.Completion(src)
	var (
		found bool
		got   string
		all   []string // for errors
	)
	items := filterBuiltinsAndKeywords(mark, list.Items)
	for _, i := range items {
		all = append(all, i.Label)
		if i.Label == item.Label {
			found = true
			if i.TextEdit != nil {
				got = i.TextEdit.NewText
			}
			break
		}
	}
	if !found {
		mark.errorf("no completion item found matching %s (got: %v)", item.Label, all)
		return
	}
	if got != want {
		mark.errorf("snippets do not match: got %q, want %q", got, want)
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
	filename := mark.path()
	mapper := mark.mapper()
	patched, _, err := protocol.ApplyEdits(mapper, append([]protocol.TextEdit{*selected.TextEdit}, selected.AdditionalTextEdits...))

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

func highlightMarker(mark marker, src protocol.Location, dsts ...protocol.Location) {
	highlights := mark.run.env.DocumentHighlight(src)
	var got []protocol.Range
	for _, h := range highlights {
		got = append(got, h.Range)
	}

	var want []protocol.Range
	for _, d := range dsts {
		want = append(want, d.Range)
	}

	sortRanges := func(s []protocol.Range) {
		sort.Slice(s, func(i, j int) bool {
			return protocol.CompareRange(s[i], s[j]) < 0
		})
	}

	sortRanges(got)
	sortRanges(want)

	if diff := cmp.Diff(want, got); diff != "" {
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

// locMarker implements the @loc marker. It is executed before other
// markers, so that locations are available.
func locMarker(mark marker, loc protocol.Location) protocol.Location { return loc }

// diagMarker implements the @diag marker. It eliminates diagnostics from
// the observed set in mark.test.
func diagMarker(mark marker, loc protocol.Location, re *regexp.Regexp) {
	if _, ok := removeDiagnostic(mark, loc, re); !ok {
		mark.errorf("no diagnostic at %v matches %q", loc, re)
	}
}

// removeDiagnostic looks for a diagnostic matching loc at the given position.
//
// If found, it returns (diag, true), and eliminates the matched diagnostic
// from the unmatched set.
//
// If not found, it returns (protocol.Diagnostic{}, false).
func removeDiagnostic(mark marker, loc protocol.Location, re *regexp.Regexp) (protocol.Diagnostic, bool) {
	loc.Range.End = loc.Range.Start // diagnostics ignore end position.
	diags := mark.run.diags[loc]
	for i, diag := range diags {
		if re.MatchString(diag.Message) {
			mark.run.diags[loc] = append(diags[:i], diags[i+1:]...)
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
	if label == "" {
		// A null result is expected.
		// (There's no point having a @signatureerr marker
		// because the server handler suppresses all errors.)
		if got != nil && len(got.Signatures) > 0 {
			mark.errorf("signatureHelp = %v, want 0 signatures", got)
		}
		return
	}
	if got == nil || len(got.Signatures) != 1 {
		mark.errorf("signatureHelp = %v, want exactly 1 signature", got)
		return
	}
	if got := got.Signatures[0].Label; got != label {
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

	editMap, err := env.Editor.Server.Rename(env.Ctx, &protocol.RenameParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: loc.URI},
		Position:     loc.Range.Start,
		NewName:      newName,
	})
	if err != nil {
		return nil, err
	}

	fileChanges := make(map[string][]byte)
	if err := applyDocumentChanges(env, editMap.DocumentChanges, fileChanges); err != nil {
		return nil, fmt.Errorf("applying document changes: %v", err)
	}
	return fileChanges, nil
}

// applyDocumentChanges applies the given document changes to the editor buffer
// content, recording the resulting contents in the fileChanges map. It is an
// error for a change to an edit a file that is already present in the
// fileChanges map.
func applyDocumentChanges(env *integration.Env, changes []protocol.DocumentChanges, fileChanges map[string][]byte) error {
	getMapper := func(path string) (*protocol.Mapper, error) {
		if _, ok := fileChanges[path]; ok {
			return nil, fmt.Errorf("internal error: %s is already edited", path)
		}
		return env.Editor.Mapper(path)
	}

	for _, change := range changes {
		if change.RenameFile != nil {
			// rename
			oldFile := env.Sandbox.Workdir.URIToPath(change.RenameFile.OldURI)
			mapper, err := getMapper(oldFile)
			if err != nil {
				return err
			}
			newFile := env.Sandbox.Workdir.URIToPath(change.RenameFile.NewURI)
			fileChanges[newFile] = mapper.Content
		} else {
			// edit
			filename := env.Sandbox.Workdir.URIToPath(change.TextDocumentEdit.TextDocument.URI)
			mapper, err := getMapper(filename)
			if err != nil {
				return err
			}
			patched, _, err := protocol.ApplyEdits(mapper, protocol.AsTextEdits(change.TextDocumentEdit.Edits))
			if err != nil {
				return err
			}
			fileChanges[filename] = patched
		}
	}

	return nil
}

func codeActionMarker(mark marker, start, end protocol.Location, actionKind string, g *Golden, titles ...string) {
	// Request the range from start.Start to end.End.
	loc := start
	loc.Range.End = end.Range.End

	// Apply the fix it suggests.
	changed, err := codeAction(mark.run.env, loc.URI, loc.Range, actionKind, nil, titles)
	if err != nil {
		mark.errorf("codeAction failed: %v", err)
		return
	}

	// Check the file state.
	checkChangedFiles(mark, changed, g)
}

func codeActionEditMarker(mark marker, loc protocol.Location, actionKind string, g *Golden, titles ...string) {
	changed, err := codeAction(mark.run.env, loc.URI, loc.Range, actionKind, nil, titles)
	if err != nil {
		mark.errorf("codeAction failed: %v", err)
		return
	}

	checkDiffs(mark, changed, g)
}

func codeActionErrMarker(mark marker, start, end protocol.Location, actionKind string, wantErr stringMatcher) {
	loc := start
	loc.Range.End = end.Range.End
	_, err := codeAction(mark.run.env, loc.URI, loc.Range, actionKind, nil, nil)
	wantErr.checkErr(mark, err)
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
		fmt.Fprintln(&b, mark.run.fmtLocDetails(loc, false), *l.Target)
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

// suggestedfixMarker implements the @suggestedfix(location, regexp,
// kind, golden) marker. It acts like @diag(location, regexp), to set
// the expectation of a diagnostic, but then it applies the first code
// action of the specified kind suggested by the matched diagnostic.
func suggestedfixMarker(mark marker, loc protocol.Location, re *regexp.Regexp, golden *Golden) {
	loc.Range.End = loc.Range.Start // diagnostics ignore end position.
	// Find and remove the matching diagnostic.
	diag, ok := removeDiagnostic(mark, loc, re)
	if !ok {
		mark.errorf("no diagnostic at %v matches %q", loc, re)
		return
	}

	// Apply the fix it suggests.
	changed, err := codeAction(mark.run.env, loc.URI, diag.Range, "quickfix", &diag, nil)
	if err != nil {
		mark.errorf("suggestedfix failed: %v. (Use @suggestedfixerr for expected errors.)", err)
		return
	}

	// Check the file state.
	checkDiffs(mark, changed, golden)
}

func suggestedfixErrMarker(mark marker, loc protocol.Location, re *regexp.Regexp, wantErr stringMatcher) {
	loc.Range.End = loc.Range.Start // diagnostics ignore end position.
	// Find and remove the matching diagnostic.
	diag, ok := removeDiagnostic(mark, loc, re)
	if !ok {
		mark.errorf("no diagnostic at %v matches %q", loc, re)
		return
	}

	// Apply the fix it suggests.
	_, err := codeAction(mark.run.env, loc.URI, diag.Range, "quickfix", &diag, nil)
	wantErr.checkErr(mark, err)
}

// codeAction executes a textDocument/codeAction request for the specified
// location and kind. If diag is non-nil, it is used as the code action
// context.
//
// The resulting map contains resulting file contents after the code action is
// applied. Currently, this function does not support code actions that return
// edits directly; it only supports code action commands.
func codeAction(env *integration.Env, uri protocol.DocumentURI, rng protocol.Range, actionKind string, diag *protocol.Diagnostic, titles []string) (map[string][]byte, error) {
	changes, err := codeActionChanges(env, uri, rng, actionKind, diag, titles)
	if err != nil {
		return nil, err
	}
	fileChanges := make(map[string][]byte)
	if err := applyDocumentChanges(env, changes, fileChanges); err != nil {
		return nil, fmt.Errorf("applying document changes: %v", err)
	}
	return fileChanges, nil
}

// codeActionChanges executes a textDocument/codeAction request for the
// specified location and kind, and captures the resulting document changes.
// If diag is non-nil, it is used as the code action context.
// If titles is non-empty, the code action title must be present among the provided titles.
func codeActionChanges(env *integration.Env, uri protocol.DocumentURI, rng protocol.Range, actionKind string, diag *protocol.Diagnostic, titles []string) ([]protocol.DocumentChanges, error) {
	// Request all code actions that apply to the diagnostic.
	// (The protocol supports filtering using Context.Only={actionKind}
	// but we can give a better error if we don't filter.)
	params := &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Range:        rng,
		Context: protocol.CodeActionContext{
			Only: nil, // => all kinds
		},
	}
	if diag != nil {
		params.Context.Diagnostics = []protocol.Diagnostic{*diag}
	}

	actions, err := env.Editor.Server.CodeAction(env.Ctx, params)
	if err != nil {
		return nil, err
	}

	// Find the sole candidates CodeAction of the specified kind (e.g. refactor.rewrite).
	var candidates []protocol.CodeAction
	for _, act := range actions {
		if act.Kind == protocol.CodeActionKind(actionKind) {
			if len(titles) > 0 {
				for _, f := range titles {
					if act.Title == f {
						candidates = append(candidates, act)
						break
					}
				}
			} else {
				candidates = append(candidates, act)
			}
		}
	}
	if len(candidates) != 1 {
		for _, act := range actions {
			env.T.Logf("found CodeAction Kind=%s Title=%q", act.Kind, act.Title)
		}
		return nil, fmt.Errorf("found %d CodeActions of kind %s matching filters %v for this diagnostic, want 1", len(candidates), actionKind, titles)
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
		if action.Edit.Changes != nil {
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
		// ApplyFix dispatches to the "stub_methods" suggestedfix hook (the meat).
		// The server then makes an ApplyEdit RPC to the client,
		// whose Awaiter hook gathers the edits instead of applying them.

		_ = env.Awaiter.TakeDocumentChanges() // reset (assuming Env is confined to this thread)

		if _, err := env.Editor.Server.ExecuteCommand(env.Ctx, &protocol.ExecuteCommandParams{
			Command:   action.Command.Command,
			Arguments: action.Command.Arguments,
		}); err != nil {
			return nil, err
		}
		return env.Awaiter.TakeDocumentChanges(), nil
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

func prepareRenameMarker(mark marker, src, spn protocol.Location, placeholder string) {
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
	want := &protocol.PrepareRenameResult{Range: spn.Range, Placeholder: placeholder}
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
		loc := mark.run.fmtLocDetails(s.Location, false)
		// TODO(rfindley): can we do better here, by detecting if the location is
		// relative to GOROOT?
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
