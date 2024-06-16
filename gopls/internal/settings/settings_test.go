// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"reflect"
	"testing"
	"time"
)

func TestDefaultsEquivalence(t *testing.T) {
	opts1 := DefaultOptions()
	opts2 := DefaultOptions()
	if !reflect.DeepEqual(opts1, opts2) {
		t.Fatal("default options are not equivalent using reflect.DeepEqual")
	}
}

func TestSetOption(t *testing.T) {
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
			name:  "hoverKind",
			value: "Structured",
			check: func(o Options) bool {
				return o.HoverKind == Structured
			},
		},
		{
			name:  "ui.documentation.hoverKind",
			value: "Structured",
			check: func(o Options) bool {
				return o.HoverKind == Structured
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
			name: "annotations",
			value: map[string]any{
				"Nil":      false,
				"noBounds": true,
			},
			wantError: true,
			check: func(o Options) bool {
				return !o.Annotations[Nil] && !o.Annotations[Bounds]
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

	if !StaticcheckSupported {
		tests = append(tests, testCase{
			name:      "staticcheck",
			value:     true,
			check:     func(o Options) bool { return o.Staticcheck == true },
			wantError: true, // o.StaticcheckSupported is unset
		})
	}

	for _, test := range tests {
		var opts Options
		err := opts.set(test.name, test.value, make(map[string]struct{}))
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
