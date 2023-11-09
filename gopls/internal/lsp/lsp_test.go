// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/lsp/cache"
	"golang.org/x/tools/gopls/internal/lsp/command"
	"golang.org/x/tools/gopls/internal/lsp/debug"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/lsp/tests"
	"golang.org/x/tools/gopls/internal/lsp/tests/compare"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/testenv"
)

func TestMain(m *testing.M) {
	bug.PanicOnBugs = true
	testenv.ExitIfSmallMachine()

	os.Exit(m.Run())
}

// TestLSP runs the marker tests in files beneath testdata/ using
// implementations of each of the marker operations that make LSP RPCs to a
// gopls server.
func TestLSP(t *testing.T) {
	tests.RunTests(t, "testdata", true, testLSP)
}

func testLSP(t *testing.T, datum *tests.Data) {
	ctx := tests.Context(t)

	// Setting a debug instance suppresses logging to stderr, but ensures that we
	// still e.g. convert events into runtime/trace/instrumentation.
	//
	// Previously, we called event.SetExporter(nil), which turns off all
	// instrumentation.
	ctx = debug.WithInstance(ctx, "", "off")

	session := cache.NewSession(ctx, cache.New(nil))
	options := source.DefaultOptions(tests.DefaultOptions)
	options.SetEnvSlice(datum.Config.Env)
	folder := &cache.Folder{
		Dir:     span.URIFromPath(datum.Config.Dir),
		Name:    datum.Config.Dir,
		Options: options,
	}
	view, snapshot, release, err := session.NewView(ctx, folder)
	if err != nil {
		t.Fatal(err)
	}

	defer session.RemoveView(view)

	// Only run the -modfile specific tests in module mode with Go 1.14 or above.
	datum.ModfileFlagAvailable = len(snapshot.ModFiles()) > 0 && testenv.Go1Point() >= 14
	release()

	// Open all files for performance reasons, because gopls only
	// keeps active packages (those with open files) in memory.
	//
	// In practice clients will only send document-oriented requests for open
	// files.
	var modifications []source.FileModification
	for _, module := range datum.Exported.Modules {
		for name := range module.Files {
			filename := datum.Exported.File(module.Name, name)
			if filepath.Ext(filename) != ".go" {
				continue
			}
			content, err := datum.Exported.FileContents(filename)
			if err != nil {
				t.Fatal(err)
			}
			modifications = append(modifications, source.FileModification{
				URI:        span.URIFromPath(filename),
				Action:     source.Open,
				Version:    -1,
				Text:       content,
				LanguageID: "go",
			})
		}
	}
	for filename, content := range datum.Config.Overlay {
		if filepath.Ext(filename) != ".go" {
			continue
		}
		modifications = append(modifications, source.FileModification{
			URI:        span.URIFromPath(filename),
			Action:     source.Open,
			Version:    -1,
			Text:       content,
			LanguageID: "go",
		})
	}
	if err := session.ModifyFiles(ctx, modifications); err != nil {
		t.Fatal(err)
	}
	r := &runner{
		data:     datum,
		ctx:      ctx,
		editRecv: make(chan map[span.URI][]byte, 1),
	}

	r.server = NewServer(session, testClient{runner: r}, options)
	tests.Run(t, r, datum)
}

// runner implements tests.Tests by making LSP RPCs to a gopls server.
type runner struct {
	server      *Server
	data        *tests.Data
	diagnostics map[span.URI][]*source.Diagnostic
	ctx         context.Context
	editRecv    chan map[span.URI][]byte
}

// testClient stubs any client functions that may be called by LSP functions.
type testClient struct {
	protocol.Client
	runner *runner
}

func (c testClient) Close() error {
	return nil
}

// Trivially implement PublishDiagnostics so that we can call
// server.publishReports below to de-dup sent diagnostics.
func (c testClient) PublishDiagnostics(context.Context, *protocol.PublishDiagnosticsParams) error {
	return nil
}

func (c testClient) ShowMessage(context.Context, *protocol.ShowMessageParams) error {
	return nil
}

func (c testClient) ApplyEdit(ctx context.Context, params *protocol.ApplyWorkspaceEditParams) (*protocol.ApplyWorkspaceEditResult, error) {
	res, err := applyTextDocumentEdits(c.runner, params.Edit.DocumentChanges)
	if err != nil {
		return nil, err
	}
	c.runner.editRecv <- res
	return &protocol.ApplyWorkspaceEditResult{Applied: true}, nil
}

func (r *runner) CallHierarchy(t *testing.T, spn span.Span, expectedCalls *tests.CallHierarchyResult) {
	mapper, err := r.data.Mapper(spn.URI())
	if err != nil {
		t.Fatal(err)
	}
	loc, err := mapper.SpanLocation(spn)
	if err != nil {
		t.Fatalf("failed for %v: %v", spn, err)
	}

	params := &protocol.CallHierarchyPrepareParams{
		TextDocumentPositionParams: protocol.LocationTextDocumentPositionParams(loc),
	}

	items, err := r.server.PrepareCallHierarchy(r.ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatalf("expected call hierarchy item to be returned for identifier at %v\n", loc.Range)
	}

	callLocation := protocol.Location{
		URI:   items[0].URI,
		Range: items[0].Range,
	}
	if callLocation != loc {
		t.Fatalf("expected server.PrepareCallHierarchy to return identifier at %v but got %v\n", loc, callLocation)
	}

	incomingCalls, err := r.server.IncomingCalls(r.ctx, &protocol.CallHierarchyIncomingCallsParams{Item: items[0]})
	if err != nil {
		t.Error(err)
	}
	var incomingCallItems []protocol.CallHierarchyItem
	for _, item := range incomingCalls {
		incomingCallItems = append(incomingCallItems, item.From)
	}
	msg := tests.DiffCallHierarchyItems(incomingCallItems, expectedCalls.IncomingCalls)
	if msg != "" {
		t.Errorf("incoming calls: %s", msg)
	}

	outgoingCalls, err := r.server.OutgoingCalls(r.ctx, &protocol.CallHierarchyOutgoingCallsParams{Item: items[0]})
	if err != nil {
		t.Error(err)
	}
	var outgoingCallItems []protocol.CallHierarchyItem
	for _, item := range outgoingCalls {
		outgoingCallItems = append(outgoingCallItems, item.To)
	}
	msg = tests.DiffCallHierarchyItems(outgoingCallItems, expectedCalls.OutgoingCalls)
	if msg != "" {
		t.Errorf("outgoing calls: %s", msg)
	}
}

func (r *runner) SemanticTokens(t *testing.T, spn span.Span) {
	uri := spn.URI()
	filename := uri.Filename()
	// this is called solely for coverage in semantic.go
	_, err := r.server.semanticTokensFull(r.ctx, &protocol.SemanticTokensParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: protocol.URIFromSpanURI(uri),
		},
	})
	if err != nil {
		t.Errorf("%v for %s", err, filename)
	}
	_, err = r.server.semanticTokensRange(r.ctx, &protocol.SemanticTokensRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: protocol.URIFromSpanURI(uri),
		},
		// any legal range. Just to exercise the call.
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      0,
				Character: 0,
			},
			End: protocol.Position{
				Line:      2,
				Character: 0,
			},
		},
	})
	if err != nil {
		t.Errorf("%v for Range %s", err, filename)
	}
}

func applyTextDocumentEdits(r *runner, edits []protocol.DocumentChanges) (map[span.URI][]byte, error) {
	res := make(map[span.URI][]byte)
	for _, docEdits := range edits {
		if docEdits.TextDocumentEdit != nil {
			uri := docEdits.TextDocumentEdit.TextDocument.URI.SpanURI()
			var m *protocol.Mapper
			// If we have already edited this file, we use the edited version (rather than the
			// file in its original state) so that we preserve our initial changes.
			if content, ok := res[uri]; ok {
				m = protocol.NewMapper(uri, content)
			} else {
				var err error
				if m, err = r.data.Mapper(uri); err != nil {
					return nil, err
				}
			}
			patched, _, err := source.ApplyProtocolEdits(m, docEdits.TextDocumentEdit.Edits)
			if err != nil {
				return nil, err
			}
			res[uri] = patched
		}
	}
	return res, nil
}

func (r *runner) AddImport(t *testing.T, uri span.URI, expectedImport string) {
	cmd, err := command.NewListKnownPackagesCommand("List Known Packages", command.URIArg{
		URI: protocol.URIFromSpanURI(uri),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.server.executeCommand(r.ctx, &protocol.ExecuteCommandParams{
		Command:   cmd.Command,
		Arguments: cmd.Arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := resp.(command.ListKnownPackagesResult)
	var hasPkg bool
	for _, p := range res.Packages {
		if p == expectedImport {
			hasPkg = true
			break
		}
	}
	if !hasPkg {
		t.Fatalf("%s: got %v packages\nwant contains %q", command.ListKnownPackages, res.Packages, expectedImport)
	}
	cmd, err = command.NewAddImportCommand("Add Imports", command.AddImportArgs{
		URI:        protocol.URIFromSpanURI(uri),
		ImportPath: expectedImport,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.server.executeCommand(r.ctx, &protocol.ExecuteCommandParams{
		Command:   cmd.Command,
		Arguments: cmd.Arguments,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := (<-r.editRecv)[uri]
	want := r.data.Golden(t, "addimport", uri.Filename(), func() ([]byte, error) {
		return []byte(got), nil
	})
	if want == nil {
		t.Fatalf("golden file %q not found", uri.Filename())
	}
	if diff := compare.Bytes(want, got); diff != "" {
		t.Errorf("%s mismatch\n%s", command.AddImport, diff)
	}
}

func (r *runner) SelectionRanges(t *testing.T, spn span.Span) {
	uri := spn.URI()
	sm, err := r.data.Mapper(uri)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := sm.SpanLocation(spn)
	if err != nil {
		t.Error(err)
	}

	ranges, err := r.server.selectionRange(r.ctx, &protocol.SelectionRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{
			URI: protocol.URIFromSpanURI(uri),
		},
		Positions: []protocol.Position{loc.Range.Start},
	})
	if err != nil {
		t.Fatal(err)
	}

	sb := &strings.Builder{}
	for i, path := range ranges {
		fmt.Fprintf(sb, "Ranges %d: ", i)
		rng := path
		for {
			s, e, err := sm.RangeOffsets(rng.Range)
			if err != nil {
				t.Error(err)
			}

			var snippet string
			if e-s < 30 {
				snippet = string(sm.Content[s:e])
			} else {
				snippet = string(sm.Content[s:s+15]) + "..." + string(sm.Content[e-15:e])
			}

			fmt.Fprintf(sb, "\n\t%v %q", rng.Range, strings.ReplaceAll(snippet, "\n", "\\n"))

			if rng.Parent == nil {
				break
			}
			rng = *rng.Parent
		}
		sb.WriteRune('\n')
	}
	got := sb.String()

	testName := "selectionrange_" + tests.SpanName(spn)
	want := r.data.Golden(t, testName, uri.Filename(), func() ([]byte, error) {
		return []byte(got), nil
	})
	if want == nil {
		t.Fatalf("golden file %q not found", uri.Filename())
	}
	if diff := compare.Text(got, string(want)); diff != "" {
		t.Errorf("%s mismatch\n%s", testName, diff)
	}
}
