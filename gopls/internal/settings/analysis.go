// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"log"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/atomicalign"
	"golang.org/x/tools/go/analysis/passes/deepequalerrors"
	"golang.org/x/tools/go/analysis/passes/fieldalignment"
	"golang.org/x/tools/go/analysis/passes/inline"
	"golang.org/x/tools/go/analysis/passes/modernize"
	"golang.org/x/tools/go/analysis/passes/nilness"
	"golang.org/x/tools/go/analysis/passes/scannererr"
	"golang.org/x/tools/go/analysis/passes/shadow"
	"golang.org/x/tools/go/analysis/passes/sortslice"
	"golang.org/x/tools/go/analysis/passes/sqlrowserr"
	"golang.org/x/tools/go/analysis/passes/unusedwrite"
	"golang.org/x/tools/go/analysis/suite/fix"
	"golang.org/x/tools/go/analysis/suite/vet"
	"golang.org/x/tools/gopls/internal/analysis/deprecated"
	"golang.org/x/tools/gopls/internal/analysis/embeddirective"
	"golang.org/x/tools/gopls/internal/analysis/errorsastypeshadow"
	"golang.org/x/tools/gopls/internal/analysis/fillreturns"
	"golang.org/x/tools/gopls/internal/analysis/infertypeargs"
	"golang.org/x/tools/gopls/internal/analysis/maprange"
	"golang.org/x/tools/gopls/internal/analysis/nonewvars"
	"golang.org/x/tools/gopls/internal/analysis/noresultvalues"
	"golang.org/x/tools/gopls/internal/analysis/ptrtoerror"
	"golang.org/x/tools/gopls/internal/analysis/recursiveiter"
	"golang.org/x/tools/gopls/internal/analysis/simplifycompositelit"
	"golang.org/x/tools/gopls/internal/analysis/simplifyrange"
	"golang.org/x/tools/gopls/internal/analysis/simplifyslice"
	"golang.org/x/tools/gopls/internal/analysis/unusedfunc"
	"golang.org/x/tools/gopls/internal/analysis/unusedparams"
	"golang.org/x/tools/gopls/internal/analysis/unusedvariable"
	"golang.org/x/tools/gopls/internal/analysis/writestring"
	"golang.org/x/tools/gopls/internal/analysis/yield"
	"golang.org/x/tools/gopls/internal/protocol"
	"honnef.co/go/tools/analysis/lint"
)

// AllAnalyzers holds the list of Analyzers available to all gopls
// sessions, independent of build version. It is the source from which
// gopls/doc/analyzers.md is generated.
var AllAnalyzers = initAnalyzers()

// Analyzer augments an [analysis.Analyzer] with additional LSP configuration.
//
// Analyzers are immutable, since they are shared across multiple LSP sessions.
type Analyzer struct {
	analyzer    *analysis.Analyzer
	staticcheck *lint.RawDocumentation // only for staticcheck analyzers
	nonDefault  bool                   // (sense is negated so we can mostly omit it)
	actionKinds []protocol.CodeActionKind
	severity    protocol.DiagnosticSeverity
	tags        []protocol.DiagnosticTag
}

// Analyzer returns the [analysis.Analyzer] that this Analyzer wraps.
func (a *Analyzer) Analyzer() *analysis.Analyzer { return a.analyzer }

// Enabled reports whether the analyzer is enabled by the options.
// This value can be configured per-analysis in user settings.
func (a *Analyzer) Enabled(o *Options) bool {
	// An explicit setting by name takes precedence.
	if v, found := o.Analyses[a.Analyzer().Name]; found {
		return v
	}
	if a.staticcheck != nil {
		// An explicit staticcheck={true,false} setting
		// enables/disables all staticcheck analyzers.
		if o.StaticcheckProvided {
			return o.Staticcheck
		}
		// Respect staticcheck's off-by-default options too.
		// (This applies to only a handful of analyzers.)
		if a.staticcheck.NonDefault {
			return false
		}
	}
	// Respect gopls' default setting.
	return !a.nonDefault
}

// ActionKinds is the set of kinds of code action this analyzer produces.
//
// If left unset, it defaults to QuickFix.
// TODO(rfindley): revisit.
func (a *Analyzer) ActionKinds() []protocol.CodeActionKind { return a.actionKinds }

// Severity is the severity set for diagnostics reported by this analyzer.
// The default severity is SeverityWarning.
//
// While the LSP spec does not specify how severity should be used, here are
// some guiding heuristics:
//   - Error: for parse and type errors, which would stop the build.
//   - Warning: for analyzer diagnostics reporting likely bugs.
//   - Info: for analyzer diagnostics that do not indicate bugs, but may
//     suggest inaccurate or superfluous code.
//   - Hint: for analyzer diagnostics that do not indicate mistakes, but offer
//     simplifications or modernizations. By their nature, hints should
//     generally carry quick fixes.
//
// The difference between Info and Hint is particularly subtle. Importantly,
// Hint diagnostics do not appear in the Problems tab in VS Code, so they are
// less intrusive than Info diagnostics. The rule of thumb is this: use Info if
// the diagnostic is not a bug, but the author probably didn't mean to write
// the code that way. Use Hint if the diagnostic is not a bug and the author
// intended to write the code that way, but there is a simpler or more modern
// way to express the same logic. An 'unused' diagnostic is Info level, since
// the author probably didn't mean to check in unreachable code. A 'modernize'
// or 'deprecated' diagnostic is Hint level, since the author intended to write
// the code that way, but now there is a better way.
func (a *Analyzer) Severity() protocol.DiagnosticSeverity {
	if a.severity == 0 {
		return protocol.SeverityWarning
	}
	return a.severity
}

// Tags is extra tags (unnecessary, deprecated, etc) for diagnostics
// reported by this analyzer.
func (a *Analyzer) Tags() []protocol.DiagnosticTag { return a.tags }

// String returns the name of this analyzer.
func (a *Analyzer) String() string { return a.analyzer.String() }

func initAnalyzers() (res []*Analyzer) {
	seen := make(map[*analysis.Analyzer]bool)

	// Start with the traditional vet and fix suites.
	for _, suite := range []struct {
		name      string
		analyzers []*analysis.Analyzer
		severity  protocol.DiagnosticSeverity
	}{
		{"fix.Suite", fix.Suite, protocol.SeverityHint},
		{"vet.Suite", vet.Suite, protocol.SeverityWarning},
	} {
		for _, a := range suite.analyzers {
			// De-duplicate, since the suites overlap.
			if !seen[a] {
				seen[a] = true
				res = append(res, &Analyzer{analyzer: a, severity: suite.severity})
			}
		}
	}

	// set inline -lazy_edit mode
	if err := inline.Analyzer.Flags.Set("lazy_edits", "true"); err != nil {
		log.Fatalf("setting inline -lazy_edits flag: %v", err)
	}

	// See [Analyzer.Severity] for guidance on setting analyzer severity below.
	for _, a := range []*Analyzer{
		// not suitable for vet.Suite:
		// - some (nilness, yield) use go/ssa; see #59714.
		// - others don't meet the "frequency" criterion;
		//   see GOROOT/src/cmd/vet/README.
		{analyzer: atomicalign.Analyzer},
		{analyzer: deprecated.Analyzer,
			severity: protocol.SeverityHint,
			tags:     []protocol.DiagnosticTag{protocol.Deprecated},
		},
		{analyzer: deepequalerrors.Analyzer},
		{analyzer: nilness.Analyzer}, // uses go/ssa
		{analyzer: yield.Analyzer},   // uses go/ssa
		{analyzer: sortslice.Analyzer},
		{analyzer: embeddirective.Analyzer},
		{analyzer: scannererr.Analyzer},         // to appear in cmd/vet@go1.28
		{analyzer: sqlrowserr.Analyzer},         // to appear in cmd/vet@go1.28
		{analyzer: recursiveiter.Analyzer},      // under evaluation
		{analyzer: errorsastypeshadow.Analyzer}, // under evaluation
		{analyzer: writestring.Analyzer},        // under evaluation
		{analyzer: ptrtoerror.Analyzer},         // under evaluation

		// disabled due to high false positives
		{analyzer: shadow.Analyzer, severity: protocol.SeverityHint, nonDefault: true},         // very noisy
		{analyzer: fieldalignment.Analyzer, severity: protocol.SeverityHint, nonDefault: true}, // #67762, #76237

		// simplifiers and modernizers (beyond fix.Suite)
		//
		// These analyzers offer mere style fixes on correct code,
		// thus they will never appear in cmd/vet or cmd/fix and
		// their severity level is "information".
		//
		// gofmt -s suite
		{
			analyzer:    simplifycompositelit.Analyzer,
			actionKinds: []protocol.CodeActionKind{protocol.SourceFixAll, protocol.QuickFix},
			severity:    protocol.SeverityInformation,
		},
		{
			analyzer:    simplifyrange.Analyzer,
			actionKinds: []protocol.CodeActionKind{protocol.SourceFixAll, protocol.QuickFix},
			severity:    protocol.SeverityInformation,
		},
		{
			analyzer:    simplifyslice.Analyzer,
			actionKinds: []protocol.CodeActionKind{protocol.SourceFixAll, protocol.QuickFix},
			severity:    protocol.SeverityInformation,
		},
		// other simplifiers
		{analyzer: infertypeargs.Analyzer, severity: protocol.SeverityInformation},
		{analyzer: maprange.Analyzer, severity: protocol.SeverityHint},
		{analyzer: unusedparams.Analyzer, severity: protocol.SeverityInformation},
		{analyzer: unusedfunc.Analyzer, severity: protocol.SeverityInformation},
		{analyzer: unusedwrite.Analyzer, severity: protocol.SeverityInformation}, // uses go/ssa
		// modernizers not included in modernize.Suite (nor fix.Suite)
		{analyzer: modernize.AppendClippedAnalyzer, nonDefault: true}, // not nil-preserving
		{analyzer: modernize.BLoopAnalyzer},                           // may skew benchmark results, see golang/go#74967
		{analyzer: modernize.FmtAppendfAnalyzer},                      // makes code less clear, see golang/go#77581
		{analyzer: modernize.SlicesDeleteAnalyzer, nonDefault: true},  // not nil-preserving

		// type-error analyzers
		// These analyzers enrich go/types errors with suggested fixes.
		// Since they exist only to attach their fixes to type errors, their
		// severity is irrelevant.
		{analyzer: fillreturns.Analyzer},
		{analyzer: nonewvars.Analyzer},
		{analyzer: noresultvalues.Analyzer},
		{analyzer: unusedvariable.Analyzer},
	} {
		if seen[a.analyzer] {
			log.Fatalf("duplicate analyzer: %q", a.analyzer.Name)
		}
		seen[a.analyzer] = true
		res = append(res, a)
	}

	return append(res, staticcheckAnalyzers()...)
}
