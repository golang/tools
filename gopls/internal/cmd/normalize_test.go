// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"slices"
	"strings"
	"testing"
)

// TODO(adonovan): turn this into an integration test. Tests of
// internal helper functions are not robust nor do they reflect actual
// application behavior when state (e.g. flags) is involved.
//
// Also, check that the correct usage message is generated.
func TestNormalize(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantCmd        string
		wantGlobalArgs []string
		wantArgs       []string
		wantErr        string
	}{
		// ==========================================================
		// Implicit & Explicit Serve
		// ==========================================================
		{
			name:    "VSCodeGo_Default",
			args:    []string{},
			wantCmd: "serve",
		},
		{
			name:           "GlobalFlagDefaultsToServe",
			args:           []string{"-v"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-v"},
		},
		{
			name:     "VSCodeGo_RPCTrace",
			args:     []string{"-rpc.trace"},
			wantCmd:  "serve",
			wantArgs: []string{"-rpc.trace"},
		},
		{
			name:     "VSCodeGo_RPCTraceServe",
			args:     []string{"-rpc.trace", "serve"},
			wantCmd:  "serve",
			wantArgs: []string{"-rpc.trace"},
		},
		{
			name:     "XTools_ServeDebug",
			args:     []string{"serve", "-debug=localhost:6060"},
			wantCmd:  "serve",
			wantArgs: []string{"-debug=localhost:6060"},
		},
		{
			name:           "GlobalFlagBeforeExplicitServe",
			args:           []string{"-otel=http://localhost:4318", "serve"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-otel=http://localhost:4318"},
		},

		// ==========================================================
		// Non-Serve Subcommands
		// ==========================================================
		{
			name:    "OtherCases_Version",
			args:    []string{"version"},
			wantCmd: "version",
		},
		{
			name:    "XTools_Help",
			args:    []string{"help"},
			wantCmd: "help",
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
			wantArgs: []string{"-remote.debug=:0", "./gopls/main.go:35:8"},
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
			wantArgs: []string{"--", "-mode=convert", "-show=color"},
		},
		{
			name:           "OtherCases_VerboseExecuteServe",
			args:           []string{"-v", "execute", "serve"},
			wantCmd:        "execute",
			wantGlobalArgs: []string{"-v"},
			wantArgs:       []string{"serve"},
		},
		{
			name:    "OtherCases_UnknownAppFlag",
			args:    []string{"-nope", "execute"},
			wantErr: "unknown flag: -nope",
		},

		// ==========================================================
		// Edge cases
		// ==========================================================
		{
			name:           "AppFlagWithDoubleDash",
			args:           []string{"--verbose", "check", "foo.go"},
			wantCmd:        "check",
			wantGlobalArgs: []string{"--verbose"},
			wantArgs:       []string{"foo.go"},
		},
		{
			name:    "AppFlagWithTripleDash",
			args:    []string{"---foo"},
			wantErr: "unknown flag: ---foo",
		},
		{
			name:     "ServeFlagWithSpaceArg",
			args:     []string{"-listen", "localhost:3000"},
			wantCmd:  "serve",
			wantArgs: []string{"-listen", "localhost:3000"},
		},
		{
			name:     "LogfileWithSpaceServe",
			args:     []string{"-logfile", "serve"},
			wantCmd:  "serve",
			wantArgs: []string{"-logfile", "serve"},
		},
		{
			name:           "GlobalAndServeFlagsHoist",
			args:           []string{"-listen=localhost:3000", "-v"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-v"},
			wantArgs:       []string{"-listen=localhost:3000"},
		},
		{
			name:           "GlobalAndServeFlagsHoistMixed",
			args:           []string{"-listen", "localhost:3000", "-v"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-v"},
			wantArgs:       []string{"-listen", "localhost:3000"},
		},
		{
			name:     "DoubleDashPositional",
			args:     []string{"--", "foo"},
			wantCmd:  "serve",
			wantArgs: []string{"foo"},
		},
		{
			name:     "DoubleDashFlag",
			args:     []string{"--", "-v"},
			wantCmd:  "serve",
			wantArgs: []string{"-v"},
		},
		{
			name:    "UnknownFlagInServe",
			args:    []string{"-unknown"},
			wantErr: "unknown flag: -unknown",
		},
		{
			name:           "GlobalFlagWithSpaceArgBeforeCheck",
			args:           []string{"-otel", "http://localhost", "check", "file.go"},
			wantCmd:        "check",
			wantGlobalArgs: []string{"-otel", "http://localhost"},
			wantArgs:       []string{"file.go"},
		},
		{
			name:    "ImplicitServePositional",
			args:    []string{"-listen=localhost:3000", "foo"},
			wantErr: `unknown command "foo"`, // consistent with gopls@v0.20.0
		},
		{
			name:           "RemoteFlagBeforeServeCompat",
			args:           []string{"-remote=auto", "serve"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-remote=auto"},
		},
		{
			name:           "RemoteFlagDefaultsToServe",
			args:           []string{"-remote=auto"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-remote=auto"},
		},
		{
			name:           "RemoteFlagsDefaultToServe",
			args:           []string{"-remote=auto", "-remote.debug=localhost:8080"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-remote=auto", "-remote.debug=localhost:8080"},
		},

		{
			name:     "RepeatedSubcommandFlags",
			args:     []string{"-listen", "localhost:3000", "-listen", "localhost:4000"},
			wantCmd:  "serve",
			wantArgs: []string{"-listen", "localhost:3000", "-listen", "localhost:4000"},
		},
		{
			name:           "BooleanFlagInlineTrue",
			args:           []string{"-v=true"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-v=true"},
		},
		{
			name:           "GlobalFlagTrailingEqual",
			args:           []string{"-v="},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-v="},
		},
		{
			name:     "ServeFlagTrailingEqual",
			args:     []string{"-listen="},
			wantCmd:  "serve",
			wantArgs: []string{"-listen="},
		},
		{
			name:    "EmptyArgumentImplicitServe",
			args:    []string{""},
			wantErr: `unknown command ""`,
		},
		{
			name:           "EmptyArgumentAsGlobalFlagValue",
			args:           []string{"-otel", "", "check"},
			wantCmd:        "check",
			wantGlobalArgs: []string{"-otel", ""},
		},
		{
			name:     "RepeatedExplicitServeSubcommand",
			args:     []string{"serve", "serve"},
			wantCmd:  "serve",
			wantArgs: []string{"serve"},
		},
		{
			name:           "GlobalHelpFlagDefaultsToServe",
			args:           []string{"-help"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-help"},
		},

		// ==========================================================
		// Regression Tests: Reflection-based flag separation & misplaced flags
		// ==========================================================
		{
			name:           "SeparateProfileFlagFromServeFlag",
			args:           []string{"-profile.cpu=cpu.prof", "-listen=localhost:8080"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-profile.cpu=cpu.prof"},
			wantArgs:       []string{"-listen=localhost:8080"},
		},
		{
			name:           "SeparateVeryVerboseAndProfileFromServeFlags",
			args:           []string{"-vv", "-listen", "localhost:8080", "-profile.mem=mem.prof"},
			wantCmd:        "serve",
			wantGlobalArgs: []string{"-vv", "-profile.mem=mem.prof"},
			wantArgs:       []string{"-listen", "localhost:8080"},
		},
		{
			name:    "MisplacedServeListenFlagBeforeCheck",
			args:    []string{"-listen=localhost:8080", "check", "file.go"},
			wantCmd: "check",
			wantErr: "flag -listen belongs to subcommand serve",
		},
		{
			name:    "MisplacedServeLogfileFlagBeforeVersion",
			args:    []string{"-logfile=gopls.log", "version"},
			wantCmd: "version",
			wantErr: "flag -logfile belongs to subcommand serve",
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
			name:    "SubcommandFlagBeforeSubcommandFail",
			args:    []string{"-listen=:0", "references", "./gopls/main.go:35:8"},
			wantCmd: "references",
			wantErr: "flag -listen belongs to subcommand serve",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := newApplication() // Ensure test isolation
			t.Logf("> gopls %v", strings.Join(tc.args, " "))
			subApp, gotGlobalArgs, gotArgs, err := normalize(app, tc.args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("err = %v, want error containing %q", err, tc.wantErr)
				}
				if tc.wantCmd != "" && subApp != nil && subApp.Name() != tc.wantCmd {
					t.Errorf("normalize() cmd = %v, want %v on error", subApp.Name(), tc.wantCmd)
				}
				return
			}
			if err != nil {
				t.Fatalf("dispatch failed: %v", err)
			}
			if subApp.Name() != tc.wantCmd {
				t.Errorf("normalize() cmd = %v, want %v", subApp.Name(), tc.wantCmd)
			}
			if !slices.Equal(gotGlobalArgs, tc.wantGlobalArgs) {
				t.Errorf("normalize() globalArgs = %v, want %v", gotGlobalArgs, tc.wantGlobalArgs)
			}
			if !slices.Equal(gotArgs, tc.wantArgs) {
				t.Errorf("normalize() args = %v, want %v", gotArgs, tc.wantArgs)
			}
		})
	}
}
