// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/appends"
	"golang.org/x/tools/go/analysis/passes/asmdecl"
	"golang.org/x/tools/go/analysis/passes/assign"
	"golang.org/x/tools/go/analysis/passes/atomic"
	"golang.org/x/tools/go/analysis/passes/atomicalign"
	"golang.org/x/tools/go/analysis/passes/bools"
	"golang.org/x/tools/go/analysis/passes/buildtag"
	"golang.org/x/tools/go/analysis/passes/cgocall"
	"golang.org/x/tools/go/analysis/passes/composite"
	"golang.org/x/tools/go/analysis/passes/copylock"
	"golang.org/x/tools/go/analysis/passes/deepequalerrors"
	"golang.org/x/tools/go/analysis/passes/defers"
	"golang.org/x/tools/go/analysis/passes/directive"
	"golang.org/x/tools/go/analysis/passes/errorsas"
	"golang.org/x/tools/go/analysis/passes/framepointer"
	"golang.org/x/tools/go/analysis/passes/httpresponse"
	"golang.org/x/tools/go/analysis/passes/ifaceassert"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/go/analysis/passes/lostcancel"
	"golang.org/x/tools/go/analysis/passes/nilfunc"
	"golang.org/x/tools/go/analysis/passes/nilness"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/passes/shadow"
	"golang.org/x/tools/go/analysis/passes/shift"
	"golang.org/x/tools/go/analysis/passes/sigchanyzer"
	"golang.org/x/tools/go/analysis/passes/slog"
	"golang.org/x/tools/go/analysis/passes/sortslice"
	"golang.org/x/tools/go/analysis/passes/stdmethods"
	"golang.org/x/tools/go/analysis/passes/stdversion"
	"golang.org/x/tools/go/analysis/passes/stringintconv"
	"golang.org/x/tools/go/analysis/passes/structtag"
	"golang.org/x/tools/go/analysis/passes/testinggoroutine"
	"golang.org/x/tools/go/analysis/passes/tests"
	"golang.org/x/tools/go/analysis/passes/timeformat"
	"golang.org/x/tools/go/analysis/passes/unmarshal"
	"golang.org/x/tools/go/analysis/passes/unreachable"
	"golang.org/x/tools/go/analysis/passes/unsafeptr"
	"golang.org/x/tools/go/analysis/passes/unusedresult"
	"golang.org/x/tools/go/analysis/passes/unusedwrite"
	"golang.org/x/tools/go/analysis/passes/waitgroup"
	"golang.org/x/tools/gopls/internal/analysis/deprecated"
	"golang.org/x/tools/gopls/internal/analysis/embeddirective"
	"golang.org/x/tools/gopls/internal/analysis/fillreturns"
	"golang.org/x/tools/gopls/internal/analysis/gofix"
	"golang.org/x/tools/gopls/internal/analysis/hostport"
	"golang.org/x/tools/gopls/internal/analysis/infertypeargs"
	"golang.org/x/tools/gopls/internal/analysis/modernize"
	"golang.org/x/tools/gopls/internal/analysis/nonewvars"
	"golang.org/x/tools/gopls/internal/analysis/noresultvalues"
	"golang.org/x/tools/gopls/internal/analysis/simplifycompositelit"
	"golang.org/x/tools/gopls/internal/analysis/simplifyrange"
	"golang.org/x/tools/gopls/internal/analysis/simplifyslice"
	"golang.org/x/tools/gopls/internal/analysis/unusedfunc"
	"golang.org/x/tools/gopls/internal/analysis/unusedparams"
	"golang.org/x/tools/gopls/internal/analysis/unusedvariable"
	"golang.org/x/tools/gopls/internal/analysis/yield"
	"golang.org/x/tools/gopls/internal/protocol"
)

// Analyzer augments a [analysis.Analyzer] with additional LSP configuration.
//
// Analyzers are immutable, since they are shared across multiple LSP sessions.
type Analyzer struct {
	analyzer    *analysis.Analyzer
	nonDefault  bool
	actionKinds []protocol.CodeActionKind
	severity    protocol.DiagnosticSeverity
	tags        []protocol.DiagnosticTag
}

// Analyzer returns the [analysis.Analyzer] that this Analyzer wraps.
func (a *Analyzer) Analyzer() *analysis.Analyzer { return a.analyzer }

// EnabledByDefault reports whether the analyzer is enabled by default for all sessions.
// This value can be configured per-analysis in user settings.
func (a *Analyzer) EnabledByDefault() bool { return !a.nonDefault }

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
// indended to write the code that way, but there is a simpler or more modern
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

// DefaultAnalyzers holds the set of Analyzers available to all gopls sessions,
// independent of build version, keyed by analyzer name.
//
// It is the source from which gopls/doc/analyzers.md is generated.
var DefaultAnalyzers = make(map[string]*Analyzer) // initialized below

func init() {
	// See [Analyzer.Severity] for guidance on setting analyzer severity below.
	analyzers := []*Analyzer{
		// The traditional vet suite:
		{analyzer: appends.Analyzer},
		{analyzer: asmdecl.Analyzer},
		{analyzer: assign.Analyzer},
		{analyzer: atomic.Analyzer},
		{analyzer: bools.Analyzer},
		{analyzer: buildtag.Analyzer},
		{analyzer: cgocall.Analyzer},
		{analyzer: composite.Analyzer},
		{analyzer: copylock.Analyzer},
		{analyzer: defers.Analyzer},
		{analyzer: deprecated.Analyzer, severity: protocol.SeverityHint, tags: []protocol.DiagnosticTag{protocol.Deprecated}},
		{analyzer: directive.Analyzer},
		{analyzer: errorsas.Analyzer},
		{analyzer: framepointer.Analyzer},
		{analyzer: httpresponse.Analyzer},
		{analyzer: ifaceassert.Analyzer},
		{analyzer: loopclosure.Analyzer},
		{analyzer: lostcancel.Analyzer},
		{analyzer: nilfunc.Analyzer},
		{analyzer: printf.Analyzer},
		{analyzer: shift.Analyzer},
		{analyzer: sigchanyzer.Analyzer},
		{analyzer: slog.Analyzer},
		{analyzer: stdmethods.Analyzer},
		{analyzer: stdversion.Analyzer},
		{analyzer: stringintconv.Analyzer},
		{analyzer: structtag.Analyzer},
		{analyzer: testinggoroutine.Analyzer},
		{analyzer: tests.Analyzer},
		{analyzer: timeformat.Analyzer},
		{analyzer: unmarshal.Analyzer},
		{analyzer: unreachable.Analyzer},
		{analyzer: unsafeptr.Analyzer},
		{analyzer: unusedresult.Analyzer},

		// not suitable for vet:
		// - some (nilness, yield) use go/ssa; see #59714.
		// - others don't meet the "frequency" criterion;
		//   see GOROOT/src/cmd/vet/README.
		{analyzer: atomicalign.Analyzer},
		{analyzer: deepequalerrors.Analyzer},
		{analyzer: nilness.Analyzer}, // uses go/ssa
		{analyzer: yield.Analyzer},   // uses go/ssa
		{analyzer: sortslice.Analyzer},
		{analyzer: embeddirective.Analyzer},
		{analyzer: waitgroup.Analyzer}, // to appear in cmd/vet@go1.25
		{analyzer: hostport.Analyzer},  // to appear in cmd/vet@go1.25

		// disabled due to high false positives
		{analyzer: shadow.Analyzer, nonDefault: true}, // very noisy
		// fieldalignment is not even off-by-default; see #67762.

		// simplifiers and modernizers
		//
		// These analyzers offer mere style fixes on correct code,
		// thus they will never appear in cmd/vet and
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
		{analyzer: gofix.Analyzer, severity: protocol.SeverityHint},
		{analyzer: infertypeargs.Analyzer, severity: protocol.SeverityInformation},
		{analyzer: unusedparams.Analyzer, severity: protocol.SeverityInformation},
		{analyzer: unusedfunc.Analyzer, severity: protocol.SeverityInformation},
		{analyzer: unusedwrite.Analyzer, severity: protocol.SeverityInformation}, // uses go/ssa
		{analyzer: modernize.Analyzer, severity: protocol.SeverityHint},

		// type-error analyzers
		// These analyzers enrich go/types errors with suggested fixes.
		// Since they exist only to attach their fixes to type errors, their
		// severity is irrelevant.
		{analyzer: fillreturns.Analyzer},
		{analyzer: nonewvars.Analyzer},
		{analyzer: noresultvalues.Analyzer},
		{analyzer: unusedvariable.Analyzer},
	}
	for _, analyzer := range analyzers {
		DefaultAnalyzers[analyzer.analyzer.Name] = analyzer
	}
}
