// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package tests exports functionality to be used across a variety of gopls tests.
package tests

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/packages/packagestest"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/lsp/tests/compare"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/typeparams"
	"golang.org/x/tools/txtar"
)

const (
	overlayFileSuffix = ".overlay"
	goldenFileSuffix  = ".golden"
	inFileSuffix      = ".in"
	summaryFile       = "summary.txt"

	// The module path containing the testdata packages.
	//
	// Warning: the length of this module path matters, as we have bumped up
	// against command-line limitations on windows (golang/go#54800).
	testModule = "golang.org/lsptests"
)

var UpdateGolden = flag.Bool("golden", false, "Update golden files")

// These type names apparently avoid the need to repeat the
// type in the field name and the make() expression.
type CallHierarchy = map[span.Span]*CallHierarchyResult
type SemanticTokens = []span.Span
type AddImport = map[span.URI]string
type SelectionRanges = []span.Span

type Data struct {
	Config          packages.Config
	Exported        *packagestest.Exported
	CallHierarchy   CallHierarchy
	SemanticTokens  SemanticTokens
	AddImport       AddImport
	SelectionRanges SelectionRanges

	fragments map[string]string
	dir       string
	golden    map[string]*Golden
	mode      string

	ModfileFlagAvailable bool

	mappersMu sync.Mutex
	mappers   map[span.URI]*protocol.Mapper
}

// The Tests interface abstracts the LSP-based implementation of the marker
// test operators appearing in files beneath ../testdata/.
//
// TODO(adonovan): reduce duplication; see https://github.com/golang/go/issues/54845.
// There is only one implementation (*runner in ../lsp_test.go), so
// we can abolish the interface now.
type Tests interface {
	CallHierarchy(*testing.T, span.Span, *CallHierarchyResult)
	SemanticTokens(*testing.T, span.Span)
	AddImport(*testing.T, span.URI, string)
	SelectionRanges(*testing.T, span.Span)
}

type Completion struct {
	CompletionItems []token.Pos
}

type CompletionSnippet struct {
	CompletionItem     token.Pos
	PlainSnippet       string
	PlaceholderSnippet string
}

type CallHierarchyResult struct {
	IncomingCalls, OutgoingCalls []protocol.CallHierarchyItem
}

type Link struct {
	Src          span.Span
	Target       string
	NotePosition token.Position
}

type SuggestedFix struct {
	ActionKind, Title string
}

type Golden struct {
	Filename string
	Archive  *txtar.Archive
	Modified bool
}

func Context(t testing.TB) context.Context {
	return context.Background()
}

func DefaultOptions(o *source.Options) {
	o.SupportedCodeActions = map[source.FileKind]map[protocol.CodeActionKind]bool{
		source.Go: {
			protocol.SourceOrganizeImports: true,
			protocol.QuickFix:              true,
			protocol.RefactorRewrite:       true,
			protocol.RefactorInline:        true,
			protocol.RefactorExtract:       true,
			protocol.SourceFixAll:          true,
		},
		source.Mod: {
			protocol.SourceOrganizeImports: true,
		},
		source.Sum:  {},
		source.Work: {},
		source.Tmpl: {},
	}
	o.InsertTextFormat = protocol.SnippetTextFormat
	o.CompletionBudget = time.Minute
	o.HierarchicalDocumentSymbolSupport = true
	o.SemanticTokens = true
	o.InternalOptions.NewDiff = "new"

	// Enable all inlay hints.
	if o.Hints == nil {
		o.Hints = make(map[string]bool)
	}
	for name := range source.AllInlayHints {
		o.Hints[name] = true
	}
}

func RunTests(t *testing.T, dataDir string, includeMultiModule bool, f func(*testing.T, *Data)) {
	t.Helper()
	modes := []string{"Modules", "GOPATH"}
	if includeMultiModule {
		modes = append(modes, "MultiModule")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			datum := load(t, mode, dataDir)
			t.Helper()
			f(t, datum)
		})
	}
}

func load(t testing.TB, mode string, dir string) *Data {
	datum := &Data{
		CallHierarchy: make(CallHierarchy),
		AddImport:     make(AddImport),

		dir:       dir,
		fragments: map[string]string{},
		golden:    map[string]*Golden{},
		mode:      mode,
		mappers:   map[span.URI]*protocol.Mapper{},
	}

	if !*UpdateGolden {
		summary := filepath.Join(filepath.FromSlash(dir), summaryFile+goldenFileSuffix)
		if _, err := os.Stat(summary); os.IsNotExist(err) {
			t.Fatalf("could not find golden file summary.txt in %#v", dir)
		}
		archive, err := txtar.ParseFile(summary)
		if err != nil {
			t.Fatalf("could not read golden file %v/%v: %v", dir, summary, err)
		}
		datum.golden[summaryFile] = &Golden{
			Filename: summary,
			Archive:  archive,
		}
	}

	files := packagestest.MustCopyFileTree(dir)
	// Prune test cases that exercise generics.
	if !typeparams.Enabled {
		for name := range files {
			if strings.Contains(name, "_generics") {
				delete(files, name)
			}
		}
	}
	overlays := map[string][]byte{}
	for fragment, operation := range files {
		if trimmed := strings.TrimSuffix(fragment, goldenFileSuffix); trimmed != fragment {
			delete(files, fragment)
			goldFile := filepath.Join(dir, fragment)
			archive, err := txtar.ParseFile(goldFile)
			if err != nil {
				t.Fatalf("could not read golden file %v: %v", fragment, err)
			}
			datum.golden[trimmed] = &Golden{
				Filename: goldFile,
				Archive:  archive,
			}
		} else if trimmed := strings.TrimSuffix(fragment, inFileSuffix); trimmed != fragment {
			delete(files, fragment)
			files[trimmed] = operation
		} else if index := strings.Index(fragment, overlayFileSuffix); index >= 0 {
			delete(files, fragment)
			partial := fragment[:index] + fragment[index+len(overlayFileSuffix):]
			contents, err := os.ReadFile(filepath.Join(dir, fragment))
			if err != nil {
				t.Fatal(err)
			}
			overlays[partial] = contents
		}
	}

	modules := []packagestest.Module{
		{
			Name:    testModule,
			Files:   files,
			Overlay: overlays,
		},
	}
	switch mode {
	case "Modules":
		datum.Exported = packagestest.Export(t, packagestest.Modules, modules)
	case "GOPATH":
		datum.Exported = packagestest.Export(t, packagestest.GOPATH, modules)
	case "MultiModule":
		files := map[string]interface{}{}
		for k, v := range modules[0].Files {
			files[filepath.Join("testmodule", k)] = v
		}
		modules[0].Files = files

		overlays := map[string][]byte{}
		for k, v := range modules[0].Overlay {
			overlays[filepath.Join("testmodule", k)] = v
		}
		modules[0].Overlay = overlays

		golden := map[string]*Golden{}
		for k, v := range datum.golden {
			if k == summaryFile {
				golden[k] = v
			} else {
				golden[filepath.Join("testmodule", k)] = v
			}
		}
		datum.golden = golden

		datum.Exported = packagestest.Export(t, packagestest.Modules, modules)
	default:
		panic("unknown mode " + mode)
	}

	for _, m := range modules {
		for fragment := range m.Files {
			filename := datum.Exported.File(m.Name, fragment)
			datum.fragments[filename] = fragment
		}
	}

	// Turn off go/packages debug logging.
	datum.Exported.Config.Logf = nil
	datum.Config.Logf = nil

	// Merge the exported.Config with the view.Config.
	datum.Config = *datum.Exported.Config
	datum.Config.Fset = token.NewFileSet()
	datum.Config.Context = Context(nil)
	datum.Config.ParseFile = func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
		panic("ParseFile should not be called")
	}

	// Do a first pass to collect special markers for completion and workspace symbols.
	if err := datum.Exported.Expect(map[string]interface{}{
		"item": func(name string, r packagestest.Range, _ []string) {
			datum.Exported.Mark(name, r)
		},
		"symbol": func(name string, r packagestest.Range, _ []string) {
			datum.Exported.Mark(name, r)
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Collect any data that needs to be used by subsequent tests.
	if err := datum.Exported.Expect(map[string]interface{}{
		"semantic":       datum.collectSemanticTokens,
		"incomingcalls":  datum.collectIncomingCalls,
		"outgoingcalls":  datum.collectOutgoingCalls,
		"addimport":      datum.collectAddImports,
		"selectionrange": datum.collectSelectionRanges,
	}); err != nil {
		t.Fatal(err)
	}

	if mode == "MultiModule" {
		if err := moveFile(filepath.Join(datum.Config.Dir, "go.mod"), filepath.Join(datum.Config.Dir, "testmodule/go.mod")); err != nil {
			t.Fatal(err)
		}
	}

	return datum
}

// moveFile moves the file at oldpath to newpath, by renaming if possible
// or copying otherwise.
func moveFile(oldpath, newpath string) (err error) {
	renameErr := os.Rename(oldpath, newpath)
	if renameErr == nil {
		return nil
	}

	src, err := os.Open(oldpath)
	if err != nil {
		return err
	}
	defer func() {
		src.Close()
		if err == nil {
			err = os.Remove(oldpath)
		}
	}()

	perm := os.ModePerm
	fi, err := src.Stat()
	if err == nil {
		perm = fi.Mode().Perm()
	}

	dst, err := os.OpenFile(newpath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}

	_, err = io.Copy(dst, src)
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	return err
}

func Run(t *testing.T, tests Tests, data *Data) {
	t.Helper()
	checkData(t, data)

	t.Run("CallHierarchy", func(t *testing.T) {
		t.Helper()
		for spn, callHierarchyResult := range data.CallHierarchy {
			t.Run(SpanName(spn), func(t *testing.T) {
				t.Helper()
				tests.CallHierarchy(t, spn, callHierarchyResult)
			})
		}
	})

	t.Run("SemanticTokens", func(t *testing.T) {
		t.Helper()
		for _, spn := range data.SemanticTokens {
			t.Run(uriName(spn.URI()), func(t *testing.T) {
				t.Helper()
				tests.SemanticTokens(t, spn)
			})
		}
	})

	t.Run("AddImport", func(t *testing.T) {
		t.Helper()
		for uri, exp := range data.AddImport {
			t.Run(uriName(uri), func(t *testing.T) {
				tests.AddImport(t, uri, exp)
			})
		}
	})

	t.Run("SelectionRanges", func(t *testing.T) {
		t.Helper()
		for _, span := range data.SelectionRanges {
			t.Run(SpanName(span), func(t *testing.T) {
				tests.SelectionRanges(t, span)
			})
		}
	})

	if *UpdateGolden {
		for _, golden := range data.golden {
			if !golden.Modified {
				continue
			}
			sort.Slice(golden.Archive.Files, func(i, j int) bool {
				return golden.Archive.Files[i].Name < golden.Archive.Files[j].Name
			})
			if err := os.WriteFile(golden.Filename, txtar.Format(golden.Archive), 0666); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func checkData(t *testing.T, data *Data) {
	buf := &bytes.Buffer{}

	fmt.Fprintf(buf, "CallHierarchyCount = %v\n", len(data.CallHierarchy))
	fmt.Fprintf(buf, "SemanticTokenCount = %v\n", len(data.SemanticTokens))
	fmt.Fprintf(buf, "SelectionRangesCount = %v\n", len(data.SelectionRanges))

	want := string(data.Golden(t, "summary", summaryFile, func() ([]byte, error) {
		return buf.Bytes(), nil
	}))
	got := buf.String()
	if want != got {
		// These counters change when assertions are added or removed.
		// They act as an independent safety net to ensure that the
		// tests didn't spuriously pass because they did no work.
		t.Errorf("test summary does not match:\n%s\n(Run with -golden to update golden file; also, there may be one per Go version.)", compare.Text(want, got))
	}
}

func (data *Data) Mapper(uri span.URI) (*protocol.Mapper, error) {
	data.mappersMu.Lock()
	defer data.mappersMu.Unlock()

	if _, ok := data.mappers[uri]; !ok {
		content, err := data.Exported.FileContents(uri.Filename())
		if err != nil {
			return nil, err
		}
		data.mappers[uri] = protocol.NewMapper(uri, content)
	}
	return data.mappers[uri], nil
}

func (data *Data) Golden(t *testing.T, tag, target string, update func() ([]byte, error)) []byte {
	t.Helper()
	fragment, found := data.fragments[target]
	if !found {
		if filepath.IsAbs(target) {
			t.Fatalf("invalid golden file fragment %v", target)
		}
		fragment = target
	}
	golden := data.golden[fragment]
	if golden == nil {
		if !*UpdateGolden {
			t.Fatalf("could not find golden file %v: %v", fragment, tag)
		}
		golden = &Golden{
			Filename: filepath.Join(data.dir, fragment+goldenFileSuffix),
			Archive:  &txtar.Archive{},
			Modified: true,
		}
		data.golden[fragment] = golden
	}
	var file *txtar.File
	for i := range golden.Archive.Files {
		f := &golden.Archive.Files[i]
		if f.Name == tag {
			file = f
			break
		}
	}
	if *UpdateGolden {
		if file == nil {
			golden.Archive.Files = append(golden.Archive.Files, txtar.File{
				Name: tag,
			})
			file = &golden.Archive.Files[len(golden.Archive.Files)-1]
		}
		contents, err := update()
		if err != nil {
			t.Fatalf("could not update golden file %v: %v", fragment, err)
		}
		file.Data = append(contents, '\n') // add trailing \n for txtar
		golden.Modified = true

	}
	if file == nil {
		t.Fatalf("could not find golden contents %v: %v", fragment, tag)
	}
	if len(file.Data) == 0 {
		return file.Data
	}
	return file.Data[:len(file.Data)-1] // drop the trailing \n
}

func (data *Data) collectAddImports(spn span.Span, imp string) {
	data.AddImport[spn.URI()] = imp
}

func (data *Data) collectSemanticTokens(spn span.Span) {
	data.SemanticTokens = append(data.SemanticTokens, spn)
}

func (data *Data) collectSelectionRanges(spn span.Span) {
	data.SelectionRanges = append(data.SelectionRanges, spn)
}

func (data *Data) collectIncomingCalls(src span.Span, calls []span.Span) {
	for _, call := range calls {
		rng := data.mustRange(call)
		// we're only comparing protocol.range
		if data.CallHierarchy[src] != nil {
			data.CallHierarchy[src].IncomingCalls = append(data.CallHierarchy[src].IncomingCalls,
				protocol.CallHierarchyItem{
					URI:   protocol.DocumentURI(call.URI()),
					Range: rng,
				})
		} else {
			data.CallHierarchy[src] = &CallHierarchyResult{
				IncomingCalls: []protocol.CallHierarchyItem{
					{URI: protocol.DocumentURI(call.URI()), Range: rng},
				},
			}
		}
	}
}

func (data *Data) collectOutgoingCalls(src span.Span, calls []span.Span) {
	if data.CallHierarchy[src] == nil {
		data.CallHierarchy[src] = &CallHierarchyResult{}
	}
	for _, call := range calls {
		// we're only comparing protocol.range
		data.CallHierarchy[src].OutgoingCalls = append(data.CallHierarchy[src].OutgoingCalls,
			protocol.CallHierarchyItem{
				URI:   protocol.DocumentURI(call.URI()),
				Range: data.mustRange(call),
			})
	}
}

// mustRange converts spn into a protocol.Range, panicking on any error.
func (data *Data) mustRange(spn span.Span) protocol.Range {
	m, err := data.Mapper(spn.URI())
	rng, err := m.SpanRange(spn)
	if err != nil {
		panic(fmt.Sprintf("converting span %s to range: %v", spn, err))
	}
	return rng
}

func uriName(uri span.URI) string {
	return filepath.Base(strings.TrimSuffix(uri.Filename(), ".go"))
}

// TODO(golang/go#54845): improve the formatting here to match standard
// line:column position formatting.
func SpanName(spn span.Span) string {
	return fmt.Sprintf("%v_%v_%v", uriName(spn.URI()), spn.Start().Line(), spn.Start().Column())
}
