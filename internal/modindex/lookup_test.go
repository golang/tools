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
		// these need to be in alphabetical order by symbol
		{"func Foo() {}", result{"Foo", Func, 0, nil}},
		{"const FooC = 23", result{"FooC", Const, 0, nil}},
		{"func FooF(int, float) error {return nil}", result{"FooF", Func, 1,
			[]Field{{"_", "int"}, {"_", "float"}}}},
		{"type FooT struct{}", result{"FooT", Type, 0, nil}},
		{"var FooV int", result{"FooV", Var, 0, nil}},
		{"func Ⱋoox(x int) {}", result{"Ⱋoox", Func, 0, []Field{{"x", "int"}}}},
	},
}

type result struct {
	name   string
	typ    LexType
	result int
	sig    []Field
}

func okresult(r result, p Candidate) bool {
	if r.name != p.Name || r.typ != p.Type || r.result != int(p.Results) {
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
	log.SetFlags(log.Lshortfile)
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
		// we should have gotten them all
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
