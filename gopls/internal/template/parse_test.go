// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import "testing"

func TestSymbols(t *testing.T) {
	for i, test := range []struct {
		buf       string
		wantNamed int      // expected number of named templates
		syms      []string // expected symbols (start, len, name, kind, def?)
	}{
		{`
{{if (foo .X.Y)}}{{$A := "hi"}}{{.Z $A}}{{else}}
{{$A.X 12}}
{{foo (.X.Y) 23 ($A.Zü)}}
{{end}}`, 1, []string{
			"{7,3,foo,Function,false}",
			"{12,1,X,Method,false}",
			"{14,1,Y,Method,false}",
			"{21,2,$A,Variable,true}",
			"{26,4,,String,false}",
			"{35,1,Z,Method,false}",
			"{38,2,$A,Variable,false}",
			"{53,2,$A,Variable,false}",
			"{56,1,X,Method,false}",
			"{57,2,,Number,false}",
			"{64,3,foo,Function,false}",
			"{70,1,X,Method,false}",
			"{72,1,Y,Method,false}",
			"{75,2,,Number,false}",
			"{80,2,$A,Variable,false}",
			"{83,3,Zü,Method,false}",
			"{94,3,,Constant,false}",
		}},
		{`{{define "zzz"}}{{.}}{{end}}
{{template "zzz"}}`, 2, []string{
			"{10,3,zzz,Namespace,true}",
			"{18,1,dot,Variable,false}",
			"{41,3,zzz,Package,false}",
		}},
		{`{{block "aaa" foo}}b{{end}}`, 2, []string{
			"{9,3,aaa,Namespace,true}",
			"{9,3,aaa,Package,false}",
			"{14,3,foo,Function,false}",
			"{19,1,,Constant,false}",
		}},
		{"", 0, nil},
		{`{{/* this is
a comment */}}`, 1, nil}, // https://go.dev/issue/74635
	} {
		got := parseBuffer("", []byte(test.buf))
		if got.parseErr != nil {
			t.Error(got.parseErr)
			continue
		}
		if len(got.named) != test.wantNamed {
			t.Errorf("%d: got %d, expected %d", i, len(got.named), test.wantNamed)
		}
		for n, s := range got.symbols {
			if s.String() != test.syms[n] {
				t.Errorf("%d: got %s, expected %s", i, s.String(), test.syms[n])
			}
		}
	}
}

func TestWordAt(t *testing.T) {
	want := []string{"", "", "$A", "$A", "", "", "", "", "", "",
		"", "", "", "if", "if", "", "$A", "$A", "", "",
		"B", "", "", "end", "end", "end", "", "", ""}
	buf := []byte("{{$A := .}}{{if $A}}B{{end}}")
	for i := range buf {
		got := wordAt(buf, i)
		if got != want[i] {
			t.Errorf("for %d, got %q, wanted %q", i, got, want[i])
		}
	}
}

func TestQuotes(t *testing.T) {
	for _, s := range []struct {
		tmpl      string
		tokCnt    int
		elidedCnt int8
	}{
		{"{{- /*comment*/ -}}", 1, 0},
		{"{{/*`\ncomment\n`*/}}", 1, 0},
		//{"{{foo\nbar}}\n", 1, 0}, // this action spanning lines parses in 1.16
		{"{{\"{{foo}}{{\"}}", 1, 0},
		{"{{\n{{- when}}", 1, 1},          // corrected
		{"{{{{if .}}xx{{\n{{end}}", 2, 2}, // corrected
	} {
		p := parseBuffer("", []byte(s.tmpl))
		if len(p.tokens) != s.tokCnt {
			t.Errorf("%#v: got %d tokens, expected %d", s, len(p.tokens), s.tokCnt)
		}
		if p.parseErr != nil {
			t.Errorf("%q: %v", string(p.buf), p.parseErr)
		}
		if len(p.elided) != int(s.elidedCnt) {
			t.Errorf("%#v: elided %d, expected %d", s, len(p.elided), s.elidedCnt)
		}
	}
}
