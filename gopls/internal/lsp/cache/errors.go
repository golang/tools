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
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/analysis/embeddirective"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/lsp/cache/metadata"
	"golang.org/x/tools/gopls/internal/lsp/command"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/internal/typesinternal"
)

// goPackagesErrorDiagnostics translates the given go/packages Error into a
// diagnostic, using the provided metadata and filesource.
//
// The slice of diagnostics may be empty.
func goPackagesErrorDiagnostics(ctx context.Context, e packages.Error, mp *metadata.Package, fs file.Source) ([]*Diagnostic, error) {
	if diag, err := parseGoListImportCycleError(ctx, e, mp, fs); err != nil {
		return nil, err
	} else if diag != nil {
		return []*Diagnostic{diag}, nil
	}

	// Parse error location and attempt to convert to protocol form.
	loc, err := func() (protocol.Location, error) {
		filename, line, col8 := parseGoListError(e, mp.LoadDir)
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
		var diags []*Diagnostic
		for _, uri := range mp.CompiledGoFiles {
			diags = append(diags, &Diagnostic{
				URI:      uri,
				Severity: protocol.SeverityError,
				Source:   ListError,
				Message:  e.Msg,
			})
		}
		return diags, nil
	}
	return []*Diagnostic{{
		URI:      loc.URI,
		Range:    loc.Range,
		Severity: protocol.SeverityError,
		Source:   ListError,
		Message:  e.Msg,
	}}, nil
}

func parseErrorDiagnostics(pkg *syntaxPackage, errList scanner.ErrorList) ([]*Diagnostic, error) {
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
	return []*Diagnostic{{
		URI:      pgf.URI,
		Range:    rng,
		Severity: protocol.SeverityError,
		Source:   ParseError,
		Message:  e.Msg,
	}}, nil
}

var importErrorRe = regexp.MustCompile(`could not import ([^\s]+)`)
var unsupportedFeatureRe = regexp.MustCompile(`.*require.* go(\d+\.\d+) or later`)

func goGetQuickFixes(moduleMode bool, uri protocol.DocumentURI, pkg string) []SuggestedFix {
	// Go get only supports module mode for now.
	if !moduleMode {
		return nil
	}
	title := fmt.Sprintf("go get package %v", pkg)
	cmd, err := command.NewGoGetPackageCommand(title, command.GoGetPackageArgs{
		URI:        uri,
		AddRequire: true,
		Pkg:        pkg,
	})
	if err != nil {
		bug.Reportf("internal error building 'go get package' fix: %v", err)
		return nil
	}
	return []SuggestedFix{SuggestedFixFromCommand(cmd, protocol.QuickFix)}
}

func editGoDirectiveQuickFix(moduleMode bool, uri protocol.DocumentURI, version string) []SuggestedFix {
	// Go mod edit only supports module mode.
	if !moduleMode {
		return nil
	}
	title := fmt.Sprintf("go mod edit -go=%s", version)
	cmd, err := command.NewEditGoDirectiveCommand(title, command.EditGoDirectiveArgs{
		URI:     uri,
		Version: version,
	})
	if err != nil {
		bug.Reportf("internal error constructing 'edit go directive' fix: %v", err)
		return nil
	}
	return []SuggestedFix{SuggestedFixFromCommand(cmd, protocol.QuickFix)}
}

// encodeDiagnostics gob-encodes the given diagnostics.
func encodeDiagnostics(srcDiags []*Diagnostic) []byte {
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
func decodeDiagnostics(data []byte) []*Diagnostic {
	var gobDiags []gobDiagnostic
	diagnosticsCodec.Decode(data, &gobDiags)
	var srcDiags []*Diagnostic
	for _, gobDiag := range gobDiags {
		var srcFixes []SuggestedFix
		for _, gobFix := range gobDiag.SuggestedFixes {
			srcFix := SuggestedFix{
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
		srcDiag := &Diagnostic{
			URI:            gobDiag.Location.URI,
			Range:          gobDiag.Location.Range,
			Severity:       gobDiag.Severity,
			Code:           gobDiag.Code,
			CodeHref:       gobDiag.CodeHref,
			Source:         DiagnosticSource(gobDiag.Source),
			Message:        gobDiag.Message,
			Tags:           gobDiag.Tags,
			Related:        srcRelated,
			SuggestedFixes: srcFixes,
		}
		srcDiags = append(srcDiags, srcDiag)
	}
	return srcDiags
}

// canFixFuncs maps an analyer to a function that determines whether or not a
// fix is possible for the given diagnostic.
//
// TODO(rfindley): clean this up.
var canFixFuncs = map[settings.Fix]func(*Diagnostic) bool{
	settings.AddEmbedImport: fixedByImportingEmbed,
}

// fixedByImportingEmbed returns true if diag can be fixed by addEmbedImport.
func fixedByImportingEmbed(diag *Diagnostic) bool {
	if diag == nil {
		return false
	}
	return diag.Message == embeddirective.MissingImportMessage
}

// canFix returns true if Analyzer.Fix can fix the Diagnostic.
//
// It returns true by default: only if the analyzer is configured explicitly to
// ignore this diagnostic does it return false.
//
// TODO(rfindley): reconcile the semantics of 'Fix' and
// 'suggestedAnalysisFixes'.
func canFix(a *settings.Analyzer, d *Diagnostic) bool {
	f, ok := canFixFuncs[a.Fix]
	if !ok {
		// See the above TODO: this doesn't make sense, but preserves pre-existing
		// semantics.
		return true
	}
	return f(d)
}

// toSourceDiagnostic converts a gobDiagnostic to "source" form.
func toSourceDiagnostic(srcAnalyzer *settings.Analyzer, gobDiag *gobDiagnostic) *Diagnostic {
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

	diag := &Diagnostic{
		URI:      gobDiag.Location.URI,
		Range:    gobDiag.Location.Range,
		Severity: severity,
		Code:     gobDiag.Code,
		CodeHref: gobDiag.CodeHref,
		Source:   DiagnosticSource(gobDiag.Source),
		Message:  gobDiag.Message,
		Related:  related,
		Tags:     srcAnalyzer.Tag,
	}
	if canFix(srcAnalyzer, diag) {
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
func onlyDeletions(fixes []SuggestedFix) bool {
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
	return BuildLink(linkTarget, "golang.org/x/tools/internal/typesinternal", code.String())
}

// BuildLink constructs a URL with the given target, path, and anchor.
func BuildLink(target, path, anchor string) string {
	link := fmt.Sprintf("https://%s/%s", target, path)
	if anchor == "" {
		return link
	}
	return link + "#" + anchor
}

func suggestedAnalysisFixes(diag *gobDiagnostic, kinds []protocol.CodeActionKind) []SuggestedFix {
	var fixes []SuggestedFix
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
			fixes = append(fixes, SuggestedFix{
				Title:      fix.Message,
				Edits:      edits,
				ActionKind: kind,
			})
		}

	}
	return fixes
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
func parseGoListImportCycleError(ctx context.Context, e packages.Error, mp *metadata.Package, fs file.Source) (*Diagnostic, error) {
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
	for _, uri := range mp.CompiledGoFiles {
		pgf, err := parseGoURI(ctx, fs, uri, ParseHeader)
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

				return &Diagnostic{
					URI:      pgf.URI,
					Range:    rng.Range(),
					Severity: protocol.SeverityError,
					Source:   ListError,
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
func parseGoURI(ctx context.Context, fs file.Source, uri protocol.DocumentURI, mode parser.Mode) (*ParsedGoFile, error) {
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
func parseModURI(ctx context.Context, fs file.Source, uri protocol.DocumentURI) (*ParsedModule, error) {
	fh, err := fs.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	return parseModImpl(ctx, fh)
}
