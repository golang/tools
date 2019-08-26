// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"

	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/debug"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/internal/telemetry/log"
	"golang.org/x/tools/internal/telemetry/tag"
	errors "golang.org/x/xerrors"
)

func (s *Server) initialize(ctx context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	s.stateMu.Lock()
	state := s.state
	s.stateMu.Unlock()
	if state >= serverInitializing {
		return nil, jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidRequest, "server already initialized")
	}
	s.stateMu.Lock()
	s.state = serverInitializing
	s.stateMu.Unlock()

	// TODO: Remove the option once we are certain there are no issues here.
	s.textDocumentSyncKind = protocol.Incremental
	if opts, ok := params.InitializationOptions.(map[string]interface{}); ok {
		if opt, ok := opts["noIncrementalSync"].(bool); ok && opt {
			s.textDocumentSyncKind = protocol.Full
		}

		// Check if user has enabled watching for file changes.
		s.watchFileChanges, _ = opts["watchFileChanges"].(bool)
	}

	// Default to using synopsis as a default for hover information.
	s.hoverKind = synopsisDocumentation

	s.supportedCodeActions = map[source.FileKind]map[protocol.CodeActionKind]bool{
		source.Go: {
			protocol.SourceOrganizeImports: true,
			protocol.QuickFix:              true,
		},
		source.Mod: {},
		source.Sum: {},
	}

	s.setClientCapabilities(params.Capabilities)

	folders := params.WorkspaceFolders
	if len(folders) == 0 {
		if params.RootURI != "" {
			folders = []protocol.WorkspaceFolder{{
				URI:  params.RootURI,
				Name: path.Base(params.RootURI),
			}}
		} else {
			// no folders and no root, single file mode
			//TODO(iancottrell): not sure how to do single file mode yet
			//issue: golang.org/issue/31168
			return nil, errors.Errorf("single file mode not supported yet")
		}
	}

	for _, folder := range folders {
		if err := s.addView(ctx, folder.Name, span.NewURI(folder.URI)); err != nil {
			return nil, err
		}
	}
	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			CodeActionProvider: true,
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: []string{"."},
			},
			DefinitionProvider:         true,
			DocumentFormattingProvider: true,
			DocumentSymbolProvider:     true,
			HoverProvider:              true,
			DocumentHighlightProvider:  true,
			DocumentLinkProvider:       &protocol.DocumentLinkOptions{},
			ReferencesProvider:         true,
			RenameProvider:             true,
			SignatureHelpProvider: &protocol.SignatureHelpOptions{
				TriggerCharacters: []string{"(", ","},
			},
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				Change:    s.textDocumentSyncKind,
				OpenClose: true,
				Save: &protocol.SaveOptions{
					IncludeText: false,
				},
			},
			TypeDefinitionProvider: true,
			Workspace: &struct {
				WorkspaceFolders *struct {
					Supported           bool   "json:\"supported,omitempty\""
					ChangeNotifications string "json:\"changeNotifications,omitempty\""
				} "json:\"workspaceFolders,omitempty\""
			}{
				WorkspaceFolders: &struct {
					Supported           bool   "json:\"supported,omitempty\""
					ChangeNotifications string "json:\"changeNotifications,omitempty\""
				}{
					Supported:           true,
					ChangeNotifications: "workspace/didChangeWorkspaceFolders",
				},
			},
		},
	}, nil
}

func (s *Server) setClientCapabilities(caps protocol.ClientCapabilities) {
	// Check if the client supports snippets in completion items.
	s.insertTextFormat = protocol.PlainTextTextFormat
	if caps.TextDocument.Completion.CompletionItem.SnippetSupport {
		s.insertTextFormat = protocol.SnippetTextFormat
	}
	// Check if the client supports configuration messages.
	s.configurationSupported = caps.Workspace.Configuration
	s.dynamicConfigurationSupported = caps.Workspace.DidChangeConfiguration.DynamicRegistration
	s.dynamicWatchedFilesSupported = caps.Workspace.DidChangeWatchedFiles.DynamicRegistration

	// Check which types of content format are supported by this client.
	s.preferredContentFormat = protocol.PlainText
	if len(caps.TextDocument.Hover.ContentFormat) > 0 {
		s.preferredContentFormat = caps.TextDocument.Hover.ContentFormat[0]
	}
}

func (s *Server) initialized(ctx context.Context, params *protocol.InitializedParams) error {
	s.stateMu.Lock()
	s.state = serverInitialized
	s.stateMu.Unlock()

	var registrations []protocol.Registration
	if s.configurationSupported && s.dynamicConfigurationSupported {
		registrations = append(registrations,
			protocol.Registration{
				ID:     "workspace/didChangeConfiguration",
				Method: "workspace/didChangeConfiguration",
			},
			protocol.Registration{
				ID:     "workspace/didChangeWorkspaceFolders",
				Method: "workspace/didChangeWorkspaceFolders",
			},
		)
	}

	if s.watchFileChanges && s.dynamicWatchedFilesSupported {
		registrations = append(registrations, protocol.Registration{
			ID:     "workspace/didChangeWatchedFiles",
			Method: "workspace/didChangeWatchedFiles",
			RegisterOptions: protocol.DidChangeWatchedFilesRegistrationOptions{
				Watchers: []protocol.FileSystemWatcher{{
					GlobPattern: "**/*.go",
					Kind:        float64(protocol.WatchChange),
				}},
			},
		})
	}

	if len(registrations) > 0 {
		s.client.RegisterCapability(ctx, &protocol.RegistrationParams{
			Registrations: registrations,
		})
	}

	if s.configurationSupported {
		for _, view := range s.session.Views() {
			if err := s.fetchConfig(ctx, view); err != nil {
				return err
			}
		}
	}
	buf := &bytes.Buffer{}
	debug.PrintVersionInfo(buf, true, debug.PlainText)
	log.Print(ctx, buf.String())
	return nil
}

func (s *Server) fetchConfig(ctx context.Context, view source.View) error {
	configs, err := s.client.Configuration(ctx, &protocol.ConfigurationParams{
		Items: []protocol.ConfigurationItem{{
			ScopeURI: protocol.NewURI(view.Folder()),
			Section:  "gopls",
		}, {
			ScopeURI: protocol.NewURI(view.Folder()),
			Section:  view.Name(),
		},
		},
	})
	if err != nil {
		return err
	}
	for _, config := range configs {
		if err := s.processConfig(ctx, view, config); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) processConfig(ctx context.Context, view source.View, config interface{}) error {
	// TODO: We should probably store and process more of the config.
	if config == nil {
		return nil // ignore error if you don't have a config
	}

	c, ok := config.(map[string]interface{})
	if !ok {
		return errors.Errorf("invalid config gopls type %T", config)
	}

	// Get the environment for the go/packages config.
	if env := c["env"]; env != nil {
		menv, ok := env.(map[string]interface{})
		if !ok {
			return errors.Errorf("invalid config gopls.env type %T", env)
		}
		env := view.Env()
		for k, v := range menv {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		view.SetEnv(env)
	}

	// Get the build flags for the go/packages config.
	if buildFlags := c["buildFlags"]; buildFlags != nil {
		iflags, ok := buildFlags.([]interface{})
		if !ok {
			return errors.Errorf("invalid config gopls.buildFlags type %T", buildFlags)
		}
		flags := make([]string, 0, len(iflags))
		for _, flag := range iflags {
			flags = append(flags, fmt.Sprintf("%s", flag))
		}
		view.SetBuildFlags(flags)
	}

	// Check if the user wants documentation in completion items.
	if wantCompletionDocumentation, ok := c["wantCompletionDocumentation"].(bool); ok {
		s.wantCompletionDocumentation = wantCompletionDocumentation
	}
	// Check if placeholders are enabled.
	if usePlaceholders, ok := c["usePlaceholders"].(bool); ok {
		s.usePlaceholders = usePlaceholders
	}
	// Set the hover kind.
	if hoverKind, ok := c["hoverKind"].(string); ok {
		switch hoverKind {
		case "NoDocumentation":
			s.hoverKind = noDocumentation
		case "SingleLine":
			s.hoverKind = singleLine
		case "SynopsisDocumentation":
			s.hoverKind = synopsisDocumentation
		case "FullDocumentation":
			s.hoverKind = fullDocumentation
		case "Structured":
			s.hoverKind = structured
		default:
			log.Error(ctx, "unsupported hover kind", nil, tag.Of("HoverKind", hoverKind))
			// The default value is already be set to synopsis.
		}
	}

	// Check if the user wants to see suggested fixes from go/analysis.
	if wantSuggestedFixes, ok := c["wantSuggestedFixes"].(bool); ok {
		s.wantSuggestedFixes = wantSuggestedFixes
	}

	// Check if the user has explicitly disabled any analyses.
	if disabledAnalyses, ok := c["experimentalDisabledAnalyses"].([]interface{}); ok {
		s.disabledAnalyses = make(map[string]struct{})
		for _, a := range disabledAnalyses {
			if a, ok := a.(string); ok {
				s.disabledAnalyses[a] = struct{}{}
			}
		}
	}

	// Check if deep completions are enabled.
	if useDeepCompletions, ok := c["useDeepCompletions"].(bool); ok {
		s.useDeepCompletions = useDeepCompletions
	}

	// Check if want unimported package completions.
	if wantUnimportedCompletions, ok := c["wantUnimportedCompletions"].(bool); ok {
		s.wantUnimportedCompletions = wantUnimportedCompletions
	}

	return nil
}

func (s *Server) shutdown(ctx context.Context) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state < serverInitialized {
		return jsonrpc2.NewErrorf(jsonrpc2.CodeInvalidRequest, "server not initialized")
	}
	// drop all the active views
	s.session.Shutdown(ctx)
	s.state = serverShutDown
	return nil
}

func (s *Server) exit(ctx context.Context) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state != serverShutDown {
		os.Exit(1)
	}
	os.Exit(0)
	return nil
}
