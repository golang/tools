// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"context"
	"fmt"
	"go/ast"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/lsp/debug/tag"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/span"
	errors "golang.org/x/xerrors"
)

type Diagnostic struct {
	Range    protocol.Range
	Message  string
	Source   string
	Severity protocol.DiagnosticSeverity
	Tags     []protocol.DiagnosticTag

	Related []RelatedInformation
}

type SuggestedFix struct {
	Title string
	Edits map[span.URI][]protocol.TextEdit
}

type RelatedInformation struct {
	URI     span.URI
	Range   protocol.Range
	Message string
}

func Diagnostics(ctx context.Context, snapshot Snapshot, ph PackageHandle, missingModules map[string]*modfile.Require, withAnalysis bool) (map[FileIdentity][]*Diagnostic, bool, error) {
	// If we are missing dependencies, it may because the user's workspace is
	// not correctly configured. Report errors, if possible.
	var warn bool
	if len(ph.MissingDependencies()) > 0 {
		warn = true
	}
	pkg, err := ph.Check(ctx)
	if err != nil {
		return nil, false, err
	}
	// If we have a package with a single file and errors about "undeclared" symbols,
	// we may have an ad-hoc package with multiple files. Show a warning message.
	// TODO(golang/go#36416): Remove this when golang.org/cl/202277 is merged.
	if len(pkg.CompiledGoFiles()) == 1 && hasUndeclaredErrors(pkg) {
		warn = true
	}

	missing := make(map[string]*modfile.Require)
	for _, imp := range pkg.Imports() {
		if req, ok := missingModules[imp.PkgPath()]; ok {
			missing[imp.PkgPath()] = req
			continue
		}
		for dep, req := range missingModules {
			// If the import is a package of the dependency, then add the package to the map, this will
			// eliminate the need to do this prefix package search on each import for each file.
			// Example:
			// import (
			//   "golang.org/x/tools/go/expect"
			//   "golang.org/x/tools/go/packages"
			// )
			// They both are related to the same module: "golang.org/x/tools"
			if req != nil && strings.HasPrefix(imp.PkgPath(), dep) {
				missing[imp.PkgPath()] = req
				break
			}
		}
	}

	// Prepare the reports we will send for the files in this package.
	reports := make(map[FileIdentity][]*Diagnostic)
	for _, fh := range pkg.CompiledGoFiles() {
		clearReports(snapshot, reports, fh.File().Identity().URI)
		if len(missing) > 0 {
			if err := missingModulesDiagnostics(ctx, snapshot, reports, missing, fh.File().Identity().URI); err != nil {
				return nil, warn, err
			}
		}
	}
	// Prepare any additional reports for the errors in this package.
	for _, e := range pkg.GetErrors() {
		// We only need to handle lower-level errors.
		if e.Kind != ListError {
			continue
		}
		// If no file is associated with the error, pick an open file from the package.
		if e.URI.Filename() == "" {
			for _, ph := range pkg.CompiledGoFiles() {
				if snapshot.IsOpen(ph.File().Identity().URI) {
					e.URI = ph.File().Identity().URI
				}
			}
		}
		clearReports(snapshot, reports, e.URI)
	}
	// Run diagnostics for the package that this URI belongs to.
	hadDiagnostics, hadTypeErrors, err := diagnostics(ctx, snapshot, reports, pkg, len(ph.MissingDependencies()) > 0)
	if err != nil {
		return nil, warn, err
	}
	if hadDiagnostics || !withAnalysis {
		return reports, warn, nil
	}
	// Exit early if the context has been canceled. This also protects us
	// from a race on Options, see golang/go#36699.
	if ctx.Err() != nil {
		return nil, warn, ctx.Err()
	}
	// If we don't have any list or parse errors, run analyses.
	analyzers := snapshot.View().Options().DefaultAnalyzers
	if hadTypeErrors {
		analyzers = snapshot.View().Options().TypeErrorAnalyzers
	}
	if err := analyses(ctx, snapshot, reports, ph, analyzers); err != nil {
		event.Error(ctx, "analyses failed", err, tag.Snapshot.Of(snapshot.ID()), tag.Package.Of(ph.ID()))
		if ctx.Err() != nil {
			return nil, warn, ctx.Err()
		}
	}
	return reports, warn, nil
}

func FileDiagnostics(ctx context.Context, snapshot Snapshot, uri span.URI) (FileIdentity, []*Diagnostic, error) {
	fh, err := snapshot.GetFile(uri)
	if err != nil {
		return FileIdentity{}, nil, err
	}
	phs, err := snapshot.PackageHandles(ctx, fh)
	if err != nil {
		return FileIdentity{}, nil, err
	}
	ph, err := NarrowestPackageHandle(phs)
	if err != nil {
		return FileIdentity{}, nil, err
	}
	reports, _, err := Diagnostics(ctx, snapshot, ph, nil, true)
	if err != nil {
		return FileIdentity{}, nil, err
	}
	diagnostics, ok := reports[fh.Identity()]
	if !ok {
		return FileIdentity{}, nil, errors.Errorf("no diagnostics for %s", uri)
	}
	return fh.Identity(), diagnostics, nil
}

type diagnosticSet struct {
	listErrors, parseErrors, typeErrors []*Diagnostic
}

func diagnostics(ctx context.Context, snapshot Snapshot, reports map[FileIdentity][]*Diagnostic, pkg Package, hasMissingDeps bool) (bool, bool, error) {
	ctx, done := event.Start(ctx, "source.diagnostics", tag.Package.Of(pkg.ID()))
	_ = ctx // circumvent SA4006
	defer done()

	diagSets := make(map[span.URI]*diagnosticSet)
	for _, e := range pkg.GetErrors() {
		diag := &Diagnostic{
			Message:  e.Message,
			Range:    e.Range,
			Severity: protocol.SeverityError,
		}
		set, ok := diagSets[e.URI]
		if !ok {
			set = &diagnosticSet{}
			diagSets[e.URI] = set
		}
		switch e.Kind {
		case ParseError:
			set.parseErrors = append(set.parseErrors, diag)
			diag.Source = "syntax"
		case TypeError:
			set.typeErrors = append(set.typeErrors, diag)
			diag.Source = "compiler"
		case ListError:
			set.listErrors = append(set.listErrors, diag)
			diag.Source = "go list"
		}
	}
	var nonEmptyDiagnostics, hasTypeErrors bool // track if we actually send non-empty diagnostics
	for uri, set := range diagSets {
		// Don't report type errors if there are parse errors or list errors.
		diags := set.typeErrors
		if len(set.parseErrors) > 0 {
			diags, nonEmptyDiagnostics = set.parseErrors, true
		} else if len(set.listErrors) > 0 {
			// Only show list errors if the package has missing dependencies.
			if hasMissingDeps {
				diags, nonEmptyDiagnostics = set.listErrors, true
			}
		} else if len(set.typeErrors) > 0 {
			hasTypeErrors = true
		}
		if err := addReports(snapshot, reports, uri, diags...); err != nil {
			return false, false, err
		}
	}
	return nonEmptyDiagnostics, hasTypeErrors, nil
}

func missingModulesDiagnostics(ctx context.Context, snapshot Snapshot, reports map[FileIdentity][]*Diagnostic, missingModules map[string]*modfile.Require, uri span.URI) error {
	if snapshot.View().Ignore(uri) || len(missingModules) == 0 {
		return nil
	}
	fh, err := snapshot.GetFile(uri)
	if err != nil {
		return err
	}
	file, _, m, _, err := snapshot.View().Session().Cache().ParseGoHandle(fh, ParseHeader).Parse(ctx)
	if err != nil {
		return err
	}
	// Make a dependency->import map to improve performance when finding missing dependencies.
	imports := make(map[string]*ast.ImportSpec)
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		if target, err := strconv.Unquote(imp.Path.Value); err == nil {
			imports[target] = imp
		}
	}
	// If the go file has 0 imports, then we do not need to check for missing dependencies.
	if len(imports) == 0 {
		return nil
	}
	if reports[fh.Identity()] == nil {
		reports[fh.Identity()] = []*Diagnostic{}
	}
	for mod, req := range missingModules {
		if req.Syntax == nil {
			continue
		}
		imp, ok := imports[mod]
		if !ok {
			continue
		}
		spn, err := span.NewRange(snapshot.View().Session().Cache().FileSet(), imp.Path.Pos(), imp.Path.End()).Span()
		if err != nil {
			return err
		}
		rng, err := m.Range(spn)
		if err != nil {
			return err
		}
		reports[fh.Identity()] = append(reports[fh.Identity()], &Diagnostic{
			Message:  fmt.Sprintf("%s is not in your go.mod file.", req.Mod.Path),
			Range:    rng,
			Source:   "go mod tidy",
			Severity: protocol.SeverityWarning,
		})
	}
	return nil
}

func analyses(ctx context.Context, snapshot Snapshot, reports map[FileIdentity][]*Diagnostic, ph PackageHandle, analyses map[string]Analyzer) error {
	var analyzers []*analysis.Analyzer
	for _, a := range analyses {
		if !a.Enabled(snapshot) {
			continue
		}
		analyzers = append(analyzers, a.Analyzer)
	}
	analysisErrors, err := snapshot.Analyze(ctx, ph.ID(), analyzers...)
	if err != nil {
		return err
	}

	// Report diagnostics and errors from root analyzers.
	for _, e := range analysisErrors {
		// This is a bit of a hack, but clients > 3.15 will be able to grey out unnecessary code.
		// If we are deleting code as part of all of our suggested fixes, assume that this is dead code.
		// TODO(golang/go#34508): Return these codes from the diagnostics themselves.
		var tags []protocol.DiagnosticTag
		if onlyDeletions(e.SuggestedFixes) {
			tags = append(tags, protocol.Unnecessary)
		}
		if err := addReports(snapshot, reports, e.URI, &Diagnostic{
			Range:    e.Range,
			Message:  e.Message,
			Source:   e.Category,
			Severity: protocol.SeverityWarning,
			Tags:     tags,
			Related:  e.Related,
		}); err != nil {
			return err
		}
	}
	return nil
}

func clearReports(snapshot Snapshot, reports map[FileIdentity][]*Diagnostic, uri span.URI) {
	if snapshot.View().Ignore(uri) {
		return
	}
	fh := snapshot.FindFile(uri)
	if fh == nil {
		return
	}
	reports[fh.Identity()] = []*Diagnostic{}
}

func addReports(snapshot Snapshot, reports map[FileIdentity][]*Diagnostic, uri span.URI, diagnostics ...*Diagnostic) error {
	if snapshot.View().Ignore(uri) {
		return nil
	}
	fh := snapshot.FindFile(uri)
	if fh == nil {
		return nil
	}
	identity := fh.Identity()
	existingDiagnostics, ok := reports[identity]
	if !ok {
		return fmt.Errorf("diagnostics for unexpected file %s", uri)
	}
	if len(diagnostics) == 1 {
		d1 := diagnostics[0]
		if _, ok := snapshot.View().Options().TypeErrorAnalyzers[d1.Source]; ok {
			for i, d2 := range existingDiagnostics {
				if r := protocol.CompareRange(d1.Range, d2.Range); r != 0 {
					continue
				}
				if d1.Message != d2.Message {
					continue
				}
				reports[identity][i].Tags = append(reports[identity][i].Tags, d1.Tags...)
			}
			return nil
		}
	}
	reports[fh.Identity()] = append(reports[fh.Identity()], diagnostics...)
	return nil
}

// onlyDeletions returns true if all of the suggested fixes are deletions.
func onlyDeletions(fixes []SuggestedFix) bool {
	for _, fix := range fixes {
		for _, edits := range fix.Edits {
			for _, edit := range edits {
				if edit.NewText != "" {
					return false
				}
				if protocol.ComparePosition(edit.Range.Start, edit.Range.End) == 0 {
					return false
				}
			}
		}
	}
	return len(fixes) > 0
}

// hasUndeclaredErrors returns true if a package has a type error
// about an undeclared symbol.
func hasUndeclaredErrors(pkg Package) bool {
	for _, err := range pkg.GetErrors() {
		if err.Kind != TypeError {
			continue
		}
		if strings.Contains(err.Message, "undeclared name:") {
			return true
		}
	}
	return false
}
