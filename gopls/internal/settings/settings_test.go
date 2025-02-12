// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/clonetest"
	. "golang.org/x/tools/gopls/internal/settings"
)

func TestDefaultsEquivalence(t *testing.T) {
	opts1 := DefaultOptions()
	opts2 := DefaultOptions()
	if !reflect.DeepEqual(opts1, opts2) {
		t.Fatal("default options are not equivalent using reflect.DeepEqual")
	}
}

func TestOptions_Set(t *testing.T) {
	type testCase struct {
		name      string
		value     any
		wantError bool
		check     func(Options) bool
	}
	tests := []testCase{
		{
			name:  "symbolStyle",
			value: "Dynamic",
			check: func(o Options) bool { return o.SymbolStyle == DynamicSymbols },
		},
		{
			name:      "symbolStyle",
			value:     "",
			wantError: true,
			check:     func(o Options) bool { return o.SymbolStyle == "" },
		},
		{
			name:      "symbolStyle",
			value:     false,
			wantError: true,
			check:     func(o Options) bool { return o.SymbolStyle == "" },
		},
		{
			name:  "symbolMatcher",
			value: "caseInsensitive",
			check: func(o Options) bool { return o.SymbolMatcher == SymbolCaseInsensitive },
		},
		{
			name:  "completionBudget",
			value: "2s",
			check: func(o Options) bool { return o.CompletionBudget == 2*time.Second },
		},
		{
			name:  "codelenses",
			value: map[string]any{"generate": true},
			check: func(o Options) bool { return o.Codelenses["generate"] },
		},
		{
			name:  "allExperiments",
			value: true,
			check: func(o Options) bool {
				return true // just confirm that we handle this setting
			},
		},
		{
			name:  "hoverKind",
			value: "FullDocumentation",
			check: func(o Options) bool {
				return o.HoverKind == FullDocumentation
			},
		},
		{
			name:  "hoverKind",
			value: "NoDocumentation",
			check: func(o Options) bool {
				return o.HoverKind == NoDocumentation
			},
		},
		{
			name:  "hoverKind",
			value: "SingleLine",
			check: func(o Options) bool {
				return o.HoverKind == SingleLine
			},
		},
		{
			name:      "hoverKind",
			value:     "Structured",
			wantError: true,
			check: func(o Options) bool {
				return o.HoverKind == FullDocumentation
			},
		},
		{
			name:      "ui.documentation.hoverKind",
			value:     "Structured",
			wantError: true,
			check: func(o Options) bool {
				return o.HoverKind == FullDocumentation
			},
		},
		{
			name:  "hoverKind",
			value: "FullDocumentation",
			check: func(o Options) bool {
				return o.HoverKind == FullDocumentation
			},
		},
		{
			name:  "ui.documentation.hoverKind",
			value: "FullDocumentation",
			check: func(o Options) bool {
				return o.HoverKind == FullDocumentation
			},
		},
		{
			name:  "matcher",
			value: "Fuzzy",
			check: func(o Options) bool {
				return o.Matcher == Fuzzy
			},
		},
		{
			name:  "matcher",
			value: "CaseSensitive",
			check: func(o Options) bool {
				return o.Matcher == CaseSensitive
			},
		},
		{
			name:  "matcher",
			value: "CaseInsensitive",
			check: func(o Options) bool {
				return o.Matcher == CaseInsensitive
			},
		},
		{
			name:  "env",
			value: map[string]any{"testing": "true"},
			check: func(o Options) bool {
				v, found := o.Env["testing"]
				return found && v == "true"
			},
		},
		{
			name:      "env",
			value:     []string{"invalid", "input"},
			wantError: true,
			check: func(o Options) bool {
				return o.Env == nil
			},
		},
		{
			name:  "directoryFilters",
			value: []any{"-node_modules", "+project_a"},
			check: func(o Options) bool {
				return len(o.DirectoryFilters) == 2
			},
		},
		{
			name:      "directoryFilters",
			value:     []any{"invalid"},
			wantError: true,
			check: func(o Options) bool {
				return len(o.DirectoryFilters) == 0
			},
		},
		{
			name:      "directoryFilters",
			value:     []string{"-invalid", "+type"},
			wantError: true,
			check: func(o Options) bool {
				return len(o.DirectoryFilters) == 0
			},
		},
		{
			name:      "vulncheck",
			value:     []any{"invalid"},
			wantError: true,
			check: func(o Options) bool {
				return o.Vulncheck == "" // For invalid value, default to 'off'.
			},
		},
		{
			name:  "vulncheck",
			value: "Imports",
			check: func(o Options) bool {
				return o.Vulncheck == ModeVulncheckImports // For invalid value, default to 'off'.
			},
		},
		{
			name:  "vulncheck",
			value: "imports",
			check: func(o Options) bool {
				return o.Vulncheck == ModeVulncheckImports
			},
		},
	}

	for _, test := range tests {
		var opts Options
		_, err := opts.Set(map[string]any{test.name: test.value})
		if err != nil {
			if !test.wantError {
				t.Errorf("Options.set(%q, %v) failed: %v",
					test.name, test.value, err)
			}
			continue
		} else if test.wantError {
			t.Fatalf("Options.set(%q, %v) succeeded unexpectedly",
				test.name, test.value)
		}

		// TODO: this could be made much better using cmp.Diff, if that becomes
		// available in this module.
		if !test.check(opts) {
			t.Errorf("Options.set(%q, %v): unexpected result %+v", test.name, test.value, opts)
		}
	}
}

func TestOptions_Clone(t *testing.T) {
	// Test that the Options.Clone actually performs a deep clone of the Options
	// struct.

	golden := clonetest.NonZero[*Options]()
	opts := clonetest.NonZero[*Options]()
	opts2 := opts.Clone()

	// The clone should be equivalent to the original.
	if diff := cmp.Diff(golden, opts2); diff != "" {
		t.Errorf("Clone() does not match original (-want +got):\n%s", diff)
	}

	// Mutating the clone should not mutate the original.
	clonetest.ZeroOut(opts2)
	if diff := cmp.Diff(golden, opts); diff != "" {
		t.Errorf("Mutating clone mutated the original (-want +got):\n%s", diff)
	}
}
