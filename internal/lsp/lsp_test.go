// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages/packagestest"
	"golang.org/x/tools/internal/lsp/cache"
	"golang.org/x/tools/internal/lsp/diff"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/lsp/xlog"
	"golang.org/x/tools/internal/span"
)

func TestLSP(t *testing.T) {
	packagestest.TestAll(t, testLSP)
}

func testLSP(t *testing.T, exporter packagestest.Exporter) {
	ctx := context.Background()
	const dir = "testdata"

	// We hardcode the expected number of test cases to ensure that all tests
	// are being executed. If a test is added, this number must be changed.
	const expectedCompletionsCount = 65
	const expectedDiagnosticsCount = 16
	const expectedFormatCount = 4
	const expectedDefinitionsCount = 17
	const expectedTypeDefinitionsCount = 2
	const expectedHighlightsCount = 2
	const expectedSymbolsCount = 1
	const expectedSignaturesCount = 19

	files := packagestest.MustCopyFileTree(dir)
	overlays := map[string][]byte{}
	for fragment, operation := range files {
		if trimmed := strings.TrimSuffix(fragment, ".in"); trimmed != fragment {
			delete(files, fragment)
			files[trimmed] = operation
		}
		const overlay = ".overlay"
		if index := strings.Index(fragment, overlay); index >= 0 {
			delete(files, fragment)
			partial := fragment[:index] + fragment[index+len(overlay):]
			contents, err := ioutil.ReadFile(filepath.Join(dir, fragment))
			if err != nil {
				t.Fatal(err)
			}
			overlays[partial] = contents
		}
	}
	modules := []packagestest.Module{
		{
			Name:    "golang.org/x/tools/internal/lsp",
			Files:   files,
			Overlay: overlays,
		},
	}
	exported := packagestest.Export(t, exporter, modules)
	defer exported.Cleanup()

	// Merge the exported.Config with the view.Config.
	cfg := *exported.Config

	cfg.Fset = token.NewFileSet()
	cfg.Context = context.Background()
	cfg.ParseFile = func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
		return parser.ParseFile(fset, filename, src, parser.AllErrors|parser.ParseComments)
	}

	log := xlog.New(xlog.StdSink{})
	s := &Server{
		views:       []*cache.View{cache.NewView(ctx, log, "lsp_test", span.FileURI(cfg.Dir), &cfg)},
		undelivered: make(map[span.URI][]source.Diagnostic),
		log:         log,
	}
	// Do a first pass to collect special markers for completion.
	if err := exported.Expect(map[string]interface{}{
		"item": func(name string, r packagestest.Range, _, _ string) {
			exported.Mark(name, r)
		},
	}); err != nil {
		t.Fatal(err)
	}

	expectedDiagnostics := make(diagnostics)
	completionItems := make(completionItems)
	expectedCompletions := make(completions)
	expectedFormat := make(formats)
	expectedDefinitions := make(definitions)
	expectedTypeDefinitions := make(definitions)
	expectedHighlights := make(highlights)
	expectedSymbols := &symbols{
		m:        make(map[span.URI][]protocol.DocumentSymbol),
		children: make(map[string][]protocol.DocumentSymbol),
	}
	expectedSignatures := make(signatures)

	// Collect any data that needs to be used by subsequent tests.
	if err := exported.Expect(map[string]interface{}{
		"diag":      expectedDiagnostics.collect,
		"item":      completionItems.collect,
		"complete":  expectedCompletions.collect,
		"format":    expectedFormat.collect,
		"godef":     expectedDefinitions.collect,
		"typdef":    expectedTypeDefinitions.collect,
		"highlight": expectedHighlights.collect,
		"symbol":    expectedSymbols.collect,
		"signature": expectedSignatures.collect,
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("Completion", func(t *testing.T) {
		t.Helper()
		if len(expectedCompletions) != expectedCompletionsCount {
			t.Errorf("got %v completions expected %v", len(expectedCompletions), expectedCompletionsCount)
		}
		expectedCompletions.test(t, exported, s, completionItems)
	})

	t.Run("Diagnostics", func(t *testing.T) {
		t.Helper()
		diagnosticsCount := expectedDiagnostics.test(t, s.views[0])
		if diagnosticsCount != expectedDiagnosticsCount {
			t.Errorf("got %v diagnostics expected %v", diagnosticsCount, expectedDiagnosticsCount)
		}
	})

	t.Run("Format", func(t *testing.T) {
		if _, err := exec.LookPath("gofmt"); err != nil {
			switch runtime.GOOS {
			case "android":
				t.Skip("gofmt is not installed")
			default:
				t.Fatal(err)
			}
		}
		t.Helper()
		if len(expectedFormat) != expectedFormatCount {
			t.Errorf("got %v formats expected %v", len(expectedFormat), expectedFormatCount)
		}
		expectedFormat.test(t, s)
	})

	t.Run("Definitions", func(t *testing.T) {
		t.Helper()
		if len(expectedDefinitions) != expectedDefinitionsCount {
			t.Errorf("got %v definitions expected %v", len(expectedDefinitions), expectedDefinitionsCount)
		}
		expectedDefinitions.test(t, s, false)
	})

	t.Run("TypeDefinitions", func(t *testing.T) {
		t.Helper()
		if len(expectedTypeDefinitions) != expectedTypeDefinitionsCount {
			t.Errorf("got %v type definitions expected %v", len(expectedTypeDefinitions), expectedTypeDefinitionsCount)
		}
		expectedTypeDefinitions.test(t, s, true)
	})

	t.Run("Highlights", func(t *testing.T) {
		t.Helper()
		if len(expectedHighlights) != expectedHighlightsCount {
			t.Errorf("got %v highlights expected %v", len(expectedHighlights), expectedHighlightsCount)
		}
		expectedHighlights.test(t, s)
	})

	t.Run("Symbols", func(t *testing.T) {
		t.Helper()
		if len(expectedSymbols.m) != expectedSymbolsCount {
			t.Errorf("got %v symbols expected %v", len(expectedSymbols.m), expectedSymbolsCount)
		}
		expectedSymbols.test(t, s)
	})

	t.Run("Signatures", func(t *testing.T) {
		t.Helper()
		if len(expectedSignatures) != expectedSignaturesCount {
			t.Errorf("got %v signatures expected %v", len(expectedSignatures), expectedSignaturesCount)
		}
		expectedSignatures.test(t, s)
	})
}

type diagnostics map[span.URI][]protocol.Diagnostic
type completionItems map[token.Pos]*protocol.CompletionItem
type completions map[token.Position][]token.Pos
type formats map[string]string
type definitions map[protocol.Location]protocol.Location
type highlights map[string][]protocol.Location
type symbols struct {
	m        map[span.URI][]protocol.DocumentSymbol
	children map[string][]protocol.DocumentSymbol
}
type signatures map[token.Position]*protocol.SignatureHelp

func (d diagnostics) test(t *testing.T, v source.View) int {
	count := 0
	ctx := context.Background()
	for uri, want := range d {
		sourceDiagnostics, err := source.Diagnostics(context.Background(), v, uri)
		if err != nil {
			t.Fatal(err)
		}
		got, err := toProtocolDiagnostics(ctx, v, sourceDiagnostics[uri])
		if err != nil {
			t.Fatal(err)
		}
		if diff := diffDiagnostics(uri, want, got); diff != "" {
			t.Error(diff)
		}
		count += len(want)
	}
	return count
}

func (d diagnostics) collect(e *packagestest.Exported, fset *token.FileSet, rng packagestest.Range, msgSource, msg string) {
	spn, m := testLocation(e, fset, rng)
	if _, ok := d[spn.URI()]; !ok {
		d[spn.URI()] = []protocol.Diagnostic{}
	}
	// If a file has an empty diagnostic message, return. This allows us to
	// avoid testing diagnostics in files that may have a lot of them.
	if msg == "" {
		return
	}
	severity := protocol.SeverityError
	if strings.Contains(string(spn.URI()), "analyzer") {
		severity = protocol.SeverityWarning
	}
	dRng, err := m.Range(spn)
	if err != nil {
		return
	}
	want := protocol.Diagnostic{
		Range:    dRng,
		Severity: severity,
		Source:   msgSource,
		Message:  msg,
	}
	d[spn.URI()] = append(d[spn.URI()], want)
}

func sortDiagnostics(d []protocol.Diagnostic) {
	sort.Slice(d, func(i int, j int) bool {
		if d[i].Range.Start.Line < d[j].Range.Start.Line {
			return true
		}
		if d[i].Range.Start.Line > d[j].Range.Start.Line {
			return false
		}
		if d[i].Range.Start.Character < d[j].Range.Start.Character {
			return true
		}
		if d[i].Range.Start.Character > d[j].Range.Start.Character {
			return false
		}
		return d[i].Message < d[j].Message
	})
}

// diffDiagnostics prints the diff between expected and actual diagnostics test
// results.
func diffDiagnostics(uri span.URI, want, got []protocol.Diagnostic) string {
	sortDiagnostics(want)
	sortDiagnostics(got)
	if len(got) != len(want) {
		return summarizeDiagnostics(-1, want, got, "different lengths got %v want %v", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if w.Message != g.Message {
			return summarizeDiagnostics(i, want, got, "incorrect Message got %v want %v", g.Message, w.Message)
		}
		if w.Range.Start != g.Range.Start {
			return summarizeDiagnostics(i, want, got, "incorrect Range.Start got %v want %v", g.Range.Start, w.Range.Start)
		}
		// Special case for diagnostics on parse errors.
		if strings.Contains(string(uri), "noparse") {
			if g.Range.Start != g.Range.End || w.Range.Start != g.Range.End {
				return summarizeDiagnostics(i, want, got, "incorrect Range.End got %v want %v", g.Range.End, w.Range.Start)
			}
		} else if g.Range.End != g.Range.Start { // Accept any 'want' range if the diagnostic returns a zero-length range.
			if w.Range.End != g.Range.End {
				return summarizeDiagnostics(i, want, got, "incorrect Range.End got %v want %v", g.Range.End, w.Range.End)
			}
		}
		if w.Severity != g.Severity {
			return summarizeDiagnostics(i, want, got, "incorrect Severity got %v want %v", g.Severity, w.Severity)
		}
		if w.Source != g.Source {
			return summarizeDiagnostics(i, want, got, "incorrect Source got %v want %v", g.Source, w.Source)
		}
	}
	return ""
}

func summarizeDiagnostics(i int, want []protocol.Diagnostic, got []protocol.Diagnostic, reason string, args ...interface{}) string {
	msg := &bytes.Buffer{}
	fmt.Fprint(msg, "diagnostics failed")
	if i >= 0 {
		fmt.Fprintf(msg, " at %d", i)
	}
	fmt.Fprint(msg, " because of ")
	fmt.Fprintf(msg, reason, args...)
	fmt.Fprint(msg, ":\nexpected:\n")
	for _, d := range want {
		fmt.Fprintf(msg, "  %v\n", d)
	}
	fmt.Fprintf(msg, "got:\n")
	for _, d := range got {
		fmt.Fprintf(msg, "  %v\n", d)
	}
	return msg.String()
}

func (c completions) test(t *testing.T, exported *packagestest.Exported, s *Server, items completionItems) {
	for src, itemList := range c {
		var want []protocol.CompletionItem
		for _, pos := range itemList {
			want = append(want, *items[pos])
		}
		list, err := s.Completion(context.Background(), &protocol.CompletionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: protocol.NewURI(span.FileURI(src.Filename)),
				},
				Position: protocol.Position{
					Line:      float64(src.Line - 1),
					Character: float64(src.Column - 1),
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		wantBuiltins := strings.Contains(src.Filename, "builtins")
		var got []protocol.CompletionItem
		for _, item := range list.Items {
			if !wantBuiltins && isBuiltin(item) {
				continue
			}
			got = append(got, item)
		}
		if err != nil {
			t.Fatalf("completion failed for %s:%v:%v: %v", filepath.Base(src.Filename), src.Line, src.Column, err)
		}
		if diff := diffCompletionItems(t, src, want, got); diff != "" {
			t.Errorf(diff)
		}
	}
}

func isBuiltin(item protocol.CompletionItem) bool {
	// If a type has no detail, it is a builtin type.
	if item.Detail == "" && item.Kind == protocol.TypeParameterCompletion {
		return true
	}
	// Remaining builtin constants, variables, interfaces, and functions.
	trimmed := item.Label
	if i := strings.Index(trimmed, "("); i >= 0 {
		trimmed = trimmed[:i]
	}
	switch trimmed {
	case "append", "cap", "close", "complex", "copy", "delete",
		"error", "false", "imag", "iota", "len", "make", "new",
		"nil", "panic", "print", "println", "real", "recover", "true":
		return true
	}
	return false
}

func (c completions) collect(src token.Position, expected []token.Pos) {
	c[src] = expected
}

func (i completionItems) collect(pos token.Pos, label, detail, kind string) {
	i[pos] = &protocol.CompletionItem{
		Label:  label,
		Detail: detail,
		Kind:   protocol.ParseCompletionItemKind(kind),
	}
}

// diffCompletionItems prints the diff between expected and actual completion
// test results.
func diffCompletionItems(t *testing.T, pos token.Position, want, got []protocol.CompletionItem) string {
	if len(got) != len(want) {
		return summarizeCompletionItems(-1, want, got, "different lengths got %v want %v", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if w.Label != g.Label {
			return summarizeCompletionItems(i, want, got, "incorrect Label got %v want %v", g.Label, w.Label)
		}
		if w.Detail != g.Detail {
			return summarizeCompletionItems(i, want, got, "incorrect Detail got %v want %v", g.Detail, w.Detail)
		}
		if w.Kind != g.Kind {
			return summarizeCompletionItems(i, want, got, "incorrect Kind got %v want %v", g.Kind, w.Kind)
		}
	}
	return ""
}

func summarizeCompletionItems(i int, want []protocol.CompletionItem, got []protocol.CompletionItem, reason string, args ...interface{}) string {
	msg := &bytes.Buffer{}
	fmt.Fprint(msg, "completion failed")
	if i >= 0 {
		fmt.Fprintf(msg, " at %d", i)
	}
	fmt.Fprint(msg, " because of ")
	fmt.Fprintf(msg, reason, args...)
	fmt.Fprint(msg, ":\nexpected:\n")
	for _, d := range want {
		fmt.Fprintf(msg, "  %v\n", d)
	}
	fmt.Fprintf(msg, "got:\n")
	for _, d := range got {
		fmt.Fprintf(msg, "  %v\n", d)
	}
	return msg.String()
}

func (f formats) test(t *testing.T, s *Server) {
	ctx := context.Background()
	for filename, gofmted := range f {
		uri := span.FileURI(filename)
		edits, err := s.Formatting(context.Background(), &protocol.DocumentFormattingParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: protocol.NewURI(uri),
			},
		})
		if err != nil {
			if gofmted != "" {
				t.Error(err)
			}
			continue
		}
		_, m, err := newColumnMap(ctx, s.findView(ctx, uri), uri)
		if err != nil {
			t.Error(err)
		}
		sedits, err := FromProtocolEdits(m, edits)
		if err != nil {
			t.Error(err)
		}
		ops := source.EditsToDiff(sedits)
		got := strings.Join(diff.ApplyEdits(diff.SplitLines(string(m.Content)), ops), "")
		if gofmted != got {
			t.Errorf("format failed for %s, expected:\n%v\ngot:\n%v", filename, gofmted, got)
		}
	}
}

func (f formats) collect(pos token.Position) {
	cmd := exec.Command("gofmt", pos.Filename)
	stdout := bytes.NewBuffer(nil)
	cmd.Stdout = stdout
	cmd.Run() // ignore error, sometimes we have intentionally ungofmt-able files
	f[pos.Filename] = stdout.String()
}

func (d definitions) test(t *testing.T, s *Server, typ bool) {
	for src, target := range d {
		params := &protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: src.URI,
			},
			Position: src.Range.Start,
		}
		var locs []protocol.Location
		var err error
		if typ {
			locs, err = s.TypeDefinition(context.Background(), params)
		} else {
			locs, err = s.Definition(context.Background(), params)
		}
		if err != nil {
			t.Fatalf("failed for %v: %v", src, err)
		}
		if len(locs) != 1 {
			t.Errorf("got %d locations for definition, expected 1", len(locs))
		}
		if locs[0] != target {
			t.Errorf("for %v got %v want %v", src, locs[0], target)
		}
	}
}

func (d definitions) collect(e *packagestest.Exported, fset *token.FileSet, src, target packagestest.Range) {
	sSrc, mSrc := testLocation(e, fset, src)
	lSrc, err := mSrc.Location(sSrc)
	if err != nil {
		return
	}
	sTarget, mTarget := testLocation(e, fset, target)
	lTarget, err := mTarget.Location(sTarget)
	if err != nil {
		return
	}
	d[lSrc] = lTarget
}

func (h highlights) collect(e *packagestest.Exported, fset *token.FileSet, name string, rng packagestest.Range) {
	s, m := testLocation(e, fset, rng)
	loc, err := m.Location(s)
	if err != nil {
		return
	}

	h[name] = append(h[name], loc)
}

func (h highlights) test(t *testing.T, s *Server) {
	for name, locations := range h {
		params := &protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: locations[0].URI,
			},
			Position: locations[0].Range.Start,
		}
		highlights, err := s.DocumentHighlight(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}
		if len(highlights) != len(locations) {
			t.Fatalf("got %d highlights for %s, expected %d", len(highlights), name, len(locations))
		}
		for i := range highlights {
			if highlights[i].Range != locations[i].Range {
				t.Errorf("want %v, got %v\n", locations[i].Range, highlights[i].Range)
			}
		}
	}
}

func (s symbols) collect(e *packagestest.Exported, fset *token.FileSet, name string, rng span.Range, kind string, parentName string) {
	f := fset.File(rng.Start)
	if f == nil {
		return
	}

	content, err := e.FileContents(f.Name())
	if err != nil {
		return
	}

	spn, err := rng.Span()
	if err != nil {
		return
	}

	m := protocol.NewColumnMapper(spn.URI(), fset, f, content)
	prng, err := m.Range(spn)
	if err != nil {
		return
	}

	sym := protocol.DocumentSymbol{
		Name:           name,
		Kind:           protocol.ParseSymbolKind(kind),
		SelectionRange: prng,
	}
	if parentName == "" {
		s.m[spn.URI()] = append(s.m[spn.URI()], sym)
	} else {
		s.children[parentName] = append(s.children[parentName], sym)
	}
}

func (s symbols) test(t *testing.T, server *Server) {
	for uri, expectedSymbols := range s.m {
		params := &protocol.DocumentSymbolParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: string(uri),
			},
		}
		symbols, err := server.DocumentSymbol(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}

		if len(symbols) != len(expectedSymbols) {
			t.Errorf("want %d top-level symbols in %v, got %d", len(expectedSymbols), uri, len(symbols))
			continue
		}

		for i := range expectedSymbols {
			children := s.children[expectedSymbols[i].Name]
			expectedSymbols[i].Children = children
		}
		if diff := diffSymbols(uri, expectedSymbols, symbols); diff != "" {
			t.Error(diff)
		}
	}
}

func (s signatures) collect(src token.Position, signature string, activeParam int64) {
	s[src] = &protocol.SignatureHelp{
		Signatures:      []protocol.SignatureInformation{{Label: signature}},
		ActiveSignature: 0,
		ActiveParameter: float64(activeParam),
	}
}

func diffSignatures(src token.Position, want, got *protocol.SignatureHelp) string {
	decorate := func(f string, args ...interface{}) string {
		return fmt.Sprintf("Invalid signature at %s: %s", src, fmt.Sprintf(f, args...))
	}

	if lw, lg := len(want.Signatures), len(got.Signatures); lw != lg {
		return decorate("wanted %d signatures, got %d", lw, lg)
	}

	if want.ActiveSignature != got.ActiveSignature {
		return decorate("wanted active signature of %f, got %f", want.ActiveSignature, got.ActiveSignature)
	}

	if want.ActiveParameter != got.ActiveParameter {
		return decorate("wanted active parameter of %f, got %f", want.ActiveParameter, got.ActiveParameter)
	}

	for i := range want.Signatures {
		wantSig, gotSig := want.Signatures[i], got.Signatures[i]

		if wantSig.Label != gotSig.Label {
			return decorate("wanted label %q, got %q", wantSig.Label, gotSig.Label)
		}

		var paramParts []string
		for _, p := range gotSig.Parameters {
			paramParts = append(paramParts, p.Label)
		}
		paramsStr := strings.Join(paramParts, ", ")
		if !strings.Contains(gotSig.Label, paramsStr) {
			return decorate("expected signature %q to contain params %q", gotSig.Label, paramsStr)
		}
	}

	return ""
}

func (s signatures) test(t *testing.T, server *Server) {
	for src, expectedSignatures := range s {
		gotSignatures, err := server.SignatureHelp(context.Background(), &protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: protocol.NewURI(span.FileURI(src.Filename)),
			},
			Position: protocol.Position{
				Line:      float64(src.Line - 1),
				Character: float64(src.Column - 1),
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		if diff := diffSignatures(src, expectedSignatures, gotSignatures); diff != "" {
			t.Error(diff)
		}
	}
}

func diffSymbols(uri span.URI, want, got []protocol.DocumentSymbol) string {
	sort.Slice(want, func(i, j int) bool { return want[i].Name < want[j].Name })
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })
	if len(got) != len(want) {
		return summarizeSymbols(-1, want, got, "different lengths got %v want %v", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if w.Name != g.Name {
			return summarizeSymbols(i, want, got, "incorrect name got %v want %v", g.Name, w.Name)
		}
		if w.Kind != g.Kind {
			return summarizeSymbols(i, want, got, "incorrect kind got %v want %v", g.Kind, w.Kind)
		}
		if w.SelectionRange != g.SelectionRange {
			return summarizeSymbols(i, want, got, "incorrect span got %v want %v", g.SelectionRange, w.SelectionRange)
		}
		if msg := diffSymbols(uri, w.Children, g.Children); msg != "" {
			return fmt.Sprintf("children of %s: %s", w.Name, msg)
		}
	}
	return ""
}

func summarizeSymbols(i int, want []protocol.DocumentSymbol, got []protocol.DocumentSymbol, reason string, args ...interface{}) string {
	msg := &bytes.Buffer{}
	fmt.Fprint(msg, "document symbols failed")
	if i >= 0 {
		fmt.Fprintf(msg, " at %d", i)
	}
	fmt.Fprint(msg, " because of ")
	fmt.Fprintf(msg, reason, args...)
	fmt.Fprint(msg, ":\nexpected:\n")
	for _, s := range want {
		fmt.Fprintf(msg, "  %v %v %v\n", s.Name, s.Kind, s.SelectionRange)
	}
	fmt.Fprintf(msg, "got:\n")
	for _, s := range got {
		fmt.Fprintf(msg, "  %v %v %v\n", s.Name, s.Kind, s.SelectionRange)
	}
	return msg.String()
}

func testLocation(e *packagestest.Exported, fset *token.FileSet, rng packagestest.Range) (span.Span, *protocol.ColumnMapper) {
	spn, err := span.NewRange(fset, rng.Start, rng.End).Span()
	if err != nil {
		return spn, nil
	}
	f := fset.File(rng.Start)
	content, err := e.FileContents(f.Name())
	if err != nil {
		return spn, nil
	}
	m := protocol.NewColumnMapper(spn.URI(), fset, f, content)
	return spn, m
}

func TestBytesOffset(t *testing.T) {
	tests := []struct {
		text string
		pos  protocol.Position
		want int
	}{
		{text: `a𐐀b`, pos: protocol.Position{Line: 0, Character: 0}, want: 0},
		{text: `a𐐀b`, pos: protocol.Position{Line: 0, Character: 1}, want: 1},
		{text: `a𐐀b`, pos: protocol.Position{Line: 0, Character: 2}, want: 1},
		{text: `a𐐀b`, pos: protocol.Position{Line: 0, Character: 3}, want: 5},
		{text: `a𐐀b`, pos: protocol.Position{Line: 0, Character: 4}, want: 6},
		{text: `a𐐀b`, pos: protocol.Position{Line: 0, Character: 5}, want: -1},
		{text: "aaa\nbbb\n", pos: protocol.Position{Line: 0, Character: 3}, want: 3},
		{text: "aaa\nbbb\n", pos: protocol.Position{Line: 0, Character: 4}, want: -1},
		{text: "aaa\nbbb\n", pos: protocol.Position{Line: 1, Character: 0}, want: 4},
		{text: "aaa\nbbb\n", pos: protocol.Position{Line: 1, Character: 3}, want: 7},
		{text: "aaa\nbbb\n", pos: protocol.Position{Line: 1, Character: 4}, want: -1},
		{text: "aaa\nbbb\n", pos: protocol.Position{Line: 2, Character: 0}, want: 8},
		{text: "aaa\nbbb\n", pos: protocol.Position{Line: 2, Character: 1}, want: -1},
		{text: "aaa\nbbb\n\n", pos: protocol.Position{Line: 2, Character: 0}, want: 8},
	}

	for i, test := range tests {
		fname := fmt.Sprintf("test %d", i)
		fset := token.NewFileSet()
		f := fset.AddFile(fname, -1, len(test.text))
		f.SetLinesForContent([]byte(test.text))
		mapper := protocol.NewColumnMapper(span.FileURI(fname), fset, f, []byte(test.text))
		got, err := mapper.Point(test.pos)
		if err != nil && test.want != -1 {
			t.Errorf("unexpected error: %v", err)
		}
		if err == nil && got.Offset() != test.want {
			t.Errorf("want %d for %q(Line:%d,Character:%d), but got %d", test.want, test.text, int(test.pos.Line), int(test.pos.Character), got.Offset())
		}
	}
}
