// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"slices"
	"strings"
	"testing"
)

func TestDispatch(t *testing.T) {
	app := New()
	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantArgs []string
		wantErr  string
	}{
		// ==========================================================
		// Implicit & Explicit Serve
		// ==========================================================
		{
			name:     "VSCodeGo_Default",
			args:     []string{},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "GlobalFlagDefaultsToServe",
			args:     []string{"-v"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "VSCodeGo_RPCTrace",
			args:     []string{"-rpc.trace"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "VSCodeGo_RPCTraceServe",
			args:     []string{"-rpc.trace", "serve"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "XTools_ServeDebug",
			args:     []string{"serve", "-debug=localhost:6060"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:    "XTools_GlobalFlagAfterServe",
			args:    []string{"serve", "-otel=http://localhost:4318"},
			wantErr: "global flag -otel must be placed before subcommand serve",
		},

		// ==========================================================
		// Non-Serve Subcommands
		// ==========================================================
		{
			name:     "OtherCases_Version",
			args:     []string{"version"},
			wantCmd:  "version",
			wantArgs: []string{},
		},
		{
			name:     "XTools_Help",
			args:     []string{"help"},
			wantCmd:  "help",
			wantArgs: []string{},
		},
		{
			name:     "XTools_References",
			args:     []string{"references", "./gopls/main.go:35:8"},
			wantCmd:  "references",
			wantArgs: []string{"./gopls/main.go:35:8"},
		},
		{
			name:     "ReferencesWithRemoteDebugStrict",
			args:     []string{"references", "-remote.debug=:0", "./gopls/main.go:35:8"},
			wantCmd:  "references",
			wantArgs: []string{"./gopls/main.go:35:8"},
		},
		{
			name:    "ModernRemoteSessionsStrict_Misplaced",
			args:    []string{"remote", "-remote=localhost:12345", "sessions"},
			wantErr: "unknown flag: -remote",
		},
		{
			name:     "ModernRemoteSessionsStrict_Valid",
			args:     []string{"remote", "sessions", "-remote=localhost:12345"},
			wantCmd:  "remote",
			wantArgs: []string{"sessions", "-remote=localhost:12345"},
		},
		{
			name:     "ModernRemoteDebugStrict_Valid",
			args:     []string{"remote", "debug", "-remote=localhost:8082", "localhost:8083"},
			wantCmd:  "remote",
			wantArgs: []string{"debug", "-remote=localhost:8082", "localhost:8083"},
		},
		{
			name:     "VSCodeGo_Vulncheck",
			args:     []string{"vulncheck", "--", "-mode=convert", "-show=color"},
			wantCmd:  "vulncheck",
			wantArgs: []string{"-mode=convert", "-show=color"},
		},
		{
			name:     "OtherCases_VerboseExecuteServe",
			args:     []string{"-v", "execute", "serve"},
			wantCmd:  "execute",
			wantArgs: []string{"serve"},
		},

		// ==========================================================
		// Edge cases
		// ==========================================================
		{
			name:     "ServeFlagWithSpaceArg",
			args:     []string{"-listen", "localhost:3000"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "LogfileWithSpaceServe",
			args:     []string{"-logfile", "serve"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "GlobalAndServeFlagsHoist",
			args:     []string{"-listen=localhost:3000", "-v"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "GlobalAndServeFlagsHoistMixed",
			args:     []string{"-listen", "localhost:3000", "-v"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "DoubleDashPositional",
			args:     []string{"--", "-v"},
			wantCmd:  "serve",
			wantArgs: []string{"-v"},
		},
		{
			name:    "UnknownFlagInServe",
			args:    []string{"-unknown"},
			wantErr: "flag provided but not defined",
		},
		{
			name:     "GlobalFlagWithSpaceArgBeforeCheck",
			args:     []string{"-otel", "http://localhost", "check", "file.go"},
			wantCmd:  "check",
			wantArgs: []string{"file.go"},
		},
		{
			name:     "ImplicitServePositional",
			args:     []string{"-listen=localhost:3000", "foo"},
			wantCmd:  "serve",
			wantArgs: []string{"foo"},
		},
		{
			name:     "RemoteFlagBeforeServeCompat",
			args:     []string{"-remote=auto", "serve"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "RemoteFlagDefaultsToServe",
			args:     []string{"-remote=auto"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "RemoteFlagsDefaultToServe",
			args:     []string{"-remote=auto", "-remote.debug=localhost:8080"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "RepeatedSubcommandFlags",
			args:     []string{"-listen", "localhost:3000", "-listen", "localhost:4000"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "BooleanFlagInlineTrue",
			args:     []string{"-v=true"},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:    "GlobalFlagTrailingEqual",
			args:    []string{"-v="},
			wantErr: "invalid boolean value",
		},
		{
			name:     "ServeFlagTrailingEqual",
			args:     []string{"-listen="},
			wantCmd:  "serve",
			wantArgs: []string{},
		},
		{
			name:     "EmptyArgumentImplicitServe",
			args:     []string{""},
			wantCmd:  "serve",
			wantArgs: []string{""},
		},
		{
			name:     "EmptyArgumentAsGlobalFlagValue",
			args:     []string{"-otel", "", "check"},
			wantCmd:  "check",
			wantArgs: []string{},
		},
		{
			name:     "RepeatedExplicitServeSubcommand",
			args:     []string{"serve", "serve"},
			wantCmd:  "serve",
			wantArgs: []string{"serve"},
		},
		{
			name:    "GlobalHelpFlagDefaultsToServe",
			args:    []string{"-help"},
			wantErr: "help requested",
		},

		// ==========================================================
		// Error Scenarios
		// ==========================================================
		{
			name:    "MissingGlobalFlagValue",
			args:    []string{"-otel"},
			wantErr: "flag needs an argument",
		},
		{
			name:    "MissingServeFlagValue",
			args:    []string{"-listen"},
			wantErr: "flag needs an argument",
		},
		{
			name:    "GlobalFlagAfterSubcommandFail",
			args:    []string{"references", "-v", "./gopls/main.go:35:8"},
			wantErr: "global flag -v must be placed before subcommand references",
		},
		{
			name:    "SubcommandFlagBeforeSubcommandFail",
			args:    []string{"-remote.debug=:0", "references", "./gopls/main.go:35:8"},
			wantErr: "belongs to subcommand but is placed before it",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Logf("> gopls %v", strings.Join(tc.args, " "))
			subApp, gotArgs, _, err := dispatch(app, tc.args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("dispatch failed: %v", err)
			}
			if subApp.Name() != tc.wantCmd {
				t.Errorf("dispatch() cmd = %v, want %v", subApp.Name(), tc.wantCmd)
			}
			if !slices.Equal(gotArgs, tc.wantArgs) {
				t.Errorf("dispatch() args = %v, want %v", gotArgs, tc.wantArgs)
			}
		})
	}
}
