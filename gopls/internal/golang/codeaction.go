// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"strings"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/analysis/fillstruct"
	"golang.org/x/tools/gopls/internal/analysis/fillswitch"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/slices"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/tag"
	"golang.org/x/tools/internal/imports"
)

// CodeActions returns all code actions (edits and other commands)
// available for the selected range.
func CodeActions(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range, diagnostics []protocol.Diagnostic, want map[protocol.CodeActionKind]bool) (actions []protocol.CodeAction, _ error) {
	// Only compute quick fixes if there are any diagnostics to fix.
	wantQuickFixes := want[protocol.QuickFix] && len(diagnostics) > 0

	// Code actions requiring syntax information alone.
	if wantQuickFixes || want[protocol.SourceOrganizeImports] || want[protocol.RefactorExtract] {
		pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
		if err != nil {
			return nil, err
		}

		// Process any missing imports and pair them with the diagnostics they fix.
		if wantQuickFixes || want[protocol.SourceOrganizeImports] {
			importEdits, importEditsPerFix, err := allImportsFixes(ctx, snapshot, pgf)
			if err != nil {
				event.Error(ctx, "imports fixes", err, tag.File.Of(fh.URI().Path()))
				importEdits = nil
				importEditsPerFix = nil
			}

			// Separate this into a set of codeActions per diagnostic, where
			// each action is the addition, removal, or renaming of one import.
			if wantQuickFixes {
				for _, importFix := range importEditsPerFix {
					fixed := fixedByImportFix(importFix.fix, diagnostics)
					if len(fixed) == 0 {
						continue
					}
					actions = append(actions, protocol.CodeAction{
						Title: importFixTitle(importFix.fix),
						Kind:  protocol.QuickFix,
						Edit: &protocol.WorkspaceEdit{
							DocumentChanges: documentChanges(fh, importFix.edits),
						},
						Diagnostics: fixed,
					})
				}
			}

			// Send all of the import edits as one code action if the file is
			// being organized.
			if want[protocol.SourceOrganizeImports] && len(importEdits) > 0 {
				actions = append(actions, protocol.CodeAction{
					Title: "Organize Imports",
					Kind:  protocol.SourceOrganizeImports,
					Edit: &protocol.WorkspaceEdit{
						DocumentChanges: documentChanges(fh, importEdits),
					},
				})
			}
		}

		if want[protocol.RefactorExtract] {
			extractions, err := getExtractCodeActions(pgf, rng, snapshot.Options())
			if err != nil {
				return nil, err
			}
			actions = append(actions, extractions...)
		}
	}

	// Code actions requiring type information.
	if want[protocol.RefactorRewrite] ||
		want[protocol.RefactorInline] ||
		want[protocol.GoTest] ||
		want[protocol.GoDoc] {
		pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
		if err != nil {
			return nil, err
		}
		if want[protocol.RefactorRewrite] {
			rewrites, err := getRewriteCodeActions(ctx, pkg, snapshot, pgf, fh, rng, snapshot.Options())
			if err != nil {
				return nil, err
			}
			actions = append(actions, rewrites...)
		}

		if want[protocol.RefactorInline] {
			rewrites, err := getInlineCodeActions(pkg, pgf, rng, snapshot.Options())
			if err != nil {
				return nil, err
			}
			actions = append(actions, rewrites...)
		}

		if want[protocol.GoTest] {
			fixes, err := getGoTestCodeActions(pkg, pgf, rng)
			if err != nil {
				return nil, err
			}
			actions = append(actions, fixes...)
		}

		if want[protocol.GoDoc] {
			loc := protocol.Location{URI: pgf.URI, Range: rng}
			cmd, err := command.NewDocCommand("View package documentation", loc)
			if err != nil {
				return nil, err
			}
			actions = append(actions, protocol.CodeAction{
				Title:   cmd.Title,
				Kind:    protocol.GoDoc,
				Command: &cmd,
			})
		}
	}
	return actions, nil
}

func supportsResolveEdits(options *settings.Options) bool {
	return options.CodeActionResolveOptions != nil && slices.Contains(options.CodeActionResolveOptions, "edit")
}

func importFixTitle(fix *imports.ImportFix) string {
	var str string
	switch fix.FixType {
	case imports.AddImport:
		str = fmt.Sprintf("Add import: %s %q", fix.StmtInfo.Name, fix.StmtInfo.ImportPath)
	case imports.DeleteImport:
		str = fmt.Sprintf("Delete import: %s %q", fix.StmtInfo.Name, fix.StmtInfo.ImportPath)
	case imports.SetImportName:
		str = fmt.Sprintf("Rename import: %s %q", fix.StmtInfo.Name, fix.StmtInfo.ImportPath)
	}
	return str
}

// fixedByImportFix filters the provided slice of diagnostics to those that
// would be fixed by the provided imports fix.
func fixedByImportFix(fix *imports.ImportFix, diagnostics []protocol.Diagnostic) []protocol.Diagnostic {
	var results []protocol.Diagnostic
	for _, diagnostic := range diagnostics {
		switch {
		// "undeclared name: X" may be an unresolved import.
		case strings.HasPrefix(diagnostic.Message, "undeclared name: "):
			ident := strings.TrimPrefix(diagnostic.Message, "undeclared name: ")
			if ident == fix.IdentName {
				results = append(results, diagnostic)
			}
		// "undefined: X" may be an unresolved import at Go 1.20+.
		case strings.HasPrefix(diagnostic.Message, "undefined: "):
			ident := strings.TrimPrefix(diagnostic.Message, "undefined: ")
			if ident == fix.IdentName {
				results = append(results, diagnostic)
			}
		// "could not import: X" may be an invalid import.
		case strings.HasPrefix(diagnostic.Message, "could not import: "):
			ident := strings.TrimPrefix(diagnostic.Message, "could not import: ")
			if ident == fix.IdentName {
				results = append(results, diagnostic)
			}
		// "X imported but not used" is an unused import.
		// "X imported but not used as Y" is an unused import.
		case strings.Contains(diagnostic.Message, " imported but not used"):
			idx := strings.Index(diagnostic.Message, " imported but not used")
			importPath := diagnostic.Message[:idx]
			if importPath == fmt.Sprintf("%q", fix.StmtInfo.ImportPath) {
				results = append(results, diagnostic)
			}
		}
	}
	return results
}

// getExtractCodeActions returns any refactor.extract code actions for the selection.
func getExtractCodeActions(pgf *parsego.File, rng protocol.Range, options *settings.Options) ([]protocol.CodeAction, error) {
	if rng.Start == rng.End {
		return nil, nil
	}

	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, err
	}
	puri := pgf.URI
	var commands []protocol.Command
	if _, ok, methodOk, _ := CanExtractFunction(pgf.Tok, start, end, pgf.Src, pgf.File); ok {
		cmd, err := command.NewApplyFixCommand("Extract function", command.ApplyFixArgs{
			Fix:          fixExtractFunction,
			URI:          puri,
			Range:        rng,
			ResolveEdits: supportsResolveEdits(options),
		})
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
		if methodOk {
			cmd, err := command.NewApplyFixCommand("Extract method", command.ApplyFixArgs{
				Fix:          fixExtractMethod,
				URI:          puri,
				Range:        rng,
				ResolveEdits: supportsResolveEdits(options),
			})
			if err != nil {
				return nil, err
			}
			commands = append(commands, cmd)
		}
	}
	if _, _, ok, _ := CanExtractVariable(start, end, pgf.File); ok {
		cmd, err := command.NewApplyFixCommand("Extract variable", command.ApplyFixArgs{
			Fix:          fixExtractVariable,
			URI:          puri,
			Range:        rng,
			ResolveEdits: supportsResolveEdits(options),
		})
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}
	var actions []protocol.CodeAction
	for i := range commands {
		actions = append(actions, newCodeAction(commands[i].Title, protocol.RefactorExtract, &commands[i], nil, options))
	}
	return actions, nil
}

func newCodeAction(title string, kind protocol.CodeActionKind, cmd *protocol.Command, diagnostics []protocol.Diagnostic, options *settings.Options) protocol.CodeAction {
	action := protocol.CodeAction{
		Title:       title,
		Kind:        kind,
		Diagnostics: diagnostics,
	}
	if !supportsResolveEdits(options) {
		action.Command = cmd
	} else {
		data, err := json.Marshal(cmd)
		if err != nil {
			panic("unable to marshal")
		}
		msg := json.RawMessage(data)
		action.Data = &msg
	}
	return action
}

func getRewriteCodeActions(ctx context.Context, pkg *cache.Package, snapshot *cache.Snapshot, pgf *parsego.File, fh file.Handle, rng protocol.Range, options *settings.Options) (_ []protocol.CodeAction, rerr error) {
	// golang/go#61693: code actions were refactored to run outside of the
	// analysis framework, but as a result they lost their panic recovery.
	//
	// These code actions should never fail, but put back the panic recovery as a
	// defensive measure.
	defer func() {
		if r := recover(); r != nil {
			rerr = bug.Errorf("refactor.rewrite code actions panicked: %v", r)
		}
	}()

	var actions []protocol.CodeAction

	if canRemoveParameter(pkg, pgf, rng) {
		cmd, err := command.NewChangeSignatureCommand("remove unused parameter", command.ChangeSignatureArgs{
			RemoveParameter: protocol.Location{
				URI:   pgf.URI,
				Range: rng,
			},
			ResolveEdits: supportsResolveEdits(options),
		})
		if err != nil {
			return nil, err
		}
		actions = append(actions, newCodeAction("Refactor: remove unused parameter", protocol.RefactorRewrite, &cmd, nil, options))
	}

	if action, ok := ConvertStringLiteral(pgf, fh, rng); ok {
		actions = append(actions, action)
	}

	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, err
	}

	var commands []protocol.Command
	if _, ok, _ := CanInvertIfCondition(pgf.File, start, end); ok {
		cmd, err := command.NewApplyFixCommand("Invert 'if' condition", command.ApplyFixArgs{
			Fix:          fixInvertIfCondition,
			URI:          pgf.URI,
			Range:        rng,
			ResolveEdits: supportsResolveEdits(options),
		})
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	if msg, ok, _ := CanSplitLines(pgf.File, pkg.FileSet(), start, end); ok {
		cmd, err := command.NewApplyFixCommand(msg, command.ApplyFixArgs{
			Fix:          fixSplitLines,
			URI:          pgf.URI,
			Range:        rng,
			ResolveEdits: supportsResolveEdits(options),
		})
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	if msg, ok, _ := CanJoinLines(pgf.File, pkg.FileSet(), start, end); ok {
		cmd, err := command.NewApplyFixCommand(msg, command.ApplyFixArgs{
			Fix:          fixJoinLines,
			URI:          pgf.URI,
			Range:        rng,
			ResolveEdits: supportsResolveEdits(options),
		})
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	// N.B.: an inspector only pays for itself after ~5 passes, which means we're
	// currently not getting a good deal on this inspection.
	//
	// TODO: Consider removing the inspection after convenienceAnalyzers are removed.
	inspect := inspector.New([]*ast.File{pgf.File})
	for _, diag := range fillstruct.Diagnose(inspect, start, end, pkg.Types(), pkg.TypesInfo()) {
		rng, err := pgf.Mapper.PosRange(pgf.Tok, diag.Pos, diag.End)
		if err != nil {
			return nil, err
		}
		for _, fix := range diag.SuggestedFixes {
			cmd, err := command.NewApplyFixCommand(fix.Message, command.ApplyFixArgs{
				Fix:          diag.Category,
				URI:          pgf.URI,
				Range:        rng,
				ResolveEdits: supportsResolveEdits(options),
			})
			if err != nil {
				return nil, err
			}
			commands = append(commands, cmd)
		}
	}

	for _, diag := range fillswitch.Diagnose(inspect, start, end, pkg.Types(), pkg.TypesInfo()) {
		edits, err := suggestedFixToEdits(ctx, snapshot, pkg.FileSet(), &diag.SuggestedFixes[0])
		if err != nil {
			return nil, err
		}

		changes := []protocol.DocumentChanges{} // must be a slice
		for _, edit := range edits {
			edit := edit
			changes = append(changes, protocol.DocumentChanges{
				TextDocumentEdit: &edit,
			})
		}

		actions = append(actions, protocol.CodeAction{
			Title: diag.Message,
			Kind:  protocol.RefactorRewrite,
			Edit: &protocol.WorkspaceEdit{
				DocumentChanges: changes,
			},
		})
	}
	for i := range commands {
		actions = append(actions, newCodeAction(commands[i].Title, protocol.RefactorRewrite, &commands[i], nil, options))
	}

	return actions, nil
}

// canRemoveParameter reports whether we can remove the function parameter
// indicated by the given [start, end) range.
//
// This is true if:
//   - there are no parse or type errors, and
//   - [start, end) is contained within an unused field or parameter name
//   - ... of a non-method function declaration.
//
// (Note that the unusedparam analyzer also computes this property, but
// much more precisely, allowing it to report its findings as diagnostics.)
func canRemoveParameter(pkg *cache.Package, pgf *parsego.File, rng protocol.Range) bool {
	if perrors, terrors := pkg.ParseErrors(), pkg.TypeErrors(); len(perrors) > 0 || len(terrors) > 0 {
		return false // can't remove parameters from packages with errors
	}
	info, err := FindParam(pgf, rng)
	if err != nil {
		return false // e.g. invalid range
	}
	if info.Field == nil {
		return false // range does not span a parameter
	}
	if info.Decl.Body == nil {
		return false // external function
	}
	if len(info.Field.Names) == 0 {
		return true // no names => field is unused
	}
	if info.Name == nil {
		return false // no name is indicated
	}
	if info.Name.Name == "_" {
		return true // trivially unused
	}

	obj := pkg.TypesInfo().Defs[info.Name]
	if obj == nil {
		return false // something went wrong
	}

	used := false
	ast.Inspect(info.Decl.Body, func(node ast.Node) bool {
		if n, ok := node.(*ast.Ident); ok && pkg.TypesInfo().Uses[n] == obj {
			used = true
		}
		return !used // keep going until we find a use
	})
	return !used
}

// getInlineCodeActions returns refactor.inline actions available at the specified range.
func getInlineCodeActions(pkg *cache.Package, pgf *parsego.File, rng protocol.Range, options *settings.Options) ([]protocol.CodeAction, error) {
	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, err
	}

	// If range is within call expression, offer to inline the call.
	var commands []protocol.Command
	if _, fn, err := EnclosingStaticCall(pkg, pgf, start, end); err == nil {
		cmd, err := command.NewApplyFixCommand(fmt.Sprintf("Inline call to %s", fn.Name()), command.ApplyFixArgs{
			Fix:          fixInlineCall,
			URI:          pgf.URI,
			Range:        rng,
			ResolveEdits: supportsResolveEdits(options),
		})
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	// Convert commands to actions.
	var actions []protocol.CodeAction
	for i := range commands {
		actions = append(actions, newCodeAction(commands[i].Title, protocol.RefactorInline, &commands[i], nil, options))
	}
	return actions, nil
}

// getGoTestCodeActions returns any "run this test/benchmark" code actions for the selection.
func getGoTestCodeActions(pkg *cache.Package, pgf *parsego.File, rng protocol.Range) ([]protocol.CodeAction, error) {
	fns, err := TestsAndBenchmarks(pkg, pgf)
	if err != nil {
		return nil, err
	}

	var tests, benchmarks []string
	for _, fn := range fns.Tests {
		if !protocol.Intersect(fn.Rng, rng) {
			continue
		}
		tests = append(tests, fn.Name)
	}
	for _, fn := range fns.Benchmarks {
		if !protocol.Intersect(fn.Rng, rng) {
			continue
		}
		benchmarks = append(benchmarks, fn.Name)
	}

	if len(tests) == 0 && len(benchmarks) == 0 {
		return nil, nil
	}

	cmd, err := command.NewTestCommand("Run tests and benchmarks", pgf.URI, tests, benchmarks)
	if err != nil {
		return nil, err
	}
	return []protocol.CodeAction{{
		Title:   cmd.Title,
		Kind:    protocol.GoTest,
		Command: &cmd,
	}}, nil
}

func documentChanges(fh file.Handle, edits []protocol.TextEdit) []protocol.DocumentChanges {
	return protocol.TextEditsToDocumentChanges(fh.URI(), fh.Version(), edits)
}
