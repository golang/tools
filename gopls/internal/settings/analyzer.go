// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/gopls/internal/protocol"
)

// Analyzer augments a go/analysis analyzer with additional LSP configuration.
type Analyzer struct {
	Analyzer *analysis.Analyzer

	// Enabled reports whether the analyzer is enabled. This value can be
	// configured per-analysis in user settings. For staticcheck analyzers,
	// the value of the Staticcheck setting overrides this field.
	//
	// Most clients should use the IsEnabled method.
	Enabled bool

	// ActionKinds is the set of kinds of code action this analyzer produces.
	// If empty, the set is just QuickFix.
	ActionKinds []protocol.CodeActionKind

	// Severity is the severity set for diagnostics reported by this
	// analyzer. If left unset it defaults to Warning.
	//
	// Note: diagnostics with severity protocol.SeverityHint do not show up in
	// the VS Code "problems" tab.
	Severity protocol.DiagnosticSeverity

	// Tag is extra tags (unnecessary, deprecated, etc) for diagnostics
	// reported by this analyzer.
	Tag []protocol.DiagnosticTag
}

func (a *Analyzer) String() string { return a.Analyzer.String() }

// IsEnabled reports whether this analyzer is enabled by the given options.
func (a Analyzer) IsEnabled(options *Options) bool {
	// Staticcheck analyzers can only be enabled when staticcheck is on.
	if _, ok := options.StaticcheckAnalyzers[a.Analyzer.Name]; ok {
		if !options.Staticcheck {
			return false
		}
	}
	if enabled, ok := options.Analyses[a.Analyzer.Name]; ok {
		return enabled
	}
	return a.Enabled
}
