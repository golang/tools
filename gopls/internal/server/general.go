// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

// This file defines server methods related to initialization,
// options, shutdown, and exit.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/telemetry/counter"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/debug"
	debuglog "golang.org/x/tools/gopls/internal/debug/log"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/goversion"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/jsonrpc2"
)

func (s *server) Initialize(ctx context.Context, params *protocol.ParamInitialize) (*protocol.InitializeResult, error) {
	ctx, done := event.Start(ctx, "lsp.Server.initialize")
	defer done()

	var clientName string
	if params != nil && params.ClientInfo != nil {
		clientName = params.ClientInfo.Name
	}
	recordClientInfo(clientName)

	s.stateMu.Lock()
	if s.state >= serverInitializing {
		defer s.stateMu.Unlock()
		return nil, fmt.Errorf("%w: initialize called while server in %v state", jsonrpc2.ErrInvalidRequest, s.state)
	}
	s.state = serverInitializing
	s.stateMu.Unlock()

	// For uniqueness, use the gopls PID rather than params.ProcessID (the client
	// pid). Some clients might start multiple gopls servers, though they
	// probably shouldn't.
	pid := os.Getpid()
	s.tempDir = filepath.Join(os.TempDir(), fmt.Sprintf("gopls-%d.%s", pid, s.session.ID()))
	err := os.Mkdir(s.tempDir, 0700)
	if err != nil {
		// MkdirTemp could fail due to permissions issues. This is a problem with
		// the user's environment, but should not block gopls otherwise behaving.
		// All usage of s.tempDir should be predicated on having a non-empty
		// s.tempDir.
		event.Error(ctx, "creating temp dir", err)
		s.tempDir = ""
	}
	s.progress.SetSupportsWorkDoneProgress(params.Capabilities.Window.WorkDoneProgress)

	options := s.Options().Clone()
	// TODO(rfindley): eliminate this defer.
	defer func() { s.SetOptions(options) }()

	s.handleOptionErrors(ctx, options.Set(params.InitializationOptions))
	options.ForClientCapabilities(params.ClientInfo, params.Capabilities)

	if options.ShowBugReports {
		// Report the next bug that occurs on the server.
		bug.Handle(func(b bug.Bug) {
			msg := &protocol.ShowMessageParams{
				Type:    protocol.Error,
				Message: fmt.Sprintf("A bug occurred on the server: %s\nLocation:%s", b.Description, b.Key),
			}
			go s.eventuallyShowMessage(context.Background(), msg)
		})
	}

	folders := params.WorkspaceFolders
	if len(folders) == 0 {
		if params.RootURI != "" {
			folders = []protocol.WorkspaceFolder{{
				URI:  string(params.RootURI),
				Name: path.Base(params.RootURI.Path()),
			}}
		}
	}
	s.pendingFolders = append(s.pendingFolders, folders...)

	var codeActionProvider interface{} = true
	if ca := params.Capabilities.TextDocument.CodeAction; len(ca.CodeActionLiteralSupport.CodeActionKind.ValueSet) > 0 {
		// If the client has specified CodeActionLiteralSupport,
		// send the code actions we support.
		//
		// Using CodeActionOptions is only valid if codeActionLiteralSupport is set.
		codeActionProvider = &protocol.CodeActionOptions{
			CodeActionKinds: s.getSupportedCodeActions(),
			ResolveProvider: true,
		}
	}

	var diagnosticProvider *protocol.Or_ServerCapabilities_diagnosticProvider
	if options.PullDiagnostics {
		diagnosticProvider = &protocol.Or_ServerCapabilities_diagnosticProvider{
			Value: protocol.DiagnosticOptions{
				InterFileDependencies: true,
				WorkspaceDiagnostics:  false, // we don't support workspace/diagnostic
			},
		}
	}

	var renameOpts interface{} = true
	if r := params.Capabilities.TextDocument.Rename; r != nil && r.PrepareSupport {
		renameOpts = protocol.RenameOptions{
			PrepareProvider: r.PrepareSupport,
		}
	}

	versionInfo := debug.VersionInfo()

	goplsVersion, err := json.Marshal(versionInfo)
	if err != nil {
		return nil, err
	}

	return &protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			CallHierarchyProvider: &protocol.Or_ServerCapabilities_callHierarchyProvider{Value: true},
			CodeActionProvider:    codeActionProvider,
			CodeLensProvider:      &protocol.CodeLensOptions{}, // must be non-nil to enable the code lens capability
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: []string{"."},
			},
			DefinitionProvider:         &protocol.Or_ServerCapabilities_definitionProvider{Value: true},
			TypeDefinitionProvider:     &protocol.Or_ServerCapabilities_typeDefinitionProvider{Value: true},
			ImplementationProvider:     &protocol.Or_ServerCapabilities_implementationProvider{Value: true},
			DocumentFormattingProvider: &protocol.Or_ServerCapabilities_documentFormattingProvider{Value: true},
			DocumentSymbolProvider:     &protocol.Or_ServerCapabilities_documentSymbolProvider{Value: true},
			WorkspaceSymbolProvider:    &protocol.Or_ServerCapabilities_workspaceSymbolProvider{Value: true},
			ExecuteCommandProvider: &protocol.ExecuteCommandOptions{
				Commands: protocol.NonNilSlice(options.SupportedCommands),
			},
			FoldingRangeProvider:      &protocol.Or_ServerCapabilities_foldingRangeProvider{Value: true},
			HoverProvider:             &protocol.Or_ServerCapabilities_hoverProvider{Value: true},
			DocumentHighlightProvider: &protocol.Or_ServerCapabilities_documentHighlightProvider{Value: true},
			DocumentLinkProvider:      &protocol.DocumentLinkOptions{},
			InlayHintProvider:         protocol.InlayHintOptions{},
			DiagnosticProvider:        diagnosticProvider,
			ReferencesProvider:        &protocol.Or_ServerCapabilities_referencesProvider{Value: true},
			RenameProvider:            renameOpts,
			SelectionRangeProvider:    &protocol.Or_ServerCapabilities_selectionRangeProvider{Value: true},
			SemanticTokensProvider: protocol.SemanticTokensOptions{
				Range: &protocol.Or_SemanticTokensOptions_range{Value: true},
				Full:  &protocol.Or_SemanticTokensOptions_full{Value: true},
				Legend: protocol.SemanticTokensLegend{
					TokenTypes:     protocol.NonNilSlice(options.SemanticTypes),
					TokenModifiers: protocol.NonNilSlice(options.SemanticMods),
				},
			},
			SignatureHelpProvider: &protocol.SignatureHelpOptions{
				TriggerCharacters: []string{"(", ","},
			},
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				Change:    protocol.Incremental,
				OpenClose: true,
				Save: &protocol.SaveOptions{
					IncludeText: false,
				},
			},
			Workspace: &protocol.WorkspaceOptions{
				WorkspaceFolders: &protocol.WorkspaceFolders5Gn{
					Supported:           true,
					ChangeNotifications: "workspace/didChangeWorkspaceFolders",
				},
			},
		},
		ServerInfo: &protocol.ServerInfo{
			Name:    "gopls",
			Version: string(goplsVersion),
		},
	}, nil
}

func (s *server) Initialized(ctx context.Context, params *protocol.InitializedParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.initialized")
	defer done()

	s.stateMu.Lock()
	if s.state >= serverInitialized {
		defer s.stateMu.Unlock()
		return fmt.Errorf("%w: initialized called while server in %v state", jsonrpc2.ErrInvalidRequest, s.state)
	}
	s.state = serverInitialized
	s.stateMu.Unlock()

	for _, not := range s.notifications {
		s.client.ShowMessage(ctx, not)
	}
	s.notifications = nil

	s.addFolders(ctx, s.pendingFolders)

	s.pendingFolders = nil
	s.checkViewGoVersions()

	var registrations []protocol.Registration
	options := s.Options()
	if options.ConfigurationSupported && options.DynamicConfigurationSupported {
		registrations = append(registrations, protocol.Registration{
			ID:     "workspace/didChangeConfiguration",
			Method: "workspace/didChangeConfiguration",
		})
	}
	if len(registrations) > 0 {
		if err := s.client.RegisterCapability(ctx, &protocol.RegistrationParams{
			Registrations: registrations,
		}); err != nil {
			return err
		}
	}

	// Ask (maybe) about enabling telemetry. Do this asynchronously, as it's OK
	// for users to ignore or dismiss the question.
	go s.maybePromptForTelemetry(ctx, options.TelemetryPrompt)

	return nil
}

// checkViewGoVersions checks whether any Go version used by a view is too old,
// raising a showMessage notification if so.
//
// It should be called after views change.
func (s *server) checkViewGoVersions() {
	oldestVersion, fromBuild := go1Point(), true
	for _, view := range s.session.Views() {
		viewVersion := view.GoVersion()
		if oldestVersion == -1 || viewVersion < oldestVersion {
			oldestVersion, fromBuild = viewVersion, false
		}
		if viewVersion >= 0 {
			counter.Inc(fmt.Sprintf("gopls/goversion:1.%d", viewVersion))
		}
	}

	if msg, isError := goversion.Message(oldestVersion, fromBuild); msg != "" {
		mType := protocol.Warning
		if isError {
			mType = protocol.Error
		}
		s.eventuallyShowMessage(context.Background(), &protocol.ShowMessageParams{
			Type:    mType,
			Message: msg,
		})
	}
}

// go1Point returns the x in Go 1.x. If an error occurs extracting the go
// version, it returns -1.
//
// Copied from the testenv package.
func go1Point() int {
	for i := len(build.Default.ReleaseTags) - 1; i >= 0; i-- {
		var version int
		if _, err := fmt.Sscanf(build.Default.ReleaseTags[i], "go1.%d", &version); err != nil {
			continue
		}
		return version
	}
	return -1
}

// addFolders adds the specified list of "folders" (that's Windows for
// directories) to the session. It does not return an error, though it
// may report an error to the client over LSP if one or more folders
// had problems, for example, folders with unsupported file system.
func (s *server) addFolders(ctx context.Context, folders []protocol.WorkspaceFolder) {
	originalViews := len(s.session.Views())
	viewErrors := make(map[protocol.URI]error)

	// Skip non-'file' scheme, or invalid workspace folders,
	// and log them form error reports.
	// VS Code's file system API
	// (https://code.visualstudio.com/api/references/vscode-api#FileSystem)
	// allows extension to define their own schemes and register
	// them with the workspace. We've seen gitlens://, decompileFs://, etc
	// but the list can grow over time.
	var filtered []protocol.WorkspaceFolder
	for _, f := range folders {
		if _, err := protocol.ParseDocumentURI(f.URI); err != nil {
			debuglog.Warning.Logf(ctx, "skip adding virtual folder %q - invalid folder URI: %v", f.Name, err)
			continue
		}
		filtered = append(filtered, f)
	}
	folders = filtered

	var ndiagnose sync.WaitGroup // number of unfinished diagnose calls
	if s.Options().VerboseWorkDoneProgress {
		work := s.progress.Start(ctx, DiagnosticWorkTitle(FromInitialWorkspaceLoad), "Calculating diagnostics for initial workspace load...", nil, nil)
		defer func() {
			go func() {
				ndiagnose.Wait()
				work.End(ctx, "Done.")
			}()
		}()
	}
	// Only one view gets to have a workspace.
	var nsnapshots sync.WaitGroup // number of unfinished snapshot initializations
	for _, folder := range folders {
		uri, err := protocol.ParseDocumentURI(folder.URI)
		if err != nil {
			viewErrors[folder.URI] = fmt.Errorf("invalid folder URI: %v", err)
			continue
		}
		work := s.progress.Start(ctx, "Setting up workspace", "Loading packages...", nil, nil)
		snapshot, release, err := s.addView(ctx, folder.Name, uri)
		if err != nil {
			if err == cache.ErrViewExists {
				continue
			}
			viewErrors[folder.URI] = err
			work.End(ctx, fmt.Sprintf("Error loading packages: %s", err))
			continue
		}
		// Inv: release() must be called once.

		// Initialize snapshot asynchronously.
		initialized := make(chan struct{})
		nsnapshots.Add(1)
		go func() {
			snapshot.AwaitInitialized(ctx)
			work.End(ctx, "Finished loading packages.")
			nsnapshots.Done()
			close(initialized) // signal
		}()

		// Diagnose the newly created view asynchronously.
		ndiagnose.Add(1)
		go func() {
			s.diagnoseSnapshot(snapshot.BackgroundContext(), snapshot, nil, 0)
			<-initialized
			release()
			ndiagnose.Done()
		}()
	}

	// Wait for snapshots to be initialized so that all files are known.
	// (We don't need to wait for diagnosis to finish.)
	nsnapshots.Wait()

	// Register for file watching notifications, if they are supported.
	if err := s.updateWatchedDirectories(ctx); err != nil {
		event.Error(ctx, "failed to register for file watching notifications", err)
	}

	// Report any errors using the protocol.
	if len(viewErrors) > 0 {
		errMsg := fmt.Sprintf("Error loading workspace folders (expected %v, got %v)\n", len(folders), len(s.session.Views())-originalViews)
		for uri, err := range viewErrors {
			errMsg += fmt.Sprintf("failed to load view for %s: %v\n", uri, err)
		}
		showMessage(ctx, s.client, protocol.Error, errMsg)
	}
}

// updateWatchedDirectories compares the current set of directories to watch
// with the previously registered set of directories. If the set of directories
// has changed, we unregister and re-register for file watching notifications.
// updatedSnapshots is the set of snapshots that have been updated.
func (s *server) updateWatchedDirectories(ctx context.Context) error {
	patterns := s.session.FileWatchingGlobPatterns(ctx)

	s.watchedGlobPatternsMu.Lock()
	defer s.watchedGlobPatternsMu.Unlock()

	// Nothing to do if the set of workspace directories is unchanged.
	if moremaps.SameKeys(s.watchedGlobPatterns, patterns) {
		return nil
	}

	// If the set of directories to watch has changed, register the updates and
	// unregister the previously watched directories. This ordering avoids a
	// period where no files are being watched. Still, if a user makes on-disk
	// changes before these updates are complete, we may miss them for the new
	// directories.
	prevID := s.watchRegistrationCount - 1
	if err := s.registerWatchedDirectoriesLocked(ctx, patterns); err != nil {
		return err
	}
	if prevID >= 0 {
		return s.client.UnregisterCapability(ctx, &protocol.UnregistrationParams{
			Unregisterations: []protocol.Unregistration{{
				ID:     watchedFilesCapabilityID(prevID),
				Method: "workspace/didChangeWatchedFiles",
			}},
		})
	}
	return nil
}

func watchedFilesCapabilityID(id int) string {
	return fmt.Sprintf("workspace/didChangeWatchedFiles-%d", id)
}

// registerWatchedDirectoriesLocked sends the workspace/didChangeWatchedFiles
// registrations to the client and updates s.watchedDirectories.
// The caller must not subsequently mutate patterns.
func (s *server) registerWatchedDirectoriesLocked(ctx context.Context, patterns map[protocol.RelativePattern]unit) error {
	if !s.Options().DynamicWatchedFilesSupported {
		return nil
	}

	supportsRelativePatterns := s.Options().RelativePatternsSupported

	s.watchedGlobPatterns = patterns
	watchers := make([]protocol.FileSystemWatcher, 0, len(patterns)) // must be a slice
	val := protocol.WatchChange | protocol.WatchDelete | protocol.WatchCreate
	for pattern := range patterns {
		var value any
		if supportsRelativePatterns && pattern.BaseURI != "" {
			value = pattern
		} else {
			p := pattern.Pattern
			if pattern.BaseURI != "" {
				p = path.Join(filepath.ToSlash(pattern.BaseURI.Path()), p)
			}
			value = p
		}
		watchers = append(watchers, protocol.FileSystemWatcher{
			GlobPattern: protocol.GlobPattern{Value: value},
			Kind:        &val,
		})
	}

	if err := s.client.RegisterCapability(ctx, &protocol.RegistrationParams{
		Registrations: []protocol.Registration{{
			ID:     watchedFilesCapabilityID(s.watchRegistrationCount),
			Method: "workspace/didChangeWatchedFiles",
			RegisterOptions: protocol.DidChangeWatchedFilesRegistrationOptions{
				Watchers: watchers,
			},
		}},
	}); err != nil {
		return err
	}
	s.watchRegistrationCount++
	return nil
}

// Options returns the current server options.
//
// The caller must not modify the result.
func (s *server) Options() *settings.Options {
	s.optionsMu.Lock()
	defer s.optionsMu.Unlock()
	return s.options
}

// SetOptions sets the current server options.
//
// The caller must not subsequently modify the options.
func (s *server) SetOptions(opts *settings.Options) {
	s.optionsMu.Lock()
	defer s.optionsMu.Unlock()
	s.options = opts
}

func (s *server) newFolder(ctx context.Context, folder protocol.DocumentURI, name string, opts *settings.Options) (*cache.Folder, error) {
	env, err := cache.FetchGoEnv(ctx, folder, opts)
	if err != nil {
		return nil, err
	}

	// Increment folder counters.
	switch {
	case env.GOTOOLCHAIN == "auto" || strings.Contains(env.GOTOOLCHAIN, "+auto"):
		counter.Inc("gopls/gotoolchain:auto")
	case env.GOTOOLCHAIN == "path" || strings.Contains(env.GOTOOLCHAIN, "+path"):
		counter.Inc("gopls/gotoolchain:path")
	case env.GOTOOLCHAIN == "local": // local+auto and local+path handled above
		counter.Inc("gopls/gotoolchain:local")
	default:
		counter.Inc("gopls/gotoolchain:other")
	}

	// Record whether a driver is in use so that it appears in the
	// user's telemetry upload. Although we can't correlate the
	// driver information with the crash or bug.Report at the
	// granularity of the process instance, users that use a
	// driver tend to do so most of the time, so we'll get a
	// strong clue. See #60890 for an example of an issue where
	// this information would have been helpful.
	if env.EffectiveGOPACKAGESDRIVER != "" {
		counter.Inc("gopls/gopackagesdriver")
	}

	return &cache.Folder{
		Dir:     folder,
		Name:    name,
		Options: opts,
		Env:     *env,
	}, nil
}

// fetchFolderOptions makes a workspace/configuration request for the given
// folder, and populates options with the result.
//
// If folder is "", fetchFolderOptions makes an unscoped request.
func (s *server) fetchFolderOptions(ctx context.Context, folder protocol.DocumentURI) (*settings.Options, error) {
	opts := s.Options()
	if !opts.ConfigurationSupported {
		return opts, nil
	}
	var scopeURI *string
	if folder != "" {
		scope := string(folder)
		scopeURI = &scope
	}
	configs, err := s.client.Configuration(ctx, &protocol.ParamConfiguration{
		Items: []protocol.ConfigurationItem{{
			ScopeURI: scopeURI,
			Section:  "gopls",
		}},
	},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace configuration from client (%s): %v", folder, err)
	}

	opts = opts.Clone()
	for _, config := range configs {
		s.handleOptionErrors(ctx, opts.Set(config))
	}
	return opts, nil
}

func (s *server) eventuallyShowMessage(ctx context.Context, msg *protocol.ShowMessageParams) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state == serverInitialized {
		_ = s.client.ShowMessage(ctx, msg) // ignore error
	}
	s.notifications = append(s.notifications, msg)
}

func (s *server) handleOptionErrors(ctx context.Context, optionErrors []error) {
	var warnings, errs []string
	for _, err := range optionErrors {
		if err == nil {
			panic("nil error passed to handleOptionErrors")
		}
		if errors.Is(err, new(settings.SoftError)) {
			warnings = append(warnings, err.Error())
		} else {
			errs = append(errs, err.Error())
		}
	}

	// Sort messages, but put errors first.
	//
	// Having stable content for the message allows clients to de-duplicate. This
	// matters because we may send duplicate warnings for clients that support
	// dynamic configuration: one for the initial settings, and then more for the
	// individual viewsettings.
	var msgs []string
	msgType := protocol.Warning
	if len(errs) > 0 {
		msgType = protocol.Error
		sort.Strings(errs)
		msgs = append(msgs, errs...)
	}
	if len(warnings) > 0 {
		sort.Strings(warnings)
		msgs = append(msgs, warnings...)
	}

	if len(msgs) > 0 {
		// Settings
		combined := "Invalid settings: " + strings.Join(msgs, "; ")
		params := &protocol.ShowMessageParams{
			Type:    msgType,
			Message: combined,
		}
		s.eventuallyShowMessage(ctx, params)
	}
}

// fileOf returns the file for a given URI and its snapshot.
// On success, the returned function must be called to release the snapshot.
func (s *server) fileOf(ctx context.Context, uri protocol.DocumentURI) (file.Handle, *cache.Snapshot, func(), error) {
	snapshot, release, err := s.session.SnapshotOf(ctx, uri)
	if err != nil {
		return nil, nil, nil, err
	}
	fh, err := snapshot.ReadFile(ctx, uri)
	if err != nil {
		release()
		return nil, nil, nil, err
	}
	return fh, snapshot, release, nil
}

// shutdown implements the 'shutdown' LSP handler. It releases resources
// associated with the server and waits for all ongoing work to complete.
func (s *server) Shutdown(ctx context.Context) error {
	ctx, done := event.Start(ctx, "lsp.Server.shutdown")
	defer done()

	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state < serverInitialized {
		event.Log(ctx, "server shutdown without initialization")
	}
	if s.state != serverShutDown {
		// Wait for the webserver (if any) to finish.
		if s.web != nil {
			s.web.server.Shutdown(ctx)
		}

		// drop all the active views
		s.session.Shutdown(ctx)
		s.state = serverShutDown
		if s.tempDir != "" {
			if err := os.RemoveAll(s.tempDir); err != nil {
				event.Error(ctx, "removing temp dir", err)
			}
		}
	}
	return nil
}

func (s *server) Exit(ctx context.Context) error {
	ctx, done := event.Start(ctx, "lsp.Server.exit")
	defer done()

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	s.client.Close()

	if s.state != serverShutDown {
		// TODO: We should be able to do better than this.
		os.Exit(1)
	}
	// We don't terminate the process on a normal exit, we just allow it to
	// close naturally if needed after the connection is closed.
	return nil
}

// recordClientInfo records gopls client info.
func recordClientInfo(clientName string) {
	key := "gopls/client:other"
	switch clientName {
	case "Visual Studio Code":
		key = "gopls/client:vscode"
	case "Visual Studio Code - Insiders":
		key = "gopls/client:vscode-insiders"
	case "VSCodium":
		key = "gopls/client:vscodium"
	case "code-server":
		// https://github.com/coder/code-server/blob/3cb92edc76ecc2cfa5809205897d93d4379b16a6/ci/build/build-vscode.sh#L19
		key = "gopls/client:code-server"
	case "Eglot":
		// https://lists.gnu.org/archive/html/bug-gnu-emacs/2023-03/msg00954.html
		key = "gopls/client:eglot"
	case "govim":
		// https://github.com/govim/govim/pull/1189
		key = "gopls/client:govim"
	case "Neovim":
		// https://github.com/neovim/neovim/blob/42333ea98dfcd2994ee128a3467dfe68205154cd/runtime/lua/vim/lsp.lua#L1361
		key = "gopls/client:neovim"
	case "coc.nvim":
		// https://github.com/neoclide/coc.nvim/blob/3dc6153a85ed0f185abec1deb972a66af3fbbfb4/src/language-client/client.ts#L994
		key = "gopls/client:coc.nvim"
	case "Sublime Text LSP":
		// https://github.com/sublimelsp/LSP/blob/e608f878e7e9dd34aabe4ff0462540fadcd88fcc/plugin/core/sessions.py#L493
		key = "gopls/client:sublimetext"
	default:
		// Accumulate at least a local counter for an unknown
		// client name, but also fall through to count it as
		// ":other" for collection.
		if clientName != "" {
			counter.New(fmt.Sprintf("gopls/client-other:%s", clientName)).Inc()
		}
	}
	counter.Inc(key)
}
