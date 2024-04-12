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
)

var (
	optionsOnce    sync.Once
	defaultOptions *Options
)

// DefaultOptions is the options that are used for Gopls execution independent
// of any externally provided configuration (LSP initialization, command
// invocation, etc.).
func DefaultOptions(overrides ...func(*Options)) *Options {
	optionsOnce.Do(func() {
		var commands []string
		for _, c := range command.Commands {
			commands = append(commands, c.ID())
		}
		defaultOptions = &Options{
			ClientOptions: ClientOptions{
				InsertTextFormat:                           protocol.PlainTextTextFormat,
				PreferredContentFormat:                     protocol.Markdown,
				ConfigurationSupported:                     true,
				DynamicConfigurationSupported:              true,
				DynamicRegistrationSemanticTokensSupported: true,
				DynamicWatchedFilesSupported:               true,
				LineFoldingOnly:                            false,
				HierarchicalDocumentSymbolSupport:          true,
			},
			ServerOptions: ServerOptions{
				SupportedCodeActions: map[file.Kind]map[protocol.CodeActionKind]bool{
					file.Go: {
						protocol.SourceFixAll:          true,
						protocol.SourceOrganizeImports: true,
						protocol.QuickFix:              true,
						protocol.RefactorRewrite:       true,
						protocol.RefactorInline:        true,
						protocol.RefactorExtract:       true,
						protocol.GoDoc:                 true,
					},
					file.Mod: {
						protocol.SourceOrganizeImports: true,
						protocol.QuickFix:              true,
					},
					file.Work: {},
					file.Sum:  {},
					file.Tmpl: {},
				},
				SupportedCommands: commands,
			},
			UserOptions: UserOptions{
				BuildOptions: BuildOptions{
					ExpandWorkspaceToModule: true,
					DirectoryFilters:        []string{"-**/node_modules"},
					TemplateExtensions:      []string{},
					StandaloneTags:          []string{"ignore"},
				},
				UIOptions: UIOptions{
					DiagnosticOptions: DiagnosticOptions{
						Annotations: map[Annotation]bool{
							Bounds: true,
							Escape: true,
							Inline: true,
							Nil:    true,
						},
						Vulncheck:                 ModeVulncheckOff,
						DiagnosticsDelay:          1 * time.Second,
						DiagnosticsTrigger:        DiagnosticsOnEdit,
						AnalysisProgressReporting: true,
					},
					InlayHintOptions: InlayHintOptions{},
					DocumentationOptions: DocumentationOptions{
						HoverKind:    FullDocumentation,
						LinkTarget:   "pkg.go.dev",
						LinksInHover: true,
					},
					NavigationOptions: NavigationOptions{
						ImportShortcut: BothShortcuts,
						SymbolMatcher:  SymbolFastFuzzy,
						SymbolStyle:    DynamicSymbols,
						SymbolScope:    AllSymbolScope,
					},
					CompletionOptions: CompletionOptions{
						Matcher:                        Fuzzy,
						CompletionBudget:               100 * time.Millisecond,
						ExperimentalPostfixCompletions: true,
						CompleteFunctionCalls:          true,
					},
					Codelenses: map[string]bool{
						string(command.Generate):          true,
						string(command.RegenerateCgo):     true,
						string(command.Tidy):              true,
						string(command.GCDetails):         false,
						string(command.UpgradeDependency): true,
						string(command.Vendor):            true,
						// TODO(hyangah): enable command.RunGovulncheck.
					},
				},
			},
			InternalOptions: InternalOptions{
				CompleteUnimported:          true,
				CompletionDocumentation:     true,
				DeepCompletion:              true,
				SubdirWatchPatterns:         SubdirWatchPatternsAuto,
				ReportAnalysisProgressAfter: 5 * time.Second,
				TelemetryPrompt:             false,
				LinkifyShowMessage:          false,
				IncludeReplaceInWorkspace:   false,
				ZeroConfig:                  true,
			},
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
