// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"strings"
	"testing"
)

func TestReadPCLineTable(t *testing.T) {
	if testing.Short() {
		// TODO(prattmic): It would be nice to have a unit test that
		// didn't require downloading.
		t.Skip("downloads source from the internet, skipping in -short")
	}

	type testCase struct {
		name         string
		info         Info
		wantSymbol   string
		wantFileLine FileLine
	}

	tests := []testCase{
		{
			name: "gopls",
			info: Info{
				Program:        "golang.org/x/tools/gopls",
				ProgramVersion: "v0.16.1",
				GoVersion:      "go1.23.4",
				GOOS:           "linux",
				GOARCH:         "amd64",
			},
			wantSymbol: "golang.org/x/tools/gopls/internal/cmd.(*Application).Run",
			wantFileLine: FileLine{
				file: "golang.org/x/tools/gopls/internal/cmd/cmd.go",
				line: 230,
			},
		},
		{
			name: "compile",
			info: Info{
				Program:        "cmd/compile",
				ProgramVersion: "go1.23.4",
				GoVersion:      "go1.23.4",
				GOOS:           "linux",
				GOARCH:         "amd64",
			},
			wantSymbol: "runtime.main",
			wantFileLine: FileLine{
				file: "runtime/proc.go",
				line: 147,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stacksDir := t.TempDir()
			pcln, err := readPCLineTable(tc.info, stacksDir)
			if err != nil {
				t.Fatalf("readPCLineTable got err %v want nil", err)
			}

			got, ok := pcln[tc.wantSymbol]
			if !ok {
				t.Fatalf("PCLineTable want entry %s got !ok from pcln %+v", tc.wantSymbol, pcln)
			}

			if got != tc.wantFileLine {
				t.Fatalf("symbol %s got FileLine %+v want %+v", tc.wantSymbol, got, tc.wantFileLine)
			}
		})
	}
}

func TestParsePredicate(t *testing.T) {
	for _, tc := range []struct {
		expr string
		arg  string
		want bool
	}{
		{`"x"`, `"x"`, true},
		{`"x"`, `"axe"`, false}, // literals match whole words
		{`"x"`, "val:x+5", true},
		{`"fu+12"`, "x:fu+12,", true},
		{`"fu+12"`, "snafu+12,", false},
		{`"fu+12"`, "x:fu+123,", false},
		{`"a.*b"`, "a.*b", true},  // regexp metachars are escaped
		{`"a.*b"`, "axxb", false}, // ditto
		{`"x"`, `"y"`, false},
		{`!"x"`, "x", false},
		{`!"x"`, "y", true},
		{`"x" && "y"`, "xy", false},
		{`"x" && "y"`, "x y", true},
		{`"x" && "y"`, "x", false},
		{`"x" && "y"`, "y", false},
		{`"xz" && "zy"`, "xzy", false},
		{`"xz" && "zy"`, "zy,xz", true},
		{`"x" || "y"`, "x\ny", true},
		{`"x" || "y"`, "x", true},
		{`"x" || "y"`, "y", true},
		{`"x" || "y"`, "z", false},
	} {
		eval, err := parsePredicate(tc.expr)
		if err != nil {
			t.Fatal(err)
		}
		got := eval(tc.arg)
		if got != tc.want {
			t.Errorf("%s applied to %q: got %t, want %t", tc.expr, tc.arg, got, tc.want)
		}
	}
}

func TestParsePredicateError(t *testing.T) {
	// Validate that bad predicates return errors.
	for _, expr := range []string{
		``,
		`1`,
		`foo`, // an identifier, not a literal
		`"x" + "y"`,
		`"x" &&`,
		`~"x"`,
		`f(1)`,
	} {
		if _, err := parsePredicate(expr); err == nil {
			t.Errorf("%s: got nil, want error", expr)
		}
	}
}

// which takes the bulk of the time.
func TestUpdateIssues(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads source from the internet, skipping in -short")
	}
	c := &githubClient{divertChanges: true}
	issues := []*Issue{{Number: 1, newStacks: []string{"stack1"}}}
	info := Info{
		Program:        "golang.org/x/tools/gopls",
		ProgramVersion: "v0.16.1",
	}
	const stack1 = "stack1"
	id1 := stackID(stack1)
	stacks := map[string]map[Info]int64{stack1: map[Info]int64{info: 3}}
	stacksToURL := map[string]string{stack1: "URL1"}
	updateIssues(c, issues, stacks, stacksToURL)

	if g, w := len(c.changes), 2; g != w {
		t.Fatalf("got %d changes, want %d", g, w)
	}
	// The first change creates an issue comment.
	cic, ok := c.changes[0].(addIssueComment)
	if !ok {
		t.Fatalf("got %T, want addIssueComment", c.changes[0])
	}
	if cic.number != 1 {
		t.Errorf("issue number: got %d, want 1", cic.number)
	}
	for _, want := range []string{"URL1", stack1, id1, "golang.org/x/tools/gopls@v0.16.1"} {
		if !strings.Contains(cic.comment, want) {
			t.Errorf("missing %q in comment:\n%s", want, cic.comment)
		}
	}

	// The second change updates the issue body.
	ui, ok := c.changes[1].(updateIssueBody)
	if !ok {
		t.Fatalf("got %T, want updateIssueBody", c.changes[1])
	}
	if ui.number != 1 {
		t.Errorf("issue number: got %d, want 1", cic.number)
	}
	want := "Dups: " + id1
	if !strings.Contains(ui.body, want) {
		t.Errorf("missing %q in body %q", want, ui.body)
	}
}
