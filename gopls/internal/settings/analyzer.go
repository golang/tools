// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
)

// A Fix identifies kinds of suggested fix, both in Analyzer.Fix and in the
// ApplyFix subcommand (see ExecuteCommand and ApplyFixArgs.Fix).
type Fix string

const (
	FillStruct        Fix = "fill_struct"
	StubMethods       Fix = "stub_methods"
	UndeclaredName    Fix = "undeclared_name"
	ExtractVariable   Fix = "extract_variable"
	ExtractFunction   Fix = "extract_function"
	ExtractMethod     Fix = "extract_method"
	InlineCall        Fix = "inline_call"
	InvertIfCondition Fix = "invert_if_condition"
	AddEmbedImport    Fix = "add_embed_import"
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

	// Fix is the name of the suggested fix name used to invoke the suggested
	// fixes for the analyzer. It is non-empty if we expect this analyzer to
	// provide its fix separately from its diagnostics. That is, we should apply
	// the analyzer's suggested fixes through a Command, not a TextEdit.
	Fix Fix

	// ActionKind is the kind of code action this analyzer produces. If
	// unspecified the type defaults to quickfix.
	ActionKind []protocol.CodeActionKind

	// Severity is the severity set for diagnostics reported by this
	// analyzer. If left unset it defaults to Warning.
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
