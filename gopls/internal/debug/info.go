// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package debug exports debug information for gopls.
package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

type PrintMode int

const (
	PlainText = PrintMode(iota)
	Markdown
	HTML
	JSON
)

// Version is a manually-updated mechanism for tracking versions.
func Version() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			return info.Main.Version
		}
	}
	return "(unknown)"
}

// ServerVersion is the format used by gopls to report its version to the
// client. This format is structured so that the client can parse it easily.
type ServerVersion struct {
	*debug.BuildInfo
	Version string
}

// VersionInfo returns the build info for the gopls process. If it was not
// built in module mode, we return a GOPATH-specific message with the
// hardcoded version.
func VersionInfo() *ServerVersion {
	if info, ok := debug.ReadBuildInfo(); ok {
		return &ServerVersion{
			Version:   Version(),
			BuildInfo: info,
		}
	}
	return &ServerVersion{
		Version: Version(),
		BuildInfo: &debug.BuildInfo{
			Path:      "gopls, built in GOPATH mode",
			GoVersion: runtime.Version(),
		},
	}
}

// PrintServerInfo writes HTML debug info to w for the Instance.
func (i *Instance) PrintServerInfo(ctx context.Context, w io.Writer) {
	section(w, HTML, "Server Instance", func() {
		fmt.Fprintf(w, "Start time: %v\n", i.StartTime)
		fmt.Fprintf(w, "LogFile: %s\n", i.Logfile)
		fmt.Fprintf(w, "pid: %d\n", os.Getpid())
		fmt.Fprintf(w, "Working directory: %s\n", i.Workdir)
		fmt.Fprintf(w, "Address: %s\n", i.ServerAddress)
		fmt.Fprintf(w, "Debug address: %s\n", i.DebugAddress())
	})
	PrintVersionInfo(ctx, w, true, HTML)
	section(w, HTML, "Command Line", func() {
		fmt.Fprintf(w, "<a href=/debug/pprof/cmdline>cmdline</a>")
	})
}

// PrintVersionInfo writes version information to w, using the output format
// specified by mode. verbose controls whether additional information is
// written, including section headers.
func PrintVersionInfo(_ context.Context, w io.Writer, verbose bool, mode PrintMode) error {
	info := VersionInfo()
	if mode == JSON {
		return printVersionInfoJSON(w, info)
	}

	if !verbose {
		printBuildInfo(w, info, false, mode)
		return nil
	}
	section(w, mode, "Build info", func() {
		printBuildInfo(w, info, true, mode)
	})
	return nil
}

func printVersionInfoJSON(w io.Writer, info *ServerVersion) error {
	js, err := json.MarshalIndent(info, "", "\t")
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(w, string(js))
	return err
}

func section(w io.Writer, mode PrintMode, title string, body func()) {
	switch mode {
	case PlainText:
		fmt.Fprintln(w, title)
		fmt.Fprintln(w, strings.Repeat("-", len(title)))
		body()
	case Markdown:
		fmt.Fprintf(w, "#### %s\n\n```\n", title)
		body()
		fmt.Fprintf(w, "```\n")
	case HTML:
		fmt.Fprintf(w, "<h3>%s</h3>\n<pre>\n", title)
		body()
		fmt.Fprint(w, "</pre>\n")
	}
}

func printBuildInfo(w io.Writer, info *ServerVersion, verbose bool, mode PrintMode) {
	fmt.Fprintf(w, "%v %v\n", info.Path, Version())
	printModuleInfo(w, info.Main, mode)
	if !verbose {
		return
	}
	for _, dep := range info.Deps {
		printModuleInfo(w, *dep, mode)
	}
	fmt.Fprintf(w, "go: %v\n", info.GoVersion)
}

func printModuleInfo(w io.Writer, m debug.Module, _ PrintMode) {
	fmt.Fprintf(w, "    %s@%s", m.Path, m.Version)
	if m.Sum != "" {
		fmt.Fprintf(w, " %s", m.Sum)
	}
	if m.Replace != nil {
		fmt.Fprintf(w, " => %v", m.Replace.Path)
	}
	fmt.Fprintf(w, "\n")
}

type field struct {
	index []int
}

var fields []field

type sessionOption struct {
	Name    string
	Type    string
	Current string
	Default string
}
