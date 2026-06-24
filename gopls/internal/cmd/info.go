// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

// This file defines the help, version, api-json, licenses commands.

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/tools/gopls/internal/debug"
	"golang.org/x/tools/gopls/internal/doc"
	licensespkg "golang.org/x/tools/gopls/internal/licenses"
	"golang.org/x/tools/gopls/internal/tool"
)

// help implements the help command.
type help struct {
	app *Application
}

func (h *help) Name() string      { return "help" }
func (h *help) Parent() string    { return h.app.Name() }
func (h *help) Usage() string     { return "" }
func (h *help) ShortHelp() string { return "print usage information for subcommands" }
func (h *help) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `

Examples:
$ gopls help                         # main gopls help message
$ gopls help remote                  # help on 'remote' command
$ gopls help remote sessions         # help on 'remote sessions' subcommand
`)
	printFlagDefaults(f)
}

// Run prints help information about a subcommand.
func (h *help) Run(ctx context.Context, args ...string) error {
	find := func(cmds []tool.Application, name string) tool.Application {
		for _, cmd := range cmds {
			if cmd.Name() == name {
				return cmd
			}
		}
		return nil
	}

	// Find the subcommand denoted by args (empty => h.app).
	var cmd tool.Application = h.app
	for i, arg := range args {
		cmd = find(getSubcommands(cmd), arg)
		if cmd == nil {
			return tool.CommandLineErrorf(
				"no such subcommand: %s", strings.Join(args[:i+1], " "))
		}
	}

	// 'gopls help cmd subcmd' is equivalent to 'gopls cmd subcmd -h'.
	// The flag package prints the usage information (defined by tool.Run)
	// when it sees the -h flag.
	fs := flag.NewFlagSet(cmd.Name(), flag.ExitOnError)
	return tool.Run(ctx, fs, h.app, append(args[:len(args):len(args)], "-h"))
}

// version implements the version command.
type version struct {
	JSON bool `flag:"json" help:"outputs in json format."`

	app *Application
}

func (v *version) Name() string      { return "version" }
func (v *version) Parent() string    { return v.app.Name() }
func (v *version) Usage() string     { return "" }
func (v *version) ShortHelp() string { return "print the gopls version information" }
func (v *version) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), ``)
	printFlagDefaults(f)
}

// Run prints version information to stdout.
func (v *version) Run(ctx context.Context, args ...string) error {
	var mode = debug.PlainText
	if v.JSON {
		mode = debug.JSON
	}
	var buf bytes.Buffer
	debug.WriteVersionInfo(&buf, v.app.verbose(), mode)
	_, err := io.Copy(os.Stdout, &buf)
	return err
}

type apiJSON struct {
	app *Application
}

func (j *apiJSON) Name() string      { return "api-json" }
func (j *apiJSON) Parent() string    { return j.app.Name() }
func (j *apiJSON) Usage() string     { return "" }
func (j *apiJSON) ShortHelp() string { return "print JSON describing gopls API" }
func (j *apiJSON) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
The api-json command prints a JSON value that describes
and documents all gopls' public interfaces.
Its schema is defined by golang.org/x/tools/gopls/internal/doc.API.
`)
	printFlagDefaults(f)
}

func (j *apiJSON) Run(ctx context.Context, args ...string) error {
	os.Stdout.WriteString(doc.JSON)
	fmt.Println()
	return nil
}

type licenses struct {
	app *Application
}

func (l *licenses) Name() string      { return "licenses" }
func (l *licenses) Parent() string    { return l.app.Name() }
func (l *licenses) Usage() string     { return "" }
func (l *licenses) ShortHelp() string { return "print licenses of included software" }
func (l *licenses) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), ``)
	printFlagDefaults(f)
}

const licensePreamble = `
gopls is made available under the following BSD-style license:

Copyright (c) 2009 The Go Authors. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google Inc. nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

gopls implements the LSP specification, which is made available under the following license:

Copyright (c) Microsoft Corporation

All rights reserved.

MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation
files (the "Software"), to deal in the Software without restriction, including without limitation the rights to use, copy,
modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software
is furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED *AS IS*, WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES
OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS
BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT
OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

gopls also includes software made available under these licenses:
`

func (l *licenses) Run(ctx context.Context, args ...string) error {
	txt := licensePreamble
	if licensespkg.Text == "" {
		txt += "(development gopls, license information not available)"
	} else {
		txt += licensespkg.Text
	}
	fmt.Fprint(os.Stdout, txt)
	return nil
}
