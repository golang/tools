// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modindex

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tdata struct {
	fname string
	pkg   string
	items []titem
}

type titem struct {
	code   string
	result result
}

var thedata = tdata{
	fname: "cloud.google.com/go/longrunning@v0.4.1/foo.go",
	pkg:   "foo",
	items: []titem{
		// these need to be in alphabetical order
		{"func Foo() {}", result{"Foo", Func, false, 0, nil}},
		{"const FooC = 23", result{"FooC", Const, false, 0, nil}},
		{"func FooF(int, float) error {return nil}", result{"FooF", Func, false, 1,
			[]Field{{"_", "int"}, {"_", "float"}}}},
		{"type FooT struct{}", result{"FooT", Type, false, 0, nil}},
		{"var FooV int", result{"FooV", Var, false, 0, nil}},
		{"func Goo() {}", result{"Goo", Func, false, 0, nil}},
		{"/*Deprecated: too weird\n*/\n// Another Goo\nvar GooVV int", result{"GooVV", Var, true, 0, nil}},
		{"func Ⱋoox(x int) {}", result{"Ⱋoox", Func, false, 0, []Field{{"x", "int"}}}},
	},
}

type result struct {
	name       string
	typ        LexType
	deprecated bool
	result     int
	sig        []Field
}

func okresult(r result, p Candidate) bool {
	if r.name != p.Name || r.typ != p.Type || r.result != int(p.Results) {
		return false
	}
	if r.deprecated != p.Deprecated {
		return false
	}
	if len(r.sig) != len(p.Sig) {
		return false
	}
	for i := 0; i < len(r.sig); i++ {
		if r.sig[i] != p.Sig[i] {
			return false
		}
	}
	return true
}

func TestLookup(t *testing.T) {
	dir := testModCache(t)
	wrtData(t, dir, thedata)
	if _, err := indexModCache(dir, true); err != nil {
		t.Fatal(err)
	}
	ix, err := ReadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Entries) != 1 {
		t.Fatalf("got %d Entries, expected 1", len(ix.Entries))
	}
	// get all the symbols
	p := ix.Lookup("foo", "", true)
	if len(p) != len(thedata.items) {
		t.Errorf("got %d possibilities for pkg foo, expected %d", len(p), len(thedata.items))
	}
	for i, r := range thedata.items {
		if !okresult(r.result, p[i]) {
			t.Errorf("got %#v, expected %#v", p[i], r.result)
		}
	}
	// look for the Foo... and check that each is a Foo...
	p = ix.Lookup("foo", "Foo", true)
	if len(p) != 5 {
		t.Errorf("got %d possibilities for foo.Foo*, expected 5", len(p))
	}
	for _, r := range p {
		if !strings.HasPrefix(r.Name, "Foo") {
			t.Errorf("got %s, expected Foo...", r.Name)
		}
	}
	// fail to find something
	p = ix.Lookup("foo", "FooVal", false)
	if len(p) != 0 {
		t.Errorf("got %d possibilities for foo.FooVal, expected 0", len(p))
	}
	// find an exact match
	p = ix.Lookup("foo", "Foo", false)
	if len(p) != 1 {
		t.Errorf("got %d possibilities for foo.Foo, expected 1", len(p))
	}
	// "Foo" is the first test datum
	if !okresult(thedata.items[0].result, p[0]) {
		t.Errorf("got %#v, expected %#v", p[0], thedata.items[0].result)
	}
}

func wrtData(t *testing.T, dir string, data tdata) {
	t.Helper()
	locname := filepath.FromSlash(data.fname)
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(locname)), 0755); err != nil {
		t.Fatal(err)
	}
	fd, err := os.Create(filepath.Join(dir, locname))
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	fd.WriteString(fmt.Sprintf("package %s\n", data.pkg))
	for _, item := range data.items {
		fd.WriteString(item.code + "\n")
	}
}

func TestLookupAll(t *testing.T) {
	log.SetFlags(log.Lshortfile)
	dir := testModCache(t)
	wrtModule := func(mod string, nms ...string) {
		dname := filepath.Join(dir, mod)
		if err := os.MkdirAll(dname, 0755); err != nil {
			t.Fatal(err)
		}
		fname := filepath.Join(dname, "foo.go")
		fd, err := os.Create(fname)
		if err != nil {
			t.Fatal(err)
		}
		defer fd.Close()
		if _, err := fmt.Fprintf(fd, "package foo\n"); err != nil {
			t.Fatal(err)
		}
		for _, nm := range nms {
			fmt.Fprintf(fd, "func %s() {}\n", nm)
		}
	}
	wrtModule("a.com/go/x4@v1.1.1", "A", "B", "C", "D")
	wrtModule("b.com/go/x3@v1.2.1", "A", "B", "C")
	wrtModule("c.com/go/x5@v1.3.1", "A", "B", "C", "D", "E")

	if _, err := indexModCache(dir, true); err != nil {
		t.Fatal(err)
	}
	ix, err := ReadIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	cands := ix.Lookup("foo", "A", false)
	if len(cands) != 3 {
		t.Errorf("got %d candidates for A, expected 3", len(cands))
	}
	got := ix.LookupAll("foo", "A", "B", "C", "D")
	if len(got) != 2 {
		t.Errorf("got %d candidates for A,B,C,D, expected 2", len(got))
	}
	got = ix.LookupAll("foo", []string{"A", "B", "C", "D", "E"}...)
	if len(got) != 1 {
		t.Errorf("got %d candidates for A,B,C,D,E, expected 1", len(got))
	}
}

func TestUniquify(t *testing.T) {
	var v []string
	for i := 1; i < 4; i++ {
		v = append(v, "A")
		w := uniquify(v)
		if len(w) != 1 {
			t.Errorf("got %d, expected 1", len(w))
		}
	}
	for i := 1; i < 3; i++ {
		v = append(v, "B", "C")
		w := uniquify(v)
		if len(w) != 3 {
			t.Errorf("got %d, expected 3", len(w))
		}
	}
}
