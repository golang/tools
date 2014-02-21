// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package oracle_test

// This file defines a test framework for oracle queries.
//
// The files beneath testdata/src/main contain Go programs containing
// query annotations of the form:
//
//   @verb id "select"
//
// where verb is the query mode (e.g. "callers"), id is a unique name
// for this query, and "select" is a regular expression matching the
// substring of the current line that is the query's input selection.
//
// The expected output for each query is provided in the accompanying
// .golden file.
//
// (Location information is not included because it's too fragile to
// display as text.  TODO(adonovan): think about how we can test its
// correctness, since it is critical information.)
//
// Run this test with:
// 	% go test code.google.com/p/go.tools/oracle -update
// to update the golden files.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"code.google.com/p/go.tools/go/loader"
	"code.google.com/p/go.tools/oracle"
)

var updateFlag = flag.Bool("update", false, "Update the golden files.")

type query struct {
	id       string         // unique id
	verb     string         // query mode, e.g. "callees"
	posn     token.Position // position of of query
	filename string
	queryPos string // value of -pos flag
}

func parseRegexp(text string) (*regexp.Regexp, error) {
	pattern, err := strconv.Unquote(text)
	if err != nil {
		return nil, fmt.Errorf("can't unquote %s", text)
	}
	return regexp.Compile(pattern)
}

// parseQueries parses and returns the queries in the named file.
func parseQueries(t *testing.T, filename string) []*query {
	filedata, err := ioutil.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}

	// Parse the file once to discover the test queries.
	var fset token.FileSet
	f, err := parser.ParseFile(&fset, filename, filedata, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	lines := bytes.Split(filedata, []byte("\n"))

	var queries []*query
	queriesById := make(map[string]*query)

	// Find all annotations of these forms:
	expectRe := regexp.MustCompile(`@([a-z]+)\s+(\S+)\s+(\".*)$`) // @verb id "regexp"
	for _, c := range f.Comments {
		text := strings.TrimSpace(c.Text())
		if text == "" || text[0] != '@' {
			continue
		}
		posn := fset.Position(c.Pos())

		// @verb id "regexp"
		match := expectRe.FindStringSubmatch(text)
		if match == nil {
			t.Errorf("%s: ill-formed query: %s", posn, text)
			continue
		}

		id := match[2]
		if prev, ok := queriesById[id]; ok {
			t.Errorf("%s: duplicate id %s", posn, id)
			t.Errorf("%s: previously used here", prev.posn)
			continue
		}

		q := &query{
			id:       id,
			verb:     match[1],
			filename: filename,
			posn:     posn,
		}

		if match[3] != `"nopos"` {
			selectRe, err := parseRegexp(match[3])
			if err != nil {
				t.Errorf("%s: %s", posn, err)
				continue
			}

			// Find text of the current line, sans query.
			// (Queries must be // not /**/ comments.)
			line := lines[posn.Line-1][:posn.Column-1]

			// Apply regexp to current line to find input selection.
			loc := selectRe.FindIndex(line)
			if loc == nil {
				t.Errorf("%s: selection pattern %s doesn't match line %q",
					posn, match[3], string(line))
				continue
			}

			// Assumes ASCII. TODO(adonovan): test on UTF-8.
			linestart := posn.Offset - (posn.Column - 1)

			// Compute the file offsets.
			q.queryPos = fmt.Sprintf("%s:#%d,#%d",
				filename, linestart+loc[0], linestart+loc[1])
		}

		queries = append(queries, q)
		queriesById[id] = q
	}

	// Return the slice, not map, for deterministic iteration.
	return queries
}

// WriteResult writes res (-format=plain) to w, stripping file locations.
func WriteResult(w io.Writer, res *oracle.Result) {
	capture := new(bytes.Buffer) // capture standard output
	res.WriteTo(capture)
	for _, line := range strings.Split(capture.String(), "\n") {
		// Remove a "file:line: " prefix.
		if i := strings.Index(line, ": "); i >= 0 {
			line = line[i+2:]
		}
		fmt.Fprintf(w, "%s\n", line)
	}
}

// doQuery poses query q to the oracle and writes its response and
// error (if any) to out.
func doQuery(out io.Writer, q *query, useJson bool) {
	fmt.Fprintf(out, "-------- @%s %s --------\n", q.verb, q.id)

	var buildContext = build.Default
	buildContext.GOPATH = "testdata"
	res, err := oracle.Query([]string{q.filename},
		q.verb,
		q.queryPos,
		nil, // ptalog,
		&buildContext,
		true) // reflection
	if err != nil {
		fmt.Fprintf(out, "\nError: %s\n", err)
		return
	}

	if useJson {
		// JSON output
		b, err := json.MarshalIndent(res.Serial(), "", "\t")
		if err != nil {
			fmt.Fprintf(out, "JSON error: %s\n", err.Error())
			return
		}
		out.Write(b)
	} else {
		// "plain" (compiler diagnostic format) output
		WriteResult(out, res)
	}
}

func TestOracle(t *testing.T) {
	switch runtime.GOOS {
	case "windows":
		t.Skipf("skipping test on %q (no /usr/bin/diff)", runtime.GOOS)
	}

	for _, filename := range []string{
		"testdata/src/main/calls.go",
		"testdata/src/main/callgraph.go",
		"testdata/src/main/callgraph2.go",
		"testdata/src/main/describe.go",
		"testdata/src/main/freevars.go",
		"testdata/src/main/implements.go",
		"testdata/src/main/imports.go",
		"testdata/src/main/peers.go",
		"testdata/src/main/pointsto.go",
		"testdata/src/main/reflection.go",
		"testdata/src/main/what.go",
		// JSON:
		// TODO(adonovan): most of these are very similar; combine them.
		"testdata/src/main/callgraph-json.go",
		"testdata/src/main/calls-json.go",
		"testdata/src/main/peers-json.go",
		"testdata/src/main/describe-json.go",
		"testdata/src/main/implements-json.go",
		"testdata/src/main/pointsto-json.go",
		"testdata/src/main/referrers-json.go",
		"testdata/src/main/what-json.go",
	} {
		useJson := strings.HasSuffix(filename, "-json.go")
		queries := parseQueries(t, filename)
		golden := filename + "lden"
		got := filename + "t"
		gotfh, err := os.Create(got)
		if err != nil {
			t.Errorf("Create(%s) failed: %s", got, err)
			continue
		}
		defer gotfh.Close()

		// Run the oracle on each query, redirecting its output
		// and error (if any) to the foo.got file.
		for _, q := range queries {
			doQuery(gotfh, q, useJson)
		}

		// Compare foo.got with foo.golden.
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "plan9":
			cmd = exec.Command("/bin/diff", "-c", golden, got)
		default:
			cmd = exec.Command("/usr/bin/diff", "-u", golden, got)
		}
		buf := new(bytes.Buffer)
		cmd.Stdout = buf
		if err := cmd.Run(); err != nil {
			t.Errorf("Oracle tests for %s failed: %s.\n%s\n",
				filename, err, buf)

			if *updateFlag {
				t.Logf("Updating %s...", golden)
				if err := exec.Command("/bin/cp", got, golden).Run(); err != nil {
					t.Errorf("Update failed: %s", err)
				}
			}
		}
	}
}

func TestMultipleQueries(t *testing.T) {
	// Loader
	var buildContext = build.Default
	buildContext.GOPATH = "testdata"
	conf := loader.Config{Build: &buildContext, SourceImports: true}
	filename := "testdata/src/main/multi.go"
	conf.CreateFromFilenames("", filename)
	iprog, err := conf.Load()
	if err != nil {
		t.Fatalf("Load failed: %s", err)
	}

	// Oracle
	o, err := oracle.New(iprog, nil, true)
	if err != nil {
		t.Fatalf("oracle.New failed: %s", err)
	}

	// QueryPos
	pos := filename + ":#54,#58"
	qpos, err := oracle.ParseQueryPos(iprog, pos, true)
	if err != nil {
		t.Fatalf("oracle.ParseQueryPos(%q) failed: %s", pos, err)
	}
	// SSA is built and we have the QueryPos.
	// Release the other ASTs and type info to the GC.
	iprog = nil

	// Run different query modes on same scope and selection.
	out := new(bytes.Buffer)
	for _, mode := range [...]string{"callers", "describe", "freevars"} {
		res, err := o.Query(mode, qpos)
		if err != nil {
			t.Errorf("(*oracle.Oracle).Query(%q) failed: %s", pos, err)
		}
		WriteResult(out, res)
	}
	want := `multi.f is called from these 1 sites:
	static function call from multi.main

function call (or conversion) of type ()

Free identifiers:
var x int

`
	if got := out.String(); got != want {
		t.Errorf("Query output differs; want <<%s>>, got <<%s>>\n", want, got)
	}
}
