// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd_test

// This file defines tests to ensure the cmd/usage/*.hlp files match
// the output of the tool. The .hlp files are not actually needed by
// the executable (they are not //go:embed-ded, say), but they make it
// easier to review changes to the gopls command's help logic since
// any effects are manifest as changes to these files.

//go:generate go test -run Help -update-help-files

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/cmd"
	"golang.org/x/tools/internal/testenv"
)

var updateHelpFiles = flag.Bool("update-help-files", false, "Write out the help files instead of checking them")

func TestHelpFiles(t *testing.T) {
	testenv.NeedsGoBuild(t) // This is a lie. We actually need the source code.
	t.Parallel()
	tree := writeTree(t, "")
	for _, name := range cmd.CommandNames() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			args := []string{name, "-h"}
			// The output of 'gopls -h' is in usage.hlp
			if name == "gopls" {
				args = args[1:]
				name = "usage"
			}
			res := gopls(t, tree, args...)
			res.checkExit(true) // -h should result in exit 0
			got := res.stdout
			helpFile := filepath.Join("usage", name+".hlp")
			if *updateHelpFiles {
				if err := os.WriteFile(helpFile, []byte(got), 0666); err != nil {
					t.Errorf("Failed writing %v: %v", helpFile, err)
				}
				return
			}
			want, err := os.ReadFile(helpFile)
			if err != nil {
				t.Fatalf("Missing help file %q", helpFile)
			}
			if diff := cmp.Diff(string(want), got); diff != "" {
				t.Errorf("Help file %q did not match, run with -update-help-files to fix (-want +got)\n%s", helpFile, diff)
			}
		})
	}
}
func TestVerboseHelp(t *testing.T) {
	testenv.NeedsGoBuild(t) // This is a lie. We actually need the source code.
	t.Parallel()
	tree := writeTree(t, "")
	res := gopls(t, tree, "-v", "-h")
	res.checkExit(true) // -h should result in exit 0
	got := res.stdout
	helpFile := filepath.Join("usage", "usage-v.hlp")
	if *updateHelpFiles {
		if err := os.WriteFile(helpFile, []byte(got), 0666); err != nil {
			t.Errorf("Failed writing %v: %v", helpFile, err)
		}
		return
	}
	want, err := os.ReadFile(helpFile)
	if err != nil {
		t.Fatalf("Missing help file %q", helpFile)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("Help file %q did not match, run with -update-help-files to fix (-want +got)\n%s", helpFile, diff)
	}
}

// TestHelpTree tests "gopls help" on a number
// of levels of the commmand tree.
func TestHelpTree(t *testing.T) {
	t.Parallel()

	tree := writeTree(t, ``)

	for _, test := range []struct {
		args         []string
		wantSuccess  bool
		wantPatterns []string
	}{
		// gopls help
		{
			args:        []string{"help"},
			wantSuccess: true,
			wantPatterns: []string{
				"gopls is a Go language server",
				"https://go.dev/gopls/features",
				"Usage:",
				"Command:",
				"  links.*list links in a file", // command menu
			},
		},
		// gopls help remote
		{
			args:        []string{"help", "remote"},
			wantSuccess: true,
			wantPatterns: []string{
				"interact with the gopls daemon",
				"Usage:",
				"Subcommand:",
				"  sessions.*print information about current gopls sessions", // subcommand menu
			},
		},
		// gopls help remote sessions
		{
			args:        []string{"help", "remote", "sessions"},
			wantSuccess: true,
			wantPatterns: []string{
				"print information about current gopls sessions",
				"Usage:",
				"list sessions for the default daemon",
			},
		},
		// gopls help remote nonesuch
		{
			args: []string{"help", "remote", "nonesuch"},
			wantPatterns: []string{
				"gopls: no such subcommand: remote nonesuch",
			},
		},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			res := gopls(t, tree, test.args...)
			res.checkExit(test.wantSuccess)
			if test.wantSuccess {
				res.checkStderr("^$") // no stderr
				for _, pattern := range test.wantPatterns {
					res.checkStdout(pattern)
				}
			} else {
				res.checkStdout("^$") // no stdout
				for _, pattern := range test.wantPatterns {
					res.checkStderr(pattern)
				}
			}
		})
	}
}
