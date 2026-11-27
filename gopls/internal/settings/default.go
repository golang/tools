// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"sync"
	"time"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/internal/collection/set"
)

var (
	optionsOnce    sync.Once
	defaultOptions *Options
)

// DefaultOptions is the options that are used for Gopls execution independent
// of any externally provided configuration (LSP initialization, command
// invocation, etc.).
//
// It is the source from which gopls/doc/settings.md is generated.
func DefaultOptions(overrides ...func(*Options)) *Options {
	optionsOnce.Do(func() {
		var commands []string
		for _, c := range command.Commands {
			commands = append(commands, c.String())
		}
		defaultOptions = &Options{
			InsertTextFormat:                           protocol.PlainTextTextFormat,
			PreferredContentFormat:                     protocol.Markdown,
			ConfigurationSupported:                     true,
			DynamicConfigurationSupported:              true,
			DynamicRegistrationSemanticTokensSupported: true,
			DynamicWatchedFilesSupported:               true,
			LineFoldingOnly:                            false,
			HierarchicalDocumentSymbolSupport:          true,
			ImportsSource:                              ImportsSourceGopls,
			SupportedCodeActions: map[file.Kind]set.Set[protocol.CodeActionKind]{
				file.Go: {
					// This should include specific leaves in the tree,
					// (e.g. refactor.inline.call) not generic branches
					// (e.g. refactor.inline or refactor).
					protocol.SourceFixAll:             {},
					protocol.SourceOrganizeImports:    {},
					protocol.QuickFix:                 {},
					GoAssembly:                        {},
					GoDoc:                             {},
					GoFreeSymbols:                     {},
					GoSplitPackage:                    {},
					GoplsDocFeatures:                  {},
					GoplsDragonSlayer:                 {},
					RefactorRewriteChangeQuote:        {},
					RefactorRewriteFillStruct:         {},
					RefactorRewriteFillSwitch:         {},
					RefactorRewriteImplementInterface: {},
					RefactorRewriteInvertIf:           {},
					RefactorRewriteJoinLines:          {},
					RefactorRewriteRemoveUnusedParam:  {},
					RefactorRewriteSplitLines:         {},
					RefactorInlineCall:                {},
					RefactorInlineVariable:            {},
					RefactorExtractConstant:           {},
					RefactorExtractConstantAll:        {},
					RefactorExtractFunction:           {},
					RefactorExtractMethod:             {},
					RefactorExtractVariable:           {},
					RefactorExtractVariableAll:        {},
					RefactorExtractToNewFile:          {},
					RefactorMoveDeclaration:           {},
					// Not GoTest: it must be explicit in CodeActionParams.Context.Only
				},
				file.Mod: {
					protocol.SourceOrganizeImports: {},
					protocol.QuickFix:              {},
				},
				file.Work: {},
				file.Sum:  {},
				file.Tmpl: {},
			},
			SupportedCommands:       commands,
			ExpandWorkspaceToModule: true,
			DirectoryFilters:        []string{"-**/node_modules"},
			TemplateExtensions:      []string{},
			StandaloneTags:          []string{"ignore"},
			WorkspaceFiles:          []string{},
			Annotations: map[Annotation]bool{
				Bounds: true,
				Escape: true,
				Inline: true,
				Nil:    true,
			},
			Vulncheck:                      ModeVulncheckPrompt,
			DiagnosticsDelay:               1 * time.Second,
			DiagnosticsTrigger:             DiagnosticsOnEdit,
			WatchedFileDelay:               50 * time.Millisecond,
			AnalysisProgressReporting:      true,
			Hints:                          map[InlayHint]bool{},
			HoverKind:                      FullDocumentation,
			LinkTarget:                     "pkg.go.dev",
			LinksInHover:                   LinksInHover_LinkTarget,
			ImportShortcut:                 BothShortcuts,
			SymbolMatcher:                  SymbolFastFuzzy,
			SymbolStyle:                    DynamicSymbols,
			SymbolScope:                    AllSymbolScope,
			Matcher:                        Fuzzy,
			CompletionBudget:               100 * time.Millisecond,
			ExperimentalPostfixCompletions: true,
			CompleteFunctionCalls:          true,
			SemanticTokens:                 true,
			Codelenses: map[CodeLensSource]bool{
				CodeLensGenerate:          true,
				CodeLensRegenerateCgo:     true,
				CodeLensTidy:              true,
				CodeLensUpgradeDependency: true,
				CodeLensVendor:            true,
				CodeLensRunGovulncheck:    true,
			},
			NewGoFileHeader:             true,
			RenameMovesSubpackages:      false,
			FileWatcher:                 FileWatcherOff,
			CompleteUnimported:          true,
			CompletionDocumentation:     true,
			DeepCompletion:              true,
			SubdirWatchPatterns:         SubdirWatchPatternsAuto,
			ReportAnalysisProgressAfter: 5 * time.Second,
			TelemetryPrompt:             false,
			LinkifyShowMessage:          false,
			IncludeReplaceInWorkspace:   false,
			ZeroConfig:                  true,
		}
	})
	options := defaultOptions.Clone()
	for _, override := range overrides {
		if override != nil {
			override(options)
		}
	}

	return options
}
