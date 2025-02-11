// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"encoding/json"
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
		{`"x"`, `"axe"`, false}, // literals must match word ends
		{`"xe"`, `"axe"`, true},
		{`"x"`, "val:x+5", true},
		{`"fu+12"`, "x:fu+12,", true},
		{`"fu+12"`, "snafu+12,", true}, // literals needn't match word start
		{`"fu+12"`, "x:fu+123,", false},
		{`"foo:+12"`, "dir/foo:+12,", true}, // literals needn't match word start
		{`"a.*b"`, "a.*b", true},            // regexp metachars are escaped
		{`"a.*b"`, "axxb", false},           // ditto
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
	const stack1 = "stack1"
	id1 := stackID(stack1)
	stacksToURL := map[string]string{stack1: "URL1"}

	// checkIssueComment asserts that the change adds an issue of the specified
	// number, with a body that contains various strings.
	checkIssueComment := func(t *testing.T, change any, number int, version string) {
		t.Helper()
		cic, ok := change.(addIssueComment)
		if !ok {
			t.Fatalf("got %T, want addIssueComment", change)
		}
		if cic.number != number {
			t.Errorf("issue number: got %d, want %d", cic.number, number)
		}
		for _, want := range []string{"URL1", stack1, id1, "golang.org/x/tools/gopls@" + version} {
			if !strings.Contains(cic.comment, want) {
				t.Errorf("missing %q in comment:\n%s", want, cic.comment)
			}
		}
	}

	t.Run("open issue", func(t *testing.T) {
		issues := []*Issue{{
			Number:    1,
			State:     "open",
			newStacks: []string{stack1},
		}}

		info := Info{
			Program:        "golang.org/x/tools/gopls",
			ProgramVersion: "v0.16.1",
		}
		stacks := map[string]map[Info]int64{stack1: map[Info]int64{info: 3}}
		updateIssues(c, issues, stacks, stacksToURL)
		changes := c.takeChanges()

		if g, w := len(changes), 2; g != w {
			t.Fatalf("got %d changes, want %d", g, w)
		}

		// The first change creates an issue comment.
		checkIssueComment(t, changes[0], 1, "v0.16.1")

		// The second change updates the issue body, and only the body.
		ui, ok := changes[1].(updateIssue)
		if !ok {
			t.Fatalf("got %T, want updateIssue", changes[1])
		}
		if ui.number != 1 {
			t.Errorf("issue number: got %d, want 1", ui.number)
		}
		if ui.Body == "" || ui.State != "" || ui.StateReason != "" {
			t.Errorf("updating other than just the body:\n%+v", ui)
		}
		want := "Dups: " + id1
		if !strings.Contains(ui.Body, want) {
			t.Errorf("missing %q in body %q", want, ui.Body)
		}
	})
	t.Run("should be reopened", func(t *testing.T) {
		issues := []*Issue{{
			// Issue purportedly fixed in v0.16.0
			Number:      2,
			State:       "closed",
			StateReason: "completed",
			Milestone:   &Milestone{Title: "gopls/v0.16.0"},
			newStacks:   []string{stack1},
		}}
		// New stack in a later version.
		info := Info{
			Program:        "golang.org/x/tools/gopls",
			ProgramVersion: "v0.17.0",
		}
		stacks := map[string]map[Info]int64{stack1: map[Info]int64{info: 3}}
		updateIssues(c, issues, stacks, stacksToURL)

		changes := c.takeChanges()
		if g, w := len(changes), 2; g != w {
			t.Fatalf("got %d changes, want %d", g, w)
		}
		// The first change creates an issue comment.
		checkIssueComment(t, changes[0], 2, "v0.17.0")

		// The second change updates the issue body, state, and state reason.
		ui, ok := changes[1].(updateIssue)
		if !ok {
			t.Fatalf("got %T, want updateIssue", changes[1])
		}
		if ui.number != 2 {
			t.Errorf("issue number: got %d, want 2", ui.number)
		}
		if ui.Body == "" || ui.State != "open" || ui.StateReason != "reopened" {
			t.Errorf(`update fields should be non-empty body, state "open", state reason "reopened":\n%+v`, ui)
		}
		want := "Dups: " + id1
		if !strings.Contains(ui.Body, want) {
			t.Errorf("missing %q in body %q", want, ui.Body)
		}

	})

}

func TestMarshalUpdateIssueFields(t *testing.T) {
	// Verify that only the non-empty fields of updateIssueFields are marshalled.
	for _, tc := range []struct {
		fields updateIssue
		want   string
	}{
		{updateIssue{Body: "b"}, `{"body":"b"}`},
		{updateIssue{State: "open"}, `{"state":"open"}`},
		{updateIssue{State: "open", StateReason: "reopened"}, `{"state":"open","state_reason":"reopened"}`},
	} {
		bytes, err := json.Marshal(tc.fields)
		if err != nil {
			t.Fatal(err)
		}
		got := string(bytes)
		if got != tc.want {
			t.Errorf("%+v: got %s, want %s", tc.fields, got, tc.want)
		}
	}
}

func TestShouldReopen(t *testing.T) {
	const stack = "stack"
	const gopls = "golang.org/x/tools/gopls"
	goplsMilestone := &Milestone{Title: "gopls/v0.2.0"}
	goMilestone := &Milestone{Title: "Go1.23"}

	for _, tc := range []struct {
		name  string
		issue Issue
		info  Info
		want  bool
	}{
		{
			"issue open",
			Issue{State: "open", Milestone: goplsMilestone},
			Info{Program: gopls, ProgramVersion: "v0.2.0"},
			false,
		},
		{
			"issue closed but not fixed",
			Issue{State: "closed", StateReason: "not_planned", Milestone: goplsMilestone},
			Info{Program: gopls, ProgramVersion: "v0.2.0"},
			false,
		},
		{
			"different program",
			Issue{State: "closed", StateReason: "completed", Milestone: goplsMilestone},
			Info{Program: "other", ProgramVersion: "v0.2.0"},
			false,
		},
		{
			"later version",
			Issue{State: "closed", StateReason: "completed", Milestone: goplsMilestone},
			Info{Program: gopls, ProgramVersion: "v0.3.0"},
			true,
		},
		{
			"earlier version",
			Issue{State: "closed", StateReason: "completed", Milestone: goplsMilestone},
			Info{Program: gopls, ProgramVersion: "v0.1.0"},
			false,
		},
		{
			"same version",
			Issue{State: "closed", StateReason: "completed", Milestone: goplsMilestone},
			Info{Program: gopls, ProgramVersion: "v0.2.0"},
			true,
		},
		{
			"compiler later version",
			Issue{State: "closed", StateReason: "completed", Milestone: goMilestone},
			Info{Program: "cmd/compile", ProgramVersion: "go1.24"},
			true,
		},
		{
			"compiler earlier version",
			Issue{State: "closed", StateReason: "completed", Milestone: goMilestone},
			Info{Program: "cmd/compile", ProgramVersion: "go1.22"},
			false,
		},
		{
			"compiler same version",
			Issue{State: "closed", StateReason: "completed", Milestone: goMilestone},
			Info{Program: "cmd/compile", ProgramVersion: "go1.23"},
			true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.issue.Number = 1
			tc.issue.newStacks = []string{stack}
			got := shouldReopen(&tc.issue, map[string]map[Info]int64{stack: map[Info]int64{tc.info: 1}})
			if got != tc.want {
				t.Errorf("got %t, want %t", got, tc.want)
			}
		})
	}
}
