// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.19
// +build go1.19

package telemetry

import (
	"fmt"

	"golang.org/x/telemetry"
	"golang.org/x/telemetry/counter"
	"golang.org/x/telemetry/crashmonitor"
	"golang.org/x/telemetry/upload"
)

// CounterOpen calls [counter.Open].
func CounterOpen() {
	counter.Open()
}

// StartCrashMonitor calls [crashmonitor.Start].
func StartCrashMonitor() {
	crashmonitor.Start()
}

// CrashMonitorSupported calls [crashmonitor.Supported].
func CrashMonitorSupported() bool {
	return crashmonitor.Supported()
}

// NewStackCounter calls [counter.NewStack].
func NewStackCounter(name string, depth int) *counter.StackCounter {
	return counter.NewStack(name, depth)
}

// Mode calls x/telemetry.Mode.
func Mode() string {
	return telemetry.Mode()
}

// SetMode calls x/telemetry.SetMode.
func SetMode(mode string) error {
	return telemetry.SetMode(mode)
}

// Upload starts a goroutine for telemetry upload.
func Upload() {
	go upload.Run(nil)
}

// RecordClientInfo records gopls client info.
func RecordClientInfo(clientName string) {
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

// RecordViewGoVersion records the Go minor version number (1.x) used for a view.
func RecordViewGoVersion(x int) {
	if x < 0 {
		return
	}
	name := fmt.Sprintf("gopls/goversion:1.%d", x)
	counter.Inc(name)
}

// AddForwardedCounters adds the given counters on behalf of clients.
// Names and values must have the same length.
func AddForwardedCounters(names []string, values []int64) {
	for i, n := range names {
		v := values[i]
		if n == "" || v < 0 {
			continue // Should we report an error? Who is the audience?
		}
		counter.Add("fwd/"+n, v)
	}
}
