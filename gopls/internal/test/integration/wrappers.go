// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package integration

import (
	"errors"
	"os"
	"path"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
	"golang.org/x/tools/internal/xcontext"
)

// RemoveWorkspaceFile deletes a file on disk but does nothing in the
// editor. It calls t.Fatal on any error.
func (e *Env) RemoveWorkspaceFile(name string) {
	e.TB.Helper()
	if err := e.Sandbox.Workdir.RemoveFile(e.Ctx, name); err != nil {
		e.TB.Fatal(err)
	}
}

// ReadWorkspaceFile reads a file from the workspace, calling t.Fatal on any
// error.
func (e *Env) ReadWorkspaceFile(name string) string {
	e.TB.Helper()
	content, err := e.Sandbox.Workdir.ReadFile(name)
	if err != nil {
		e.TB.Fatal(err)
	}
	return string(content)
}

// WriteWorkspaceFile writes a file to disk but does nothing in the editor.
// It calls t.Fatal on any error.
func (e *Env) WriteWorkspaceFile(name, content string) {
	e.TB.Helper()
	if err := e.Sandbox.Workdir.WriteFile(e.Ctx, name, content); err != nil {
		e.TB.Fatal(err)
	}
}

// WriteWorkspaceFiles deletes a file on disk but does nothing in the
// editor. It calls t.Fatal on any error.
func (e *Env) WriteWorkspaceFiles(files map[string]string) {
	e.TB.Helper()
	if err := e.Sandbox.Workdir.WriteFiles(e.Ctx, files); err != nil {
		e.TB.Fatal(err)
	}
}

// ListFiles lists relative paths to files in the given directory.
// It calls t.Fatal on any error.
func (e *Env) ListFiles(dir string) []string {
	e.TB.Helper()
	paths, err := e.Sandbox.Workdir.ListFiles(dir)
	if err != nil {
		e.TB.Fatal(err)
	}
	return paths
}

// OpenFile opens a file in the editor, calling t.Fatal on any error.
func (e *Env) OpenFile(name string) {
	e.TB.Helper()
	if err := e.Editor.OpenFile(e.Ctx, name); err != nil {
		e.TB.Fatal(err)
	}
}

// CreateBuffer creates a buffer in the editor, calling t.Fatal on any error.
func (e *Env) CreateBuffer(name string, content string) {
	e.TB.Helper()
	if err := e.Editor.CreateBuffer(e.Ctx, name, content); err != nil {
		e.TB.Fatal(err)
	}
}

// BufferText returns the current buffer contents for the file with the given
// relative path, calling t.Fatal if the file is not open in a buffer.
func (e *Env) BufferText(name string) string {
	e.TB.Helper()
	text, ok := e.Editor.BufferText(name)
	if !ok {
		e.TB.Fatalf("buffer %q is not open", name)
	}
	return text
}

// CloseBuffer closes an editor buffer without saving, calling t.Fatal on any
// error.
func (e *Env) CloseBuffer(name string) {
	e.TB.Helper()
	if err := e.Editor.CloseBuffer(e.Ctx, name); err != nil {
		e.TB.Fatal(err)
	}
}

// EditBuffer applies edits to an editor buffer, calling t.Fatal on any error.
func (e *Env) EditBuffer(name string, edits ...protocol.TextEdit) {
	e.TB.Helper()
	if err := e.Editor.EditBuffer(e.Ctx, name, edits); err != nil {
		e.TB.Fatal(err)
	}
}

func (e *Env) SetBufferContent(name string, content string) {
	e.TB.Helper()
	if err := e.Editor.SetBufferContent(e.Ctx, name, content); err != nil {
		e.TB.Fatal(err)
	}
}

// FileContent returns the file content for name that applies to the current
// editing session: it returns the buffer content for an open file, the
// on-disk content for an unopened file, or "" for a non-existent file.
func (e *Env) FileContent(name string) string {
	e.TB.Helper()
	text, ok := e.Editor.BufferText(name)
	if ok {
		return text
	}
	content, err := e.Sandbox.Workdir.ReadFile(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ""
		} else {
			e.TB.Fatal(err)
		}
	}
	return string(content)
}

// FileContentAt returns the file content at the given location, using the
// file's mapper.
func (e *Env) FileContentAt(location protocol.Location) string {
	e.TB.Helper()
	mapper, err := e.Editor.Mapper(location.URI.Path())
	if err != nil {
		e.TB.Fatal(err)
	}
	start, end, err := mapper.RangeOffsets(location.Range)
	if err != nil {
		e.TB.Fatal(err)
	}
	return string(mapper.Content[start:end])
}

// RegexpSearch returns the starting position of the first match for re in the
// buffer specified by name, calling t.Fatal on any error. It first searches
// for the position in open buffers, then in workspace files.
func (e *Env) RegexpSearch(name, re string) protocol.Location {
	e.TB.Helper()
	loc, err := e.Editor.RegexpSearch(name, re)
	if err == fake.ErrUnknownBuffer {
		loc, err = e.Sandbox.Workdir.RegexpSearch(name, re)
	}
	if err != nil {
		e.TB.Fatalf("RegexpSearch: %v, %v for %q", name, err, re)
	}
	return loc
}

// RegexpReplace replaces the first group in the first match of regexpStr with
// the replace text, calling t.Fatal on any error.
func (e *Env) RegexpReplace(name, regexpStr, replace string) {
	e.TB.Helper()
	if err := e.Editor.RegexpReplace(e.Ctx, name, regexpStr, replace); err != nil {
		e.TB.Fatalf("RegexpReplace: %v", err)
	}
}

// SaveBuffer saves an editor buffer, calling t.Fatal on any error.
func (e *Env) SaveBuffer(name string) {
	e.TB.Helper()
	if err := e.Editor.SaveBuffer(e.Ctx, name); err != nil {
		e.TB.Fatal(err)
	}
}

func (e *Env) SaveBufferWithoutActions(name string) {
	e.TB.Helper()
	if err := e.Editor.SaveBufferWithoutActions(e.Ctx, name); err != nil {
		e.TB.Fatal(err)
	}
}

// FirstDefinition returns the first definition of the symbol at the
// selected location, calling t.Fatal on error.
func (e *Env) FirstDefinition(loc protocol.Location) protocol.Location {
	e.TB.Helper()
	locs, err := e.Editor.Definitions(e.Ctx, loc)
	if err != nil {
		e.TB.Fatal(err)
	}
	if len(locs) == 0 {
		e.TB.Fatalf("no definitions")
	}
	return locs[0]
}

// FirstTypeDefinition returns the first type definition of the symbol
// at the selected location, calling t.Fatal on error.
func (e *Env) FirstTypeDefinition(loc protocol.Location) protocol.Location {
	e.TB.Helper()
	locs, err := e.Editor.TypeDefinitions(e.Ctx, loc)
	if err != nil {
		e.TB.Fatal(err)
	}
	if len(locs) == 0 {
		e.TB.Fatalf("no type definitions")
	}
	return locs[0]
}

// FormatBuffer formats the editor buffer, calling t.Fatal on any error.
func (e *Env) FormatBuffer(name string) {
	e.TB.Helper()
	if err := e.Editor.FormatBuffer(e.Ctx, name); err != nil {
		e.TB.Fatal(err)
	}
}

// OrganizeImports processes the source.organizeImports codeAction, calling
// t.Fatal on any error.
func (e *Env) OrganizeImports(name string) {
	e.TB.Helper()
	if err := e.Editor.OrganizeImports(e.Ctx, name); err != nil {
		e.TB.Fatal(err)
	}
}

// ApplyQuickFixes processes the quickfix codeAction, calling t.Fatal on any error.
func (e *Env) ApplyQuickFixes(path string, diagnostics []protocol.Diagnostic) {
	e.TB.Helper()
	loc := e.Sandbox.Workdir.EntireFile(path)
	if err := e.Editor.ApplyQuickFixes(e.Ctx, loc, diagnostics); err != nil {
		e.TB.Fatal(err)
	}
}

// ApplyCodeAction applies the given code action, calling t.Fatal on any error.
func (e *Env) ApplyCodeAction(action protocol.CodeAction) {
	e.TB.Helper()
	if err := e.Editor.ApplyCodeAction(e.Ctx, action); err != nil {
		e.TB.Fatal(err)
	}
}

// Diagnostics returns diagnostics for the given file, calling t.Fatal on any
// error.
func (e *Env) Diagnostics(name string) []protocol.Diagnostic {
	e.TB.Helper()
	diags, err := e.Editor.Diagnostics(e.Ctx, name)
	if err != nil {
		e.TB.Fatal(err)
	}
	return diags
}

// GetQuickFixes returns the available quick fix code actions, calling t.Fatal
// on any error.
func (e *Env) GetQuickFixes(path string, diagnostics []protocol.Diagnostic) []protocol.CodeAction {
	e.TB.Helper()
	loc := e.Sandbox.Workdir.EntireFile(path)
	actions, err := e.Editor.GetQuickFixes(e.Ctx, loc, diagnostics)
	if err != nil {
		e.TB.Fatal(err)
	}
	return actions
}

// Hover in the editor, calling t.Fatal on any error.
// It may return (nil, zero) even on success.
func (e *Env) Hover(loc protocol.Location) (*protocol.MarkupContent, protocol.Location) {
	e.TB.Helper()
	c, loc, err := e.Editor.Hover(e.Ctx, loc)
	if err != nil {
		e.TB.Fatal(err)
	}
	return c, loc
}

func (e *Env) DocumentLink(name string) []protocol.DocumentLink {
	e.TB.Helper()
	links, err := e.Editor.DocumentLink(e.Ctx, name)
	if err != nil {
		e.TB.Fatal(err)
	}
	return links
}

func (e *Env) DocumentHighlight(loc protocol.Location) []protocol.DocumentHighlight {
	e.TB.Helper()
	highlights, err := e.Editor.DocumentHighlight(e.Ctx, loc)
	if err != nil {
		e.TB.Fatal(err)
	}
	return highlights
}

// RunGenerate runs "go generate" in the given dir, calling t.Fatal on any error.
// It waits for the generate command to complete and checks for file changes
// before returning.
func (e *Env) RunGenerate(dir string) {
	e.TB.Helper()
	if err := e.Editor.RunGenerate(e.Ctx, dir); err != nil {
		e.TB.Fatal(err)
	}
	e.Await(NoOutstandingWork(IgnoreTelemetryPromptWork))
	// Ideally the editor.Workspace would handle all synthetic file watching, but
	// we help it out here as we need to wait for the generate command to
	// complete before checking the filesystem.
	e.CheckForFileChanges()
}

// RunGoCommand runs the given command in the sandbox's default working
// directory.
func (e *Env) RunGoCommand(verb string, args ...string) []byte {
	e.TB.Helper()
	out, err := e.Sandbox.RunGoCommand(e.Ctx, "", verb, args, nil, true)
	if err != nil {
		e.TB.Fatal(err)
	}
	return out
}

// RunGoCommandInDir is like RunGoCommand, but executes in the given
// relative directory of the sandbox.
func (e *Env) RunGoCommandInDir(dir, verb string, args ...string) {
	e.TB.Helper()
	if _, err := e.Sandbox.RunGoCommand(e.Ctx, dir, verb, args, nil, true); err != nil {
		e.TB.Fatal(err)
	}
}

// RunGoCommandInDirWithEnv is like RunGoCommand, but executes in the given
// relative directory of the sandbox with the given additional environment variables.
func (e *Env) RunGoCommandInDirWithEnv(dir string, env []string, verb string, args ...string) {
	e.TB.Helper()
	if _, err := e.Sandbox.RunGoCommand(e.Ctx, dir, verb, args, env, true); err != nil {
		e.TB.Fatal(err)
	}
}

// GoVersion checks the version of the go command.
// It returns the X in Go 1.X.
func (e *Env) GoVersion() int {
	e.TB.Helper()
	v, err := e.Sandbox.GoVersion(e.Ctx)
	if err != nil {
		e.TB.Fatal(err)
	}
	return v
}

// DumpGoSum prints the correct go.sum contents for dir in txtar format,
// for use in creating integration tests.
func (e *Env) DumpGoSum(dir string) {
	e.TB.Helper()

	if _, err := e.Sandbox.RunGoCommand(e.Ctx, dir, "list", []string{"-mod=mod", "./..."}, nil, true); err != nil {
		e.TB.Fatal(err)
	}
	sumFile := path.Join(dir, "go.sum")
	e.TB.Log("\n\n-- " + sumFile + " --\n" + e.ReadWorkspaceFile(sumFile))
	e.TB.Fatal("see contents above")
}

// CheckForFileChanges triggers a manual poll of the workspace for any file
// changes since creation, or since last polling. It is a workaround for the
// lack of true file watching support in the fake workspace.
func (e *Env) CheckForFileChanges() {
	e.TB.Helper()
	if err := e.Sandbox.Workdir.CheckForFileChanges(e.Ctx); err != nil {
		e.TB.Fatal(err)
	}
}

// CodeLens calls textDocument/codeLens for the given path, calling t.Fatal on
// any error.
func (e *Env) CodeLens(path string) []protocol.CodeLens {
	e.TB.Helper()
	lens, err := e.Editor.CodeLens(e.Ctx, path)
	if err != nil {
		e.TB.Fatal(err)
	}
	return lens
}

// ExecuteCodeLensCommand executes the command for the code lens matching the
// given command name.
//
// result is a pointer to a variable to be populated by json.Unmarshal.
func (e *Env) ExecuteCodeLensCommand(path string, cmd command.Command, result any) {
	e.TB.Helper()
	if err := e.Editor.ExecuteCodeLensCommand(e.Ctx, path, cmd, result); err != nil {
		e.TB.Fatal(err)
	}
}

// ExecuteCommand executes the requested command in the editor, calling t.Fatal
// on any error.
//
// result is a pointer to a variable to be populated by json.Unmarshal.
func (e *Env) ExecuteCommand(params *protocol.ExecuteCommandParams, result any) {
	e.TB.Helper()
	if err := e.Editor.ExecuteCommand(e.Ctx, params, result); err != nil {
		e.TB.Fatal(err)
	}
}

// Views returns the server's views.
func (e *Env) Views() []command.View {
	var summaries []command.View
	cmd := command.NewViewsCommand("")
	e.ExecuteCommand(&protocol.ExecuteCommandParams{
		Command:   cmd.Command,
		Arguments: cmd.Arguments,
	}, &summaries)
	return summaries
}

// StartProfile starts a CPU profile with the given name, using the
// gopls.start_profile custom command. It calls t.Fatal on any error.
//
// The resulting stop function must be called to stop profiling (using the
// gopls.stop_profile custom command).
func (e *Env) StartProfile() (stop func() string) {
	// TODO(golang/go#61217): revisit the ergonomics of these command APIs.
	//
	// This would be a lot simpler if we generated params constructors.
	args, err := command.MarshalArgs(command.StartProfileArgs{})
	if err != nil {
		e.TB.Fatal(err)
	}
	params := &protocol.ExecuteCommandParams{
		Command:   command.StartProfile.String(),
		Arguments: args,
	}
	var result command.StartProfileResult
	e.ExecuteCommand(params, &result)

	return func() string {
		stopArgs, err := command.MarshalArgs(command.StopProfileArgs{})
		if err != nil {
			e.TB.Fatal(err)
		}
		stopParams := &protocol.ExecuteCommandParams{
			Command:   command.StopProfile.String(),
			Arguments: stopArgs,
		}
		var result command.StopProfileResult
		e.ExecuteCommand(stopParams, &result)
		return result.File
	}
}

// InlayHints calls textDocument/inlayHints for the given path, calling t.Fatal on
// any error.
func (e *Env) InlayHints(path string) []protocol.InlayHint {
	e.TB.Helper()
	hints, err := e.Editor.InlayHint(e.Ctx, path)
	if err != nil {
		e.TB.Fatal(err)
	}
	return hints
}

// Symbol calls workspace/symbol
func (e *Env) Symbol(query string) []protocol.SymbolInformation {
	e.TB.Helper()
	ans, err := e.Editor.Symbols(e.Ctx, query)
	if err != nil {
		e.TB.Fatal(err)
	}
	return ans
}

// References wraps Editor.References, calling t.Fatal on any error.
func (e *Env) References(loc protocol.Location) []protocol.Location {
	e.TB.Helper()
	locations, err := e.Editor.References(e.Ctx, loc)
	if err != nil {
		e.TB.Fatal(err)
	}
	return locations
}

// Rename wraps Editor.Rename, calling t.Fatal on any error.
func (e *Env) Rename(loc protocol.Location, newName string) {
	e.TB.Helper()
	if err := e.Editor.Rename(e.Ctx, loc, newName); err != nil {
		e.TB.Fatal(err)
	}
}

// Implementations wraps Editor.Implementations, calling t.Fatal on any error.
func (e *Env) Implementations(loc protocol.Location) []protocol.Location {
	e.TB.Helper()
	locations, err := e.Editor.Implementations(e.Ctx, loc)
	if err != nil {
		e.TB.Fatal(err)
	}
	return locations
}

// RenameFile wraps Editor.RenameFile, calling t.Fatal on any error.
func (e *Env) RenameFile(oldPath, newPath string) {
	e.TB.Helper()
	if err := e.Editor.RenameFile(e.Ctx, oldPath, newPath); err != nil {
		e.TB.Fatal(err)
	}
}

// SignatureHelp wraps Editor.SignatureHelp, calling t.Fatal on error
func (e *Env) SignatureHelp(loc protocol.Location) *protocol.SignatureHelp {
	e.TB.Helper()
	sighelp, err := e.Editor.SignatureHelp(e.Ctx, loc)
	if err != nil {
		e.TB.Fatal(err)
	}
	return sighelp
}

// Completion executes a completion request on the server.
func (e *Env) Completion(loc protocol.Location) *protocol.CompletionList {
	e.TB.Helper()
	completions, err := e.Editor.Completion(e.Ctx, loc)
	if err != nil {
		e.TB.Fatal(err)
	}
	return completions
}

func (e *Env) DidCreateFiles(files ...protocol.DocumentURI) {
	e.TB.Helper()
	err := e.Editor.DidCreateFiles(e.Ctx, files...)
	if err != nil {
		e.TB.Fatal(err)
	}
}

func (e *Env) SetSuggestionInsertReplaceMode(useReplaceMode bool) {
	e.TB.Helper()
	e.Editor.SetSuggestionInsertReplaceMode(e.Ctx, useReplaceMode)
}

// AcceptCompletion accepts a completion for the given item at the given
// position.
func (e *Env) AcceptCompletion(loc protocol.Location, item protocol.CompletionItem) {
	e.TB.Helper()
	if err := e.Editor.AcceptCompletion(e.Ctx, loc, item); err != nil {
		e.TB.Fatal(err)
	}
}

// CodeActionForFile calls textDocument/codeAction for the entire
// file, and calls t.Fatal if there were errors.
func (e *Env) CodeActionForFile(path string, diagnostics []protocol.Diagnostic) []protocol.CodeAction {
	return e.CodeAction(e.Sandbox.Workdir.EntireFile(path), diagnostics, protocol.CodeActionUnknownTrigger)
}

// CodeAction calls textDocument/codeAction for a selection,
// and calls t.Fatal if there were errors.
func (e *Env) CodeAction(loc protocol.Location, diagnostics []protocol.Diagnostic, trigger protocol.CodeActionTriggerKind) []protocol.CodeAction {
	e.TB.Helper()
	actions, err := e.Editor.CodeAction(e.Ctx, loc, diagnostics, trigger)
	if err != nil {
		e.TB.Fatal(err)
	}
	return actions
}

// ChangeConfiguration updates the editor config, calling t.Fatal on any error.
func (e *Env) ChangeConfiguration(newConfig fake.EditorConfig) {
	e.TB.Helper()
	if err := e.Editor.ChangeConfiguration(e.Ctx, newConfig); err != nil {
		e.TB.Fatal(err)
	}
}

// ChangeWorkspaceFolders updates the editor workspace folders, calling t.Fatal
// on any error.
func (e *Env) ChangeWorkspaceFolders(newFolders ...string) {
	e.TB.Helper()
	if err := e.Editor.ChangeWorkspaceFolders(e.Ctx, newFolders); err != nil {
		e.TB.Fatal(err)
	}
}

// SemanticTokensFull invokes textDocument/semanticTokens/full, calling t.Fatal
// on any error.
func (e *Env) SemanticTokensFull(path string) []fake.SemanticToken {
	e.TB.Helper()
	toks, err := e.Editor.SemanticTokensFull(e.Ctx, path)
	if err != nil {
		e.TB.Fatal(err)
	}
	return toks
}

// SemanticTokensRange invokes textDocument/semanticTokens/range, calling t.Fatal
// on any error.
func (e *Env) SemanticTokensRange(loc protocol.Location) []fake.SemanticToken {
	e.TB.Helper()
	toks, err := e.Editor.SemanticTokensRange(e.Ctx, loc)
	if err != nil {
		e.TB.Fatal(err)
	}
	return toks
}

// Close shuts down resources associated with the environment, calling t.Error
// on any error.
func (e *Env) Close() {
	ctx := xcontext.Detach(e.Ctx)
	if e.MCPSession != nil {
		if err := e.MCPSession.Close(); err != nil {
			e.TB.Errorf("closing MCP session: %v", err)
		}
	}
	if e.MCPServer != nil {
		e.MCPServer.Close()
	}
	if err := e.Editor.Close(ctx); err != nil {
		e.TB.Errorf("closing editor: %v", err)
	}
	if err := e.Sandbox.Close(); err != nil {
		e.TB.Errorf("cleaning up sandbox: %v", err)
	}
}
