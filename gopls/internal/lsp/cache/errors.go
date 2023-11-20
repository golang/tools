// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

// This file defines routines to convert diagnostics from go list, go
// get, go/packages, parsing, type checking, and analysis into
// source.Diagnostic form, and suggesting quick fixes.

import (
	"context"
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/bug"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/lsp/command"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/safetoken"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/typesinternal"
)

// goPackagesErrorDiagnostics translates the given go/packages Error into a
// diagnostic, using the provided metadata and filesource.
//
// The slice of diagnostics may be empty.
func goPackagesErrorDiagnostics(ctx context.Context, e packages.Error, m *Metadata, fs file.Source) ([]*source.Diagnostic, error) {
	if diag, err := parseGoListImportCycleError(ctx, e, m, fs); err != nil {
		return nil, err
	} else if diag != nil {
		return []*source.Diagnostic{diag}, nil
	}

	// Parse error location and attempt to convert to protocol form.
	loc, err := func() (protocol.Location, error) {
		filename, line, col8 := parseGoListError(e, m.LoadDir)
		uri := protocol.URIFromPath(filename)

		fh, err := fs.ReadFile(ctx, uri)
		if err != nil {
			return protocol.Location{}, err
		}
		content, err := fh.Content()
		if err != nil {
			return protocol.Location{}, err
		}
		mapper := protocol.NewMapper(uri, content)
		posn, err := mapper.LineCol8Position(line, col8)
		if err != nil {
			return protocol.Location{}, err
		}
		return protocol.Location{
			URI: uri,
			Range: protocol.Range{
				Start: posn,
				End:   posn,
			},
		}, nil
	}()

	// TODO(rfindley): in some cases the go command outputs invalid spans, for
	// example (from TestGoListErrors):
	//
	//   package a
	//   import
	//
	// In this case, the go command will complain about a.go:2:8, which is after
	// the trailing newline but still considered to be on the second line, most
	// likely because *token.File lacks information about newline termination.
	//
	// We could do better here by handling that case.
	if err != nil {
		// Unable to parse a valid position.
		// Apply the error to all files to be safe.
		var diags []*source.Diagnostic
		for _, uri := range m.CompiledGoFiles {
			diags = append(diags, &source.Diagnostic{
				URI:      uri,
				Severity: protocol.SeverityError,
				Source:   source.ListError,
				Message:  e.Msg,
			})
		}
		return diags, nil
	}
	return []*source.Diagnostic{{
		URI:      loc.URI,
		Range:    loc.Range,
		Severity: protocol.SeverityError,
		Source:   source.ListError,
		Message:  e.Msg,
	}}, nil
}

func parseErrorDiagnostics(pkg *syntaxPackage, errList scanner.ErrorList) ([]*source.Diagnostic, error) {
	// The first parser error is likely the root cause of the problem.
	if errList.Len() <= 0 {
		return nil, fmt.Errorf("no errors in %v", errList)
	}
	e := errList[0]
	pgf, err := pkg.File(protocol.URIFromPath(e.Pos.Filename))
	if err != nil {
		return nil, err
	}
	rng, err := pgf.Mapper.OffsetRange(e.Pos.Offset, e.Pos.Offset)
	if err != nil {
		return nil, err
	}
	return []*source.Diagnostic{{
		URI:      pgf.URI,
		Range:    rng,
		Severity: protocol.SeverityError,
		Source:   source.ParseError,
		Message:  e.Msg,
	}}, nil
}

var importErrorRe = regexp.MustCompile(`could not import ([^\s]+)`)
var unsupportedFeatureRe = regexp.MustCompile(`.*require.* go(\d+\.\d+) or later`)

func typeErrorDiagnostics(moduleMode bool, linkTarget string, pkg *syntaxPackage, e extendedError) ([]*source.Diagnostic, error) {
	code, loc, err := typeErrorData(pkg, e.primary)
	if err != nil {
		return nil, err
	}
	diag := &source.Diagnostic{
		URI:      loc.URI,
		Range:    loc.Range,
		Severity: protocol.SeverityError,
		Source:   source.TypeError,
		Message:  e.primary.Msg,
	}
	if code != 0 {
		diag.Code = code.String()
		diag.CodeHref = typesCodeHref(linkTarget, code)
	}
	switch code {
	case typesinternal.UnusedVar, typesinternal.UnusedImport:
		diag.Tags = append(diag.Tags, protocol.Unnecessary)
	}

	for _, secondary := range e.secondaries {
		_, secondaryLoc, err := typeErrorData(pkg, secondary)
		if err != nil {
			// We may not be able to compute type error data in scenarios where the
			// secondary position is outside of the current package. In this case, we
			// don't want to ignore the diagnostic entirely.
			//
			// See golang/go#59005 for an example where gopls was missing diagnostics
			// due to returning an error here.
			continue
		}
		diag.Related = append(diag.Related, protocol.DiagnosticRelatedInformation{
			Location: secondaryLoc,
			Message:  secondary.Msg,
		})
	}

	if match := importErrorRe.FindStringSubmatch(e.primary.Msg); match != nil {
		diag.SuggestedFixes, err = goGetQuickFixes(moduleMode, loc.URI, match[1])
		if err != nil {
			return nil, err
		}
	}
	if match := unsupportedFeatureRe.FindStringSubmatch(e.primary.Msg); match != nil {
		diag.SuggestedFixes, err = editGoDirectiveQuickFix(moduleMode, loc.URI, match[1])
		if err != nil {
			return nil, err
		}
	}
	return []*source.Diagnostic{diag}, nil
}

func goGetQuickFixes(moduleMode bool, uri protocol.DocumentURI, pkg string) ([]source.SuggestedFix, error) {
	// Go get only supports module mode for now.
	if !moduleMode {
		return nil, nil
	}
	title := fmt.Sprintf("go get package %v", pkg)
	cmd, err := command.NewGoGetPackageCommand(title, command.GoGetPackageArgs{
		URI:        uri,
		AddRequire: true,
		Pkg:        pkg,
	})
	if err != nil {
		return nil, err
	}
	return []source.SuggestedFix{SuggestedFixFromCommand(cmd, protocol.QuickFix)}, nil
}

func editGoDirectiveQuickFix(moduleMode bool, uri protocol.DocumentURI, version string) ([]source.SuggestedFix, error) {
	// Go mod edit only supports module mode.
	if !moduleMode {
		return nil, nil
	}
	title := fmt.Sprintf("go mod edit -go=%s", version)
	cmd, err := command.NewEditGoDirectiveCommand(title, command.EditGoDirectiveArgs{
		URI:     uri,
		Version: version,
	})
	if err != nil {
		return nil, err
	}
	return []source.SuggestedFix{SuggestedFixFromCommand(cmd, protocol.QuickFix)}, nil
}

// encodeDiagnostics gob-encodes the given diagnostics.
func encodeDiagnostics(srcDiags []*source.Diagnostic) []byte {
	var gobDiags []gobDiagnostic
	for _, srcDiag := range srcDiags {
		var gobFixes []gobSuggestedFix
		for _, srcFix := range srcDiag.SuggestedFixes {
			gobFix := gobSuggestedFix{
				Message:    srcFix.Title,
				ActionKind: srcFix.ActionKind,
			}
			for uri, srcEdits := range srcFix.Edits {
				for _, srcEdit := range srcEdits {
					gobFix.TextEdits = append(gobFix.TextEdits, gobTextEdit{
						Location: protocol.Location{
							URI:   uri,
							Range: srcEdit.Range,
						},
						NewText: []byte(srcEdit.NewText),
					})
				}
			}
			if srcCmd := srcFix.Command; srcCmd != nil {
				gobFix.Command = &gobCommand{
					Title:     srcCmd.Title,
					Command:   srcCmd.Command,
					Arguments: srcCmd.Arguments,
				}
			}
			gobFixes = append(gobFixes, gobFix)
		}
		var gobRelated []gobRelatedInformation
		for _, srcRel := range srcDiag.Related {
			gobRel := gobRelatedInformation(srcRel)
			gobRelated = append(gobRelated, gobRel)
		}
		gobDiag := gobDiagnostic{
			Location: protocol.Location{
				URI:   srcDiag.URI,
				Range: srcDiag.Range,
			},
			Severity:       srcDiag.Severity,
			Code:           srcDiag.Code,
			CodeHref:       srcDiag.CodeHref,
			Source:         string(srcDiag.Source),
			Message:        srcDiag.Message,
			SuggestedFixes: gobFixes,
			Related:        gobRelated,
			Tags:           srcDiag.Tags,
		}
		gobDiags = append(gobDiags, gobDiag)
	}
	return diagnosticsCodec.Encode(gobDiags)
}

// decodeDiagnostics decodes the given gob-encoded diagnostics.
func decodeDiagnostics(data []byte) []*source.Diagnostic {
	var gobDiags []gobDiagnostic
	diagnosticsCodec.Decode(data, &gobDiags)
	var srcDiags []*source.Diagnostic
	for _, gobDiag := range gobDiags {
		var srcFixes []source.SuggestedFix
		for _, gobFix := range gobDiag.SuggestedFixes {
			srcFix := source.SuggestedFix{
				Title:      gobFix.Message,
				ActionKind: gobFix.ActionKind,
			}
			for _, gobEdit := range gobFix.TextEdits {
				if srcFix.Edits == nil {
					srcFix.Edits = make(map[protocol.DocumentURI][]protocol.TextEdit)
				}
				srcEdit := protocol.TextEdit{
					Range:   gobEdit.Location.Range,
					NewText: string(gobEdit.NewText),
				}
				uri := gobEdit.Location.URI
				srcFix.Edits[uri] = append(srcFix.Edits[uri], srcEdit)
			}
			if gobCmd := gobFix.Command; gobCmd != nil {
				srcFix.Command = &protocol.Command{
					Title:     gobCmd.Title,
					Command:   gobCmd.Command,
					Arguments: gobCmd.Arguments,
				}
			}
			srcFixes = append(srcFixes, srcFix)
		}
		var srcRelated []protocol.DiagnosticRelatedInformation
		for _, gobRel := range gobDiag.Related {
			srcRel := protocol.DiagnosticRelatedInformation(gobRel)
			srcRelated = append(srcRelated, srcRel)
		}
		srcDiag := &source.Diagnostic{
			URI:            gobDiag.Location.URI,
			Range:          gobDiag.Location.Range,
			Severity:       gobDiag.Severity,
			Code:           gobDiag.Code,
			CodeHref:       gobDiag.CodeHref,
			Source:         source.AnalyzerErrorKind(gobDiag.Source),
			Message:        gobDiag.Message,
			Tags:           gobDiag.Tags,
			Related:        srcRelated,
			SuggestedFixes: srcFixes,
		}
		srcDiags = append(srcDiags, srcDiag)
	}
	return srcDiags
}

// toSourceDiagnostic converts a gobDiagnostic to "source" form.
func toSourceDiagnostic(srcAnalyzer *settings.Analyzer, gobDiag *gobDiagnostic) *source.Diagnostic {
	var related []protocol.DiagnosticRelatedInformation
	for _, gobRelated := range gobDiag.Related {
		related = append(related, protocol.DiagnosticRelatedInformation(gobRelated))
	}

	kinds := srcAnalyzer.ActionKind
	if len(srcAnalyzer.ActionKind) == 0 {
		kinds = append(kinds, protocol.QuickFix)
	}

	severity := srcAnalyzer.Severity
	if severity == 0 {
		severity = protocol.SeverityWarning
	}

	diag := &source.Diagnostic{
		URI:      gobDiag.Location.URI,
		Range:    gobDiag.Location.Range,
		Severity: severity,
		Code:     gobDiag.Code,
		CodeHref: gobDiag.CodeHref,
		Source:   source.AnalyzerErrorKind(gobDiag.Source),
		Message:  gobDiag.Message,
		Related:  related,
		Tags:     srcAnalyzer.Tag,
	}
	if source.CanFix(srcAnalyzer, diag) {
		fixes := suggestedAnalysisFixes(gobDiag, kinds)
		if srcAnalyzer.Fix != "" {
			cmd, err := command.NewApplyFixCommand(gobDiag.Message, command.ApplyFixArgs{
				URI:   gobDiag.Location.URI,
				Range: gobDiag.Location.Range,
				Fix:   string(srcAnalyzer.Fix),
			})
			if err != nil {
				// JSON marshalling of these argument values cannot fail.
				log.Fatalf("internal error in NewApplyFixCommand: %v", err)
			}
			for _, kind := range kinds {
				fixes = append(fixes, SuggestedFixFromCommand(cmd, kind))
			}
		}
		diag.SuggestedFixes = fixes
	}

	// If the fixes only delete code, assume that the diagnostic is reporting dead code.
	if onlyDeletions(diag.SuggestedFixes) {
		diag.Tags = append(diag.Tags, protocol.Unnecessary)
	}
	return diag
}

// onlyDeletions returns true if fixes is non-empty and all of the suggested
// fixes are deletions.
func onlyDeletions(fixes []source.SuggestedFix) bool {
	for _, fix := range fixes {
		if fix.Command != nil {
			return false
		}
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

func typesCodeHref(linkTarget string, code typesinternal.ErrorCode) string {
	return source.BuildLink(linkTarget, "golang.org/x/tools/internal/typesinternal", code.String())
}

func suggestedAnalysisFixes(diag *gobDiagnostic, kinds []protocol.CodeActionKind) []source.SuggestedFix {
	var fixes []source.SuggestedFix
	for _, fix := range diag.SuggestedFixes {
		edits := make(map[protocol.DocumentURI][]protocol.TextEdit)
		for _, e := range fix.TextEdits {
			uri := e.Location.URI
			edits[uri] = append(edits[uri], protocol.TextEdit{
				Range:   e.Location.Range,
				NewText: string(e.NewText),
			})
		}
		for _, kind := range kinds {
			fixes = append(fixes, source.SuggestedFix{
				Title:      fix.Message,
				Edits:      edits,
				ActionKind: kind,
			})
		}

	}
	return fixes
}

func typeErrorData(pkg *syntaxPackage, terr types.Error) (typesinternal.ErrorCode, protocol.Location, error) {
	ecode, start, end, ok := typesinternal.ReadGo116ErrorData(terr)
	if !ok {
		start, end = terr.Pos, terr.Pos
		ecode = 0
	}
	// go/types may return invalid positions in some cases, such as
	// in errors on tokens missing from the syntax tree.
	if !start.IsValid() {
		return 0, protocol.Location{}, fmt.Errorf("type error (%q, code %d, go116=%t) without position", terr.Msg, ecode, ok)
	}
	// go/types errors retain their FileSet.
	// Sanity-check that we're using the right one.
	fset := pkg.fset
	if fset != terr.Fset {
		return 0, protocol.Location{}, bug.Errorf("wrong FileSet for type error")
	}
	posn := safetoken.StartPosition(fset, start)
	if !posn.IsValid() {
		return 0, protocol.Location{}, fmt.Errorf("position %d of type error %q (code %q) not found in FileSet", start, start, terr)
	}
	pgf, err := pkg.File(protocol.URIFromPath(posn.Filename))
	if err != nil {
		return 0, protocol.Location{}, err
	}
	if !end.IsValid() || end == start {
		end = analysisinternal.TypeErrorEndPos(fset, pgf.Src, start)
	}
	loc, err := pgf.Mapper.PosLocation(pgf.Tok, start, end)
	return ecode, loc, err
}

func parseGoListError(e packages.Error, dir string) (filename string, line, col8 int) {
	input := e.Pos
	if input == "" {
		// No position. Attempt to parse one out of a
		// go list error of the form "file:line:col:
		// message" by stripping off the message.
		input = strings.TrimSpace(e.Msg)
		if i := strings.Index(input, ": "); i >= 0 {
			input = input[:i]
		}
	}

	filename, line, col8 = splitFileLineCol(input)
	if !filepath.IsAbs(filename) {
		filename = filepath.Join(dir, filename)
	}
	return filename, line, col8
}

// splitFileLineCol splits s into "filename:line:col",
// where line and col consist of decimal digits.
func splitFileLineCol(s string) (file string, line, col8 int) {
	// Beware that the filename may contain colon on Windows.

	// stripColonDigits removes a ":%d" suffix, if any.
	stripColonDigits := func(s string) (rest string, num int) {
		if i := strings.LastIndex(s, ":"); i >= 0 {
			if v, err := strconv.ParseInt(s[i+1:], 10, 32); err == nil {
				return s[:i], int(v)
			}
		}
		return s, -1
	}

	// strip col ":%d"
	s, n1 := stripColonDigits(s)
	if n1 < 0 {
		return s, 0, 0 // "filename"
	}

	// strip line ":%d"
	s, n2 := stripColonDigits(s)
	if n2 < 0 {
		return s, n1, 0 // "filename:line"
	}

	return s, n2, n1 // "filename:line:col"
}

// parseGoListImportCycleError attempts to parse the given go/packages error as
// an import cycle, returning a diagnostic if successful.
//
// If the error is not detected as an import cycle error, it returns nil, nil.
func parseGoListImportCycleError(ctx context.Context, e packages.Error, m *Metadata, fs file.Source) (*source.Diagnostic, error) {
	re := regexp.MustCompile(`(.*): import stack: \[(.+)\]`)
	matches := re.FindStringSubmatch(strings.TrimSpace(e.Msg))
	if len(matches) < 3 {
		return nil, nil
	}
	msg := matches[1]
	importList := strings.Split(matches[2], " ")
	// Since the error is relative to the current package. The import that is causing
	// the import cycle error is the second one in the list.
	if len(importList) < 2 {
		return nil, nil
	}
	// Imports have quotation marks around them.
	circImp := strconv.Quote(importList[1])
	for _, uri := range m.CompiledGoFiles {
		pgf, err := parseGoURI(ctx, fs, uri, source.ParseHeader)
		if err != nil {
			return nil, err
		}
		// Search file imports for the import that is causing the import cycle.
		for _, imp := range pgf.File.Imports {
			if imp.Path.Value == circImp {
				rng, err := pgf.NodeMappedRange(imp)
				if err != nil {
					return nil, nil
				}

				return &source.Diagnostic{
					URI:      pgf.URI,
					Range:    rng.Range(),
					Severity: protocol.SeverityError,
					Source:   source.ListError,
					Message:  msg,
				}, nil
			}
		}
	}
	return nil, nil
}

// parseGoURI is a helper to parse the Go file at the given URI from the file
// source fs. The resulting syntax and token.File belong to an ephemeral,
// encapsulated FileSet, so this file stands only on its own: it's not suitable
// to use in a list of file of a package, for example.
//
// It returns an error if the file could not be read.
//
// TODO(rfindley): eliminate this helper.
func parseGoURI(ctx context.Context, fs file.Source, uri protocol.DocumentURI, mode parser.Mode) (*source.ParsedGoFile, error) {
	fh, err := fs.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	return parseGoImpl(ctx, token.NewFileSet(), fh, mode, false)
}

// parseModURI is a helper to parse the Mod file at the given URI from the file
// source fs.
//
// It returns an error if the file could not be read.
func parseModURI(ctx context.Context, fs file.Source, uri protocol.DocumentURI) (*source.ParsedModule, error) {
	fh, err := fs.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	return parseModImpl(ctx, fh)
}
