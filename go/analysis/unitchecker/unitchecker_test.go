// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package unitchecker_test

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/passes/assign"
	"golang.org/x/tools/go/analysis/passes/findcall"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/unitchecker"
	"golang.org/x/tools/internal/packagestest"
)

func TestMain(m *testing.M) {
	// child process?
	switch os.Getenv("ENTRYPOINT") {
	case "vet":
		vet()
		panic("unreachable")
	case "minivet":
		minivet()
		panic("unreachable")
	case "worker":
		worker() // see ExampleSeparateAnalysis
		panic("unreachable")
	}

	// test process
	flag.Parse()
	os.Exit(m.Run())
}

// minivet is a vet-like tool with a few analyzers, for testing.
func minivet() {
	unitchecker.Main(
		findcall.Analyzer,
		printf.Analyzer,
		assign.Analyzer,
	)
}

// This is a very basic integration test of modular
// analysis with facts using unitchecker under "go vet".
// It fork/execs the main function above.
func TestIntegration(t *testing.T) { packagestest.TestAll(t, testIntegration) }
func testIntegration(t *testing.T, exporter packagestest.Exporter) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("skipping fork/exec test on this platform")
	}

	exported := packagestest.Export(t, exporter, []packagestest.Module{{
		Name: "golang.org/fake",
		Files: map[string]any{
			"a/a.go": `package a

func _() {
	MyFunc123()
}

func MyFunc123() {}
`,
			"b/b.go": `package b

import "golang.org/fake/a"

func _() {
	a.MyFunc123()
	MyFunc123()
}

func MyFunc123() {}
`,
			"c/c.go": `package c

func _() {
    i := 5
    i = i
}
`,
		}}})
	defer exported.Cleanup()

	const wantA = `
.*a/a.go:4:11: call of MyFunc123\(...\)
`
	const wantB = `
.*b/b.go:6:13: call of MyFunc123\(...\)
.*b/b.go:7:11: call of MyFunc123\(...\)
`
	const wantC = `
.*c/c.go:5:5: self-assignment of i
`
	const wantAJSON = `
\{
	"golang.org/fake/a": \{
		"findcall": \[
			\{
				"posn": ".*a/a.go:4:11",
				"message": "call of MyFunc123\(...\)",
				"suggested_fixes": \[
					\{
						"message": "Add '_TEST_'",
						"edits": \[
							\{
								"filename": ".*a/a.go",
								"start": 32,
								"end": 32,
								"new": "_TEST_"
							\}
						\]
					\}
				\]
			\}
		\]
	\}
\}
`
	const wantCJSON = `
\{
	"golang.org/fake/c": \{
		"assign": \[
			\{
				"posn": ".*c/c.go:5:5",
				"message": "self-assignment of i",
				"suggested_fixes": \[
					\{
						"message": "Remove self-assignment",
						"edits": \[
							\{
								"filename": ".*c/c.go",
								"start": 37,
								"end": 42,
								"new": ""
							\}
						\]
					\}
				\]
			\}
		\]
	\}
\}
`
	for _, test := range []struct {
		args          string
		wantOut       string // multiline regular expression
		wantExitError bool
	}{
		{args: "golang.org/fake/a", wantOut: wantA, wantExitError: true},
		{args: "golang.org/fake/b", wantOut: wantB, wantExitError: true},
		{args: "golang.org/fake/c", wantOut: wantC, wantExitError: true},
		{args: "golang.org/fake/a golang.org/fake/b", wantOut: wantA + ".*" + wantB, wantExitError: true},
		{args: "-json golang.org/fake/a", wantOut: wantAJSON, wantExitError: false},
		{args: "-json golang.org/fake/c", wantOut: wantCJSON, wantExitError: false},
		{args: "-c=0 golang.org/fake/a", wantOut: wantA + "4		MyFunc123\\(\\)\n", wantExitError: true},
	} {
		cmd := exec.Command("go", "vet", "-vettool="+os.Args[0], "-findcall.name=MyFunc123")
		cmd.Stdout = new(strings.Builder)
		cmd.Stderr = new(strings.Builder)
		cmd.Args = append(cmd.Args, strings.Fields(test.args)...)
		cmd.Env = append(exported.Config.Env, "ENTRYPOINT=minivet")
		cmd.Dir = exported.Config.Dir

		// TODO(golang/go#65729): Rework the test to
		// be specific about which output stream to match.
		err := cmd.Run()
		exitcode := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitcode = exitErr.ExitCode()
		}
		if (exitcode != 0) != test.wantExitError {
			want := "zero"
			if test.wantExitError {
				want = "nonzero"
			}
			t.Errorf("%s: got exit code %d, want %s", test.args, exitcode, want)
		}

		out := fmt.Sprintf("stdout:\n%s\nstderr:\n%s\n", cmd.Stdout, cmd.Stderr)
		matched, err := regexp.MatchString("(?s)"+test.wantOut, out)
		if err != nil {
			t.Fatalf("regexp.Match(<<%s>>): %v", test.wantOut, err)
		}
		if !matched {
			t.Errorf("%s: got <<%s>>, want match of regexp <<%s>>", test.args, out, test.wantOut)
		}
	}
}
