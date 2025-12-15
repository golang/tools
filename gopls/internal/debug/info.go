// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package debug exports debug information for gopls.
package debug

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"golang.org/x/tools/gopls/internal/version"
)

type PrintMode int

const (
	PlainText = PrintMode(iota)
	Markdown
	HTML
	JSON
)

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
			Version:   version.Version(),
			BuildInfo: info,
		}
	}
	return &ServerVersion{
		Version: version.Version(),
		BuildInfo: &debug.BuildInfo{
			Path:      "gopls, built in GOPATH mode",
			GoVersion: runtime.Version(),
		},
	}
}

// writeServerInfo writes HTML debug info to w for the instance.
func (i *Instance) writeServerInfo(out *bytes.Buffer) {
	workDir, _ := os.Getwd()
	section(out, HTML, "server instance", func() {
		fmt.Fprintf(out, "Start time: %v\n", i.StartTime)
		fmt.Fprintf(out, "LogFile: %s\n", i.Logfile)
		fmt.Fprintf(out, "pid: %d\n", os.Getpid())
		fmt.Fprintf(out, "Working directory: %s\n", workDir)
		fmt.Fprintf(out, "Address: %s\n", i.ServerAddress)
		fmt.Fprintf(out, "Debug address: %s\n", i.DebugAddress())
	})
	WriteVersionInfo(out, true, HTML)
	section(out, HTML, "Command Line", func() {
		fmt.Fprintf(out, "<a href=/debug/pprof/cmdline>cmdline</a>")
	})
}

// WriteVersionInfo writes version information to w, using the output format
// specified by mode. verbose controls whether additional information is
// written, including section headers.
func WriteVersionInfo(out *bytes.Buffer, verbose bool, mode PrintMode) {
	info := VersionInfo()
	if mode == JSON {
		writeVersionInfoJSON(out, info)
		return
	}

	if !verbose {
		writeBuildInfo(out, info, false, mode)
		return
	}
	section(out, mode, "Build info", func() {
		writeBuildInfo(out, info, true, mode)
	})
}

func writeVersionInfoJSON(out *bytes.Buffer, info *ServerVersion) {
	data, err := json.MarshalIndent(info, "", "\t")
	if err != nil {
		panic(err) // can't happen
	}
	out.Write(data)
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

func writeBuildInfo(w io.Writer, info *ServerVersion, verbose bool, mode PrintMode) {
	fmt.Fprintf(w, "%v %v\n", info.Path, version.Version())
	if !verbose {
		return
	}
	printModuleInfo(w, info.Main, mode)
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
