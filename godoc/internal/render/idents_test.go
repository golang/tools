// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package render

import (
	"go/ast"
	"go/doc"
	"testing"
)

func TestResolveIdentifier(t *testing.T) {
	type (
		resolveTest struct {
			in   string
			want string
		}
		declTest struct {
			name  string
			tests []resolveTest
		}
		pkgTest struct {
			pkg     *doc.Package
			related []*doc.Package
			tests   []declTest
		}
	)

	tests := []pkgTest{{
		pkg:     pkgTar,
		related: []*doc.Package{pkgIO, pkgOS, pkgTime},
		tests: []declTest{{
			name: "",
			tests: []resolveTest{
				{`"`, `&#34;`}, // HTML escaping
				{`tar`, `tar`},
				{`blah`, `blah`},
				{`io.EOF`, `<a href="/io">io</a>.<a href="/io#EOF">EOF</a>`},
				{`otherPkg.Identifier`, `otherPkg.Identifier`},
				{`time.Time`, `<a href="/time">time</a>.<a href="/time#Time">Time</a>`},
				{`time.Time.String`, `<a href="/time">time</a>.<a href="/time#Time">Time</a>.<a href="/time#Time.String">String</a>`},
				{`time.NoExist`, `time.NoExist`},
				{`Writer.NoExist`, `<a href="#Writer">Writer</a>.NoExist`},
				{`Writer.WriteHeader`, `<a href="#Writer">Writer</a>.<a href="#Writer.WriteHeader">WriteHeader</a>`},
				{`ErrHeader.Error`, `<a href="#ErrHeader">ErrHeader</a>.Error`}, // Can't link Error method on variable
				{`Format`, `<a href="#Format">Format</a>`},
				{`Writers`, `<a href="#Writer">Writers</a>`},
				{`Write`, `Write`},
			},
		}, {
			name: "Header",
			tests: []resolveTest{
				{`Format`, `<a href="#Header.Format">Format</a>`},                             // Header.Format field takes precedence over Format type
				{`tar.Format`, `<a href="/archive/tar">tar</a>.<a href="#Format">Format</a>`}, // Refers to package level Format type
			},
		}, {
			name: "Reader.Read",
			tests: []resolveTest{
				{`Read`, `<a href="#Reader.Read">Read</a>`},
				{`io.EOF`, `<a href="/io">io</a>.<a href="/io#EOF">EOF</a>`},
				{`Next`, `<a href="#Reader.Next">Next</a>`},
				{`Header.Size`, `<a href="#Header">Header</a>.<a href="#Header.Size">Size</a>`},
				{`TypeLink`, `<a href="#TypeLink">TypeLink</a>`},
				{`tr.WriteTo`, `<a href="#Reader">tr</a>.<a href="#Reader.WriteTo">WriteTo</a>`},
				{`tr.WriteTo.NoExist`, `<a href="#Reader">tr</a>.<a href="#Reader.WriteTo">WriteTo</a>.NoExist`},
				{`tr.NoExist`, `tr.NoExist`},
			},
		}},
	}, {
		pkg: pkgTime,
		tests: []declTest{{
			name: "",
			tests: []resolveTest{
				{`time.Time`, `<a href="/time">time</a>.<a href="#Time">Time</a>`},
				{`Time.MarshalBinary`, `<a href="#Time">Time</a>.<a href="#Time.MarshalBinary">MarshalBinary</a>`},
				{`RFC822`, `<a href="#RFC822">RFC822</a>`},
				{`RFC823`, `RFC823`}, // This constant does not exist
			},
		}, {
			name: "Timer.Reset",
			tests: []resolveTest{
				{`Reset`, `<a href="#Timer.Reset">Reset</a>`},
				{`t.C`, `<a href="#Timer">t</a>.<a href="#Timer.C">C</a>`},
				{`Stop`, `<a href="#Timer.Stop">Stop</a>`},
				{`t.MarshalBinary`, `t.MarshalBinary`}, // MarshalBinary is not a method of Timer
			},
		}},
	}}

	for _, pt := range tests {
		pids := newPackageIDs(pt.pkg, pt.related...)
		t.Run(pt.pkg.Name, func(t *testing.T) {
			for _, dt := range pt.tests {
				dids := newDeclIDs(findDecl(pt.pkg, dt.name))
				t.Run(dt.name, func(t *testing.T) {
					idr := &identifierResolver{pids, dids, nil}
					for i, rt := range dt.tests {
						got := idr.toHTML(rt.in)
						if got != rt.want {
							t.Errorf("test %d, identifierResolver.toHTML():\ngot  `%s`\nwant `%s`", i, got, rt.want)
						}
					}
				})
			}
		})
	}
}

func findDecl(pkg *doc.Package, id string) ast.Decl {
	for _, f := range pkg.Funcs {
		if f.Name == id {
			return f.Decl
		}
	}
	for _, t := range pkg.Types {
		if t.Name == id {
			return t.Decl
		}
		for _, f := range t.Funcs {
			if f.Name == id {
				return f.Decl
			}
		}
		for _, m := range t.Methods {
			if t.Name+"."+m.Name == id {
				return m.Decl
			}
		}
	}
	return nil
}
