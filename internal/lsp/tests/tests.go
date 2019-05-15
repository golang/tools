// Copyright 2019q The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tests

import (
	"context"
	"flag"
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

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/packages/packagestest"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/internal/txtar"
)

// We hardcode the expected number of test cases to ensure that all tests
// are being executed. If a test is added, this number must be changed.
const (
	ExpectedCompletionsCount       = 107
	ExpectedCompletionSnippetCount = 13
	ExpectedDiagnosticsCount       = 17
	ExpectedFormatCount            = 5
	ExpectedDefinitionsCount       = 33
	ExpectedTypeDefinitionsCount   = 2
	ExpectedHighlightsCount        = 2
	ExpectedSymbolsCount           = 1
	ExpectedSignaturesCount        = 20
	ExpectedLinksCount             = 2
)

const (
	overlayFileSuffix = ".overlay"
	goldenFileSuffix  = ".golden"
	inFileSuffix      = ".in"
	testModule        = "golang.org/x/tools/internal/lsp"
)

var updateGolden = flag.Bool("golden", false, "Update golden files")

type Diagnostics map[span.URI][]source.Diagnostic
type CompletionItems map[token.Pos]*source.CompletionItem
type Completions map[span.Span][]token.Pos
type CompletionSnippets map[span.Span]CompletionSnippet
type Formats []span.Span
type Definitions map[span.Span]Definition
type Highlights map[string][]span.Span
type Symbols map[span.URI][]source.Symbol
type SymbolsChildren map[string][]source.Symbol
type Signatures map[span.Span]source.SignatureInformation
type Links map[span.URI][]Link

type Data struct {
	Config             packages.Config
	Exported           *packagestest.Exported
	Diagnostics        Diagnostics
	CompletionItems    CompletionItems
	Completions        Completions
	CompletionSnippets CompletionSnippets
	Formats            Formats
	Definitions        Definitions
	Highlights         Highlights
	Symbols            Symbols
	symbolsChildren    SymbolsChildren
	Signatures         Signatures
	Links              Links

	t         testing.TB
	fragments map[string]string
	dir       string
	golden    map[string]*Golden
}

type Tests interface {
	Diagnostics(*testing.T, Diagnostics)
	Completion(*testing.T, Completions, CompletionSnippets, CompletionItems)
	Format(*testing.T, Formats)
	Definition(*testing.T, Definitions)
	Highlight(*testing.T, Highlights)
	Symbol(*testing.T, Symbols)
	SignatureHelp(*testing.T, Signatures)
	Link(*testing.T, Links)
}

type Definition struct {
	Name   string
	Src    span.Span
	IsType bool
	Def    span.Span
}

type CompletionSnippet struct {
	CompletionItem     token.Pos
	PlainSnippet       string
	PlaceholderSnippet string
}

type Link struct {
	Src    span.Span
	Target string
}

type Golden struct {
	Filename string
	Archive  *txtar.Archive
	Modified bool
}

func Load(t testing.TB, exporter packagestest.Exporter, dir string) *Data {
	t.Helper()

	data := &Data{
		Diagnostics:        make(Diagnostics),
		CompletionItems:    make(CompletionItems),
		Completions:        make(Completions),
		CompletionSnippets: make(CompletionSnippets),
		Definitions:        make(Definitions),
		Highlights:         make(Highlights),
		Symbols:            make(Symbols),
		symbolsChildren:    make(SymbolsChildren),
		Signatures:         make(Signatures),
		Links:              make(Links),

		t:         t,
		dir:       dir,
		fragments: map[string]string{},
		golden:    map[string]*Golden{},
	}

	files := packagestest.MustCopyFileTree(dir)
	overlays := map[string][]byte{}
	for fragment, operation := range files {
		if trimmed := strings.TrimSuffix(fragment, goldenFileSuffix); trimmed != fragment {
			delete(files, fragment)
			goldFile := filepath.Join(dir, fragment)
			archive, err := txtar.ParseFile(goldFile)
			if err != nil {
				t.Fatalf("could not read golden file %v: %v", fragment, err)
			}
			data.golden[trimmed] = &Golden{
				Filename: goldFile,
				Archive:  archive,
			}
		} else if trimmed := strings.TrimSuffix(fragment, inFileSuffix); trimmed != fragment {
			delete(files, fragment)
			files[trimmed] = operation
		} else if index := strings.Index(fragment, overlayFileSuffix); index >= 0 {
			delete(files, fragment)
			partial := fragment[:index] + fragment[index+len(overlayFileSuffix):]
			contents, err := ioutil.ReadFile(filepath.Join(dir, fragment))
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
	data.Exported = packagestest.Export(t, exporter, modules)
	for fragment, _ := range files {
		filename := data.Exported.File(testModule, fragment)
		data.fragments[filename] = fragment
	}

	// Merge the exported.Config with the view.Config.
	data.Config = *data.Exported.Config
	data.Config.Fset = token.NewFileSet()
	data.Config.Context = context.Background()
	data.Config.ParseFile = func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
		return parser.ParseFile(fset, filename, src, parser.AllErrors|parser.ParseComments)
	}

	// Do a first pass to collect special markers for completion.
	if err := data.Exported.Expect(map[string]interface{}{
		"item": func(name string, r packagestest.Range, _, _ string) {
			data.Exported.Mark(name, r)
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Collect any data that needs to be used by subsequent tests.
	if err := data.Exported.Expect(map[string]interface{}{
		"diag":      data.collectDiagnostics,
		"item":      data.collectCompletionItems,
		"complete":  data.collectCompletions,
		"format":    data.collectFormats,
		"godef":     data.collectDefinitions,
		"typdef":    data.collectTypeDefinitions,
		"highlight": data.collectHighlights,
		"symbol":    data.collectSymbols,
		"signature": data.collectSignatures,
		"snippet":   data.collectCompletionSnippets,
		"link":      data.collectLinks,
	}); err != nil {
		t.Fatal(err)
	}
	for _, symbols := range data.Symbols {
		for i := range symbols {
			children := data.symbolsChildren[symbols[i].Name]
			symbols[i].Children = children
		}
	}
	// run a second pass to collect names for some entries.
	if err := data.Exported.Expect(map[string]interface{}{
		"godef": data.collectDefinitionNames,
	}); err != nil {
		t.Fatal(err)
	}
	return data
}

func Run(t *testing.T, tests Tests, data *Data) {
	t.Helper()
	t.Run("Completion", func(t *testing.T) {
		t.Helper()
		if len(data.Completions) != ExpectedCompletionsCount {
			t.Errorf("got %v completions expected %v", len(data.Completions), ExpectedCompletionsCount)
		}
		if len(data.CompletionSnippets) != ExpectedCompletionSnippetCount {
			t.Errorf("got %v snippets expected %v", len(data.CompletionSnippets), ExpectedCompletionSnippetCount)
		}
		tests.Completion(t, data.Completions, data.CompletionSnippets, data.CompletionItems)
	})

	t.Run("Diagnostics", func(t *testing.T) {
		t.Helper()
		diagnosticsCount := 0
		for _, want := range data.Diagnostics {
			diagnosticsCount += len(want)
		}
		if diagnosticsCount != ExpectedDiagnosticsCount {
			t.Errorf("got %v diagnostics expected %v", diagnosticsCount, ExpectedDiagnosticsCount)
		}
		tests.Diagnostics(t, data.Diagnostics)
	})

	t.Run("Format", func(t *testing.T) {
		t.Helper()
		if _, err := exec.LookPath("gofmt"); err != nil {
			switch runtime.GOOS {
			case "android":
				t.Skip("gofmt is not installed")
			default:
				t.Fatal(err)
			}
		}
		if len(data.Formats) != ExpectedFormatCount {
			t.Errorf("got %v formats expected %v", len(data.Formats), ExpectedFormatCount)
		}
		tests.Format(t, data.Formats)
	})

	t.Run("Definitions", func(t *testing.T) {
		t.Helper()
		if len(data.Definitions) != ExpectedDefinitionsCount {
			t.Errorf("got %v definitions expected %v", len(data.Definitions), ExpectedDefinitionsCount)
		}
		tests.Definition(t, data.Definitions)
	})

	t.Run("Highlights", func(t *testing.T) {
		t.Helper()
		if len(data.Highlights) != ExpectedHighlightsCount {
			t.Errorf("got %v highlights expected %v", len(data.Highlights), ExpectedHighlightsCount)
		}
		tests.Highlight(t, data.Highlights)
	})

	t.Run("Symbols", func(t *testing.T) {
		t.Helper()
		if len(data.Symbols) != ExpectedSymbolsCount {
			t.Errorf("got %v symbols expected %v", len(data.Symbols), ExpectedSymbolsCount)
		}
		tests.Symbol(t, data.Symbols)
	})

	t.Run("SignatureHelp", func(t *testing.T) {
		t.Helper()
		if len(data.Signatures) != ExpectedSignaturesCount {
			t.Errorf("got %v signatures expected %v", len(data.Signatures), ExpectedSignaturesCount)
		}
		tests.SignatureHelp(t, data.Signatures)
	})

	t.Run("Links", func(t *testing.T) {
		t.Helper()
		linksCount := 0
		for _, want := range data.Links {
			linksCount += len(want)
		}
		if linksCount != ExpectedLinksCount {
			t.Errorf("got %v links expected %v", linksCount, ExpectedLinksCount)
		}
		tests.Link(t, data.Links)
	})

	if *updateGolden {
		for _, golden := range data.golden {
			if !golden.Modified {
				continue
			}
			sort.Slice(golden.Archive.Files, func(i, j int) bool {
				return golden.Archive.Files[i].Name < golden.Archive.Files[j].Name
			})
			if err := ioutil.WriteFile(golden.Filename, txtar.Format(golden.Archive), 0666); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func (data *Data) Golden(tag string, target string, update func() ([]byte, error)) []byte {
	data.t.Helper()
	fragment, found := data.fragments[target]
	if !found {
		if filepath.IsAbs(target) {
			data.t.Fatalf("invalid golden file fragment %v", target)
		}
		fragment = target
	}
	golden := data.golden[fragment]
	if golden == nil {
		if !*updateGolden {
			data.t.Fatalf("could not find golden file %v: %v", fragment, tag)
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
	if *updateGolden {
		if file == nil {
			golden.Archive.Files = append(golden.Archive.Files, txtar.File{
				Name: tag,
			})
			file = &golden.Archive.Files[len(golden.Archive.Files)-1]
		}
		contents, err := update()
		if err != nil {
			data.t.Fatalf("could not update golden file %v: %v", fragment, err)
		}
		file.Data = append(contents, '\n') // add trailing \n for txtar
		golden.Modified = true
	}
	if file == nil {
		data.t.Fatalf("could not find golden contents %v: %v", fragment, tag)
	}
	return file.Data[:len(file.Data)-1] // drop the trailing \n
}

func (data *Data) collectDiagnostics(spn span.Span, msgSource, msg string) {
	if _, ok := data.Diagnostics[spn.URI()]; !ok {
		data.Diagnostics[spn.URI()] = []source.Diagnostic{}
	}
	// If a file has an empty diagnostic message, return. This allows us to
	// avoid testing diagnostics in files that may have a lot of them.
	if msg == "" {
		return
	}
	severity := source.SeverityError
	if strings.Contains(string(spn.URI()), "analyzer") {
		severity = source.SeverityWarning
	}
	want := source.Diagnostic{
		Span:     spn,
		Severity: severity,
		Source:   msgSource,
		Message:  msg,
	}
	data.Diagnostics[spn.URI()] = append(data.Diagnostics[spn.URI()], want)
}

func (data *Data) collectCompletions(src span.Span, expected []token.Pos) {
	data.Completions[src] = expected
}

func (data *Data) collectCompletionItems(pos token.Pos, label, detail, kind string) {
	data.CompletionItems[pos] = &source.CompletionItem{
		Label:  label,
		Detail: detail,
		Kind:   source.ParseCompletionItemKind(kind),
	}
}

func (data *Data) collectFormats(spn span.Span) {
	data.Formats = append(data.Formats, spn)
}

func (data *Data) collectDefinitions(src, target span.Span) {
	data.Definitions[src] = Definition{
		Src: src,
		Def: target,
	}
}

func (data *Data) collectTypeDefinitions(src, target span.Span) {
	data.Definitions[src] = Definition{
		Src:    src,
		Def:    target,
		IsType: true,
	}
}

func (data *Data) collectDefinitionNames(src span.Span, name string) {
	d := data.Definitions[src]
	d.Name = name
	data.Definitions[src] = d
}

func (data *Data) collectHighlights(name string, rng span.Span) {
	data.Highlights[name] = append(data.Highlights[name], rng)
}

func (data *Data) collectSymbols(name string, spn span.Span, kind string, parentName string) {
	sym := source.Symbol{
		Name:          name,
		Kind:          source.ParseSymbolKind(kind),
		SelectionSpan: spn,
	}
	if parentName == "" {
		data.Symbols[spn.URI()] = append(data.Symbols[spn.URI()], sym)
	} else {
		data.symbolsChildren[parentName] = append(data.symbolsChildren[parentName], sym)
	}
}

func (data *Data) collectSignatures(spn span.Span, signature string, activeParam int64) {
	data.Signatures[spn] = source.SignatureInformation{
		Label:           signature,
		ActiveParameter: int(activeParam),
	}
}

func (data *Data) collectCompletionSnippets(spn span.Span, item token.Pos, plain, placeholder string) {
	data.CompletionSnippets[spn] = CompletionSnippet{
		CompletionItem:     item,
		PlainSnippet:       plain,
		PlaceholderSnippet: placeholder,
	}
}

func (data *Data) collectLinks(spn span.Span, link string) {
	uri := spn.URI()
	data.Links[uri] = append(data.Links[uri], Link{
		Src:    spn,
		Target: link,
	})
}
