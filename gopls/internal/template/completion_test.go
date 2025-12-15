// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"log"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
)

func init() {
	log.SetFlags(log.Lshortfile)
}

type tparse struct {
	marked string   // ^ shows where to ask for completions. (The user just typed the following character.)
	wanted []string // expected completions; nil => no enclosing token
}

// Test completions in templates that parse enough (if completion needs symbols)
// Seen characters up to the ^
func TestParsed(t *testing.T) {
	for _, test := range []tparse{
		{"{{x}}{{12. xx^", nil}, // https://github.com/golang/go/issues/50430
		{`<table class="chroma" data-new-comment-url="{{if $.PageIsPullFiles}}{{$.Issue.HTMLURL}}/files/reviews/new_comment{{else}}{{$.CommitHTML}}/new_comment^{{end}}">`, nil},
		{"{{i^f}}", []string{"index", "if"}},
		{"{{if .}}{{e^ {{end}}", []string{"eq", "end}}", "else", "end"}},
		{"{{foo}}{{f^", []string{"foo"}},
		{"{{$^}}", []string{"$"}},
		{"{{$x:=4}}{{$^", []string{"$x"}},
		{"{{$x:=4}}{{$ ^ ", []string{}},
		{"{{len .Modified}}{{.^Mo", []string{"Modified"}},
		{"{{len .Modified}}{{.mf^", []string{"Modified"}},
		{"{{$^ }}", []string{"$"}},
		{"{{$a =3}}{{$^", []string{"$a"}},
		// .two is not good here: fix someday
		{`{{.Modified}}{{.^{{if $.one.two}}xxx{{end}}`, []string{"Modified", "one", "two"}},
		{`{{.Modified}}{{.o^{{if $.one.two}}xxx{{end}}`, []string{"one"}},
		{"{{.Modiifed}}{{.one.t^{{if $.one.two}}xxx{{end}}", []string{"two"}},
		{`{{block "foo" .}}{{i^`, []string{"index", "if"}},
		{"{{in^{{Internal}}", []string{"index", "Internal", "if"}},
		// simple number has no completions
		{"{{4^e", []string{}},
		// simple string has no completions
		{"{{`e^", []string{}},
		{"{{`No i^", []string{}}, // example of why go/scanner is used
		{"{{xavier}}{{12. x^", []string{"xavier"}},
	} {
		t.Run("", func(t *testing.T) {
			var got []string
			if c := testCompleter(t, test); c != nil {
				ans, _ := c.complete()
				for _, a := range ans.Items {
					got = append(got, a.Label)
				}
			}
			if len(got) != len(test.wanted) {
				t.Fatalf("%q: got %q, wanted %q %d,%d", test.marked, got, test.wanted, len(got), len(test.wanted))
			}
			sort.Strings(test.wanted)
			sort.Strings(got)
			for i := 0; i < len(got); i++ {
				if test.wanted[i] != got[i] {
					t.Fatalf("%q at %d: got %v, wanted %v", test.marked, i, got, test.wanted)
				}
			}
		})
	}
}

func testCompleter(t *testing.T, tx tparse) *completer {
	// seen chars up to ^
	offset := strings.Index(tx.marked, "^")
	buf := strings.Replace(tx.marked, "^", "", 1)
	p := parseBuffer("", []byte(buf))
	if p.parseErr != nil {
		t.Logf("%q: %v", tx.marked, p.parseErr)
	}
	pos, err := p.mapper.OffsetPosition(offset)
	if err != nil {
		t.Fatal(err)
	}

	start, err := enclosingTokenStart(p, pos)
	if err != nil {
		if start == -1 {
			return nil // no enclosing token
		}
		t.Fatal(err)
	}
	syms := make(map[string]symbol)
	filterSyms(syms, p.symbols)
	return &completer{
		p:      p,
		pos:    pos,
		offset: start + len(lbraces),
		ctx:    protocol.CompletionContext{TriggerKind: protocol.Invoked},
		syms:   syms,
	}
}
