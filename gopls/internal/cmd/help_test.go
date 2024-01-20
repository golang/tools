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
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/cmd"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/tool"
)

var updateHelpFiles = flag.Bool("update-help-files", false, "Write out the help files instead of checking them")

const appName = "gopls"

func TestHelpFiles(t *testing.T) {
	testenv.NeedsGoBuild(t) // This is a lie. We actually need the source code.
	app := cmd.New(nil)
	ctx := context.Background()
	for _, page := range append(app.Commands(), app) {
		t.Run(page.Name(), func(t *testing.T) {
			var buf bytes.Buffer
			s := flag.NewFlagSet(page.Name(), flag.ContinueOnError)
			s.SetOutput(&buf)
			tool.Run(ctx, s, page, []string{"-h"})
			name := page.Name()
			if name == appName {
				name = "usage"
			}
			helpFile := filepath.Join("usage", name+".hlp")
			got := buf.Bytes()
			if *updateHelpFiles {
				if err := os.WriteFile(helpFile, got, 0666); err != nil {
					t.Errorf("Failed writing %v: %v", helpFile, err)
				}
				return
			}
			want, err := os.ReadFile(helpFile)
			if err != nil {
				t.Fatalf("Missing help file %q", helpFile)
			}
			if diff := cmp.Diff(string(want), string(got)); diff != "" {
				t.Errorf("Help file %q did not match, run with -update-help-files to fix (-want +got)\n%s", helpFile, diff)
			}
		})
	}
}

func TestVerboseHelp(t *testing.T) {
	testenv.NeedsGoBuild(t) // This is a lie. We actually need the source code.
	app := cmd.New(nil)
	ctx := context.Background()
	var buf bytes.Buffer
	s := flag.NewFlagSet(appName, flag.ContinueOnError)
	s.SetOutput(&buf)
	tool.Run(ctx, s, app, []string{"-v", "-h"})
	got := buf.Bytes()

	helpFile := filepath.Join("usage", "usage-v.hlp")
	if *updateHelpFiles {
		if err := os.WriteFile(helpFile, got, 0666); err != nil {
			t.Errorf("Failed writing %v: %v", helpFile, err)
		}
		return
	}
	want, err := os.ReadFile(helpFile)
	if err != nil {
		t.Fatalf("Missing help file %q", helpFile)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("Help file %q did not match, run with -update-help-files to fix (-want +got)\n%s", helpFile, diff)
	}
}
