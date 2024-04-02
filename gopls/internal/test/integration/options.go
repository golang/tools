// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package integration

import (
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

type runConfig struct {
	editor        fake.EditorConfig
	sandbox       fake.SandboxConfig
	modes         Mode
	noLogsOnError bool
	writeGoSum    []string
}

func defaultConfig() runConfig {
	return runConfig{
		editor: fake.EditorConfig{
			Settings: map[string]interface{}{
				// Shorten the diagnostic delay to speed up test execution (else we'd add
				// the default delay to each assertion about diagnostics)
				"diagnosticsDelay": "10ms",
			},
		},
	}
}

// A RunOption augments the behavior of the test runner.
type RunOption interface {
	set(*runConfig)
}

type optionSetter func(*runConfig)

func (f optionSetter) set(opts *runConfig) {
	f(opts)
}

// ProxyFiles configures a file proxy using the given txtar-encoded string.
func ProxyFiles(txt string) RunOption {
	return optionSetter(func(opts *runConfig) {
		opts.sandbox.ProxyFiles = fake.UnpackTxt(txt)
	})
}

// WriteGoSum causes the environment to write a go.sum file for the requested
// relative directories (via `go list -mod=mod`), before starting gopls.
//
// Useful for tests that use ProxyFiles, but don't care about crafting the
// go.sum content.
func WriteGoSum(dirs ...string) RunOption {
	return optionSetter(func(opts *runConfig) {
		opts.writeGoSum = dirs
	})
}

// Modes configures the execution modes that the test should run in.
//
// By default, modes are configured by the test runner. If this option is set,
// it overrides the set of default modes and the test runs in exactly these
// modes.
func Modes(modes Mode) RunOption {
	return optionSetter(func(opts *runConfig) {
		if opts.modes != 0 {
			panic("modes set more than once")
		}
		opts.modes = modes
	})
}

// NoLogsOnError turns off dumping the LSP logs on test failures.
func NoLogsOnError() RunOption {
	return optionSetter(func(opts *runConfig) {
		opts.noLogsOnError = true
	})
}

// WindowsLineEndings configures the editor to use windows line endings.
func WindowsLineEndings() RunOption {
	return optionSetter(func(opts *runConfig) {
		opts.editor.WindowsLineEndings = true
	})
}

// ClientName sets the LSP client name.
func ClientName(name string) RunOption {
	return optionSetter(func(opts *runConfig) {
		opts.editor.ClientName = name
	})
}

// CapabilitiesJSON sets the capabalities json.
func CapabilitiesJSON(capabilities []byte) RunOption {
	return optionSetter(func(opts *runConfig) {
		opts.editor.CapabilitiesJSON = capabilities
	})
}

// Settings sets user-provided configuration for the LSP server.
//
// As a special case, the env setting must not be provided via Settings: use
// EnvVars instead.
type Settings map[string]interface{}

func (s Settings) set(opts *runConfig) {
	if opts.editor.Settings == nil {
		opts.editor.Settings = make(map[string]interface{})
	}
	for k, v := range s {
		opts.editor.Settings[k] = v
	}
}

// WorkspaceFolders configures the workdir-relative workspace folders to send
// to the LSP server. By default the editor sends a single workspace folder
// corresponding to the workdir root. To explicitly configure no workspace
// folders, use WorkspaceFolders with no arguments.
func WorkspaceFolders(relFolders ...string) RunOption {
	if len(relFolders) == 0 {
		// Use an empty non-nil slice to signal explicitly no folders.
		relFolders = []string{}
	}

	return optionSetter(func(opts *runConfig) {
		opts.editor.WorkspaceFolders = relFolders
	})
}

// FolderSettings defines per-folder workspace settings, keyed by relative path
// to the folder.
//
// Use in conjunction with WorkspaceFolders to have different settings for
// different folders.
type FolderSettings map[string]Settings

func (fs FolderSettings) set(opts *runConfig) {
	// Re-use the Settings type, for symmetry, but translate back into maps for
	// the editor config.
	folders := make(map[string]map[string]any)
	for k, v := range fs {
		folders[k] = v
	}
	opts.editor.FolderSettings = folders
}

// EnvVars sets environment variables for the LSP session. When applying these
// variables to the session, the special string $SANDBOX_WORKDIR is replaced by
// the absolute path to the sandbox working directory.
type EnvVars map[string]string

func (e EnvVars) set(opts *runConfig) {
	if opts.editor.Env == nil {
		opts.editor.Env = make(map[string]string)
	}
	for k, v := range e {
		opts.editor.Env[k] = v
	}
}

// InGOPATH configures the workspace working directory to be GOPATH, rather
// than a separate working directory for use with modules.
func InGOPATH() RunOption {
	return optionSetter(func(opts *runConfig) {
		opts.sandbox.InGoPath = true
	})
}

// MessageResponder configures the editor to respond to
// window/showMessageRequest messages using the provided function.
func MessageResponder(f func(*protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error)) RunOption {
	return optionSetter(func(opts *runConfig) {
		opts.editor.MessageResponder = f
	})
}
