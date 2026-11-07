// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
)

func TestGoplsOptions(t *testing.T) {
	t.Run("nil options", func(t *testing.T) {
		m := goplsOptions(nil)
		analyses, ok := m["analyses"].(map[string]any)
		if !ok {
			t.Fatalf("analyses is %T, want map[string]any", m["analyses"])
		}
		for _, name := range []string{"fillreturns", "nonewvars", "noresultvalues"} {
			if got, ok := analyses[name]; !ok || got != true {
				t.Errorf("analyses[%q] = %v, want true", name, got)
			}
		}
	})

	t.Run("default options", func(t *testing.T) {
		opts := settings.DefaultOptions()
		m := goplsOptions(opts)
		if got, ok := m["symbolMatcher"]; !ok || got != string(settings.SymbolFastFuzzy) {
			t.Errorf("symbolMatcher = %v, want %v", got, settings.SymbolFastFuzzy)
		}
	})

	t.Run("custom overrides", func(t *testing.T) {
		opts := settings.DefaultOptions()
		opts.VerboseWorkDoneProgress = true
		opts.VerboseOutput = true
		opts.SymbolMatcher = settings.SymbolCaseSensitive
		opts.SymbolStyle = settings.DynamicSymbols
		opts.SymbolScope = settings.WorkspaceSymbolScope
		opts.SemanticTokens = true
		opts.BuildFlags = []string{"-tags=foo"}
		opts.DirectoryFilters = []string{"-vendor"}
		opts.HoverKind = settings.SingleLine
		opts.LinkTarget = "example.com"
		opts.Local = "example.com/mod"
		opts.Gofumpt = true
		opts.Staticcheck = true
		opts.StaticcheckProvided = true
		opts.DiagnosticsDelay = 500 * time.Millisecond
		opts.DiagnosticsTrigger = settings.DiagnosticsOnSave
		opts.FileWatcher = settings.FileWatcherPoll
		opts.Codelenses = map[settings.CodeLensSource]bool{
			settings.CodeLensTest: true,
		}
		opts.Analyses = map[string]bool{
			"fillreturns":    false,
			"unusedvariable": true,
		}

		m := goplsOptions(opts)

		want := map[string]any{
			"analyses": map[string]any{
				"fillreturns":    false,
				"nonewvars":      true,
				"noresultvalues": true,
				"unusedvariable": true,
			},
			"annotations": map[string]any{
				"bounds": true,
				"escape": true,
				"inline": true,
				"nil":    true,
			},
			"buildFlags":              []any{"-tags=foo"},
			"codelenses":              map[string]any{"test": true},
			"diagnosticsDelay":        "500ms",
			"diagnosticsTrigger":      string(settings.DiagnosticsOnSave),
			"directoryFilters":        []any{"-vendor"},
			"fileWatcher":             string(settings.FileWatcherPoll),
			"gofumpt":                 true,
			"hoverKind":               string(settings.SingleLine),
			"importShortcut":          "Both",
			"linkTarget":              "example.com",
			"linksInHover":            true,
			"local":                   "example.com/mod",
			"matcher":                 "Fuzzy",
			"semanticTokens":          true,
			"standaloneTags":          []any{"ignore"},
			"staticcheck":             true,
			"symbolMatcher":           string(settings.SymbolCaseSensitive),
			"symbolScope":             string(settings.WorkspaceSymbolScope),
			"symbolStyle":             string(settings.DynamicSymbols),
			"verboseOutput":           true,
			"verboseWorkDoneProgress": true,
			"vulncheck":               "Prompt",
		}

		if diff := cmp.Diff(want, m); diff != "" {
			t.Errorf("goplsOptions mismatch (-want +got):\n%s", diff)
		}

		// Verify that Options.Set accepts the generated options map without error.
		target := settings.DefaultOptions()
		if _, errs := target.Set(m); len(errs) > 0 {
			t.Errorf("target.Set(m) returned errors: %v", errs)
		}
	})

	t.Run("nil options to initParams", func(t *testing.T) {
		params := initParams("/tmp/test", nil)
		if params.ClientInfo == nil {
			t.Fatal("ClientInfo is nil")
		}
		if params.InitializationOptions == nil {
			t.Fatal("InitializationOptions is nil")
		}
	})
}

func TestClientConfiguration(t *testing.T) {
	app := newApplication()
	opts := settings.DefaultOptions()
	opts.SymbolMatcher = settings.SymbolCaseSensitive
	cli := newClient(app, opts)

	params := &protocol.ParamConfiguration{
		Items: []protocol.ConfigurationItem{
			{Section: "gopls"},
			{Section: "other"},
		},
	}
	results, err := cli.Configuration(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[1] != nil {
		t.Errorf("results[1] = %v, want nil for non-gopls section", results[1])
	}
	m, ok := results[0].(map[string]any)
	if !ok {
		t.Fatalf("results[0] is %T, want map[string]any", results[0])
	}
	if got := m["symbolMatcher"]; got != string(settings.SymbolCaseSensitive) {
		t.Errorf("symbolMatcher = %v, want %v", got, settings.SymbolCaseSensitive)
	}
}
