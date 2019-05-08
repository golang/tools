// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd_test

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/tools/internal/lsp/cmd"
	"golang.org/x/tools/internal/lsp/tests"
	"golang.org/x/tools/internal/tool"
)

var formatModes = [][]string{
	[]string{},
	[]string{"-d"},
}

func (r *runner) Format(t *testing.T, data tests.Formats) {
	for _, spn := range data {
		for _, mode := range formatModes {
			tag := "gofmt" + strings.Join(mode, "")
			uri := spn.URI()
			filename, err := uri.Filename()
			if err != nil {
				t.Fatal(err)
			}
			args := append(mode, filename)
			expect := string(r.data.Golden(tag, filename, func() ([]byte, error) {
				cmd := exec.Command("gofmt", args...)
				contents, _ := cmd.Output() // ignore error, sometimes we have intentionally ungofmt-able files
				contents = []byte(r.normalizePaths(fixFileHeader(string(contents))))
				return contents, nil
			}))
			if expect == "" {
				//TODO: our error handling differs, for now just skip unformattable files
				continue
			}
			app := &cmd.Application{}
			app.Config = r.data.Config
			got := captureStdOut(t, func() {
				tool.Main(context.Background(), app, append([]string{"format"}, args...))
			})
			got = r.normalizePaths(got)
			// check the first two lines are the expected file header
			if expect != got {
				t.Errorf("format failed with %#v expected:\n%s\ngot:\n%s", args, expect, got)
			}
		}
	}
}

var unifiedHeader = regexp.MustCompile(`^diff -u.*\n(---\s+\S+\.go\.orig)\s+[\d-:. ]+(\n\+\+\+\s+\S+\.go)\s+[\d-:. ]+(\n@@)`)

func fixFileHeader(s string) string {
	match := unifiedHeader.FindStringSubmatch(s)
	if match == nil {
		return s
	}
	return strings.Join(append(match[1:], s[len(match[0]):]), "")
}
