// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vetfile

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestParseDecl(t *testing.T) {
	for _, test := range []struct {
		text string // a go.vet file containing (at most) one declaration
		want string // formatted first decl (or "" if empty) on success; or substring of expected error
	}{
		// successful single declarations
		{text: "enable . example.com/c.A", want: "enable . example.com/c.A"},
		{text: "enable ./... example.com/c.A", want: "enable ./... example.com/c.A"},
		{text: "enable importers example.com/c.A", want: "enable importers example.com/c.A"},
		{text: "enable\t.\texample.com/c.A", want: "enable . example.com/c.A"},
		{text: "disable . example.com/c.A", want: "disable . example.com/c.A"},
		{text: "disable ./... example.com/c.A", want: "disable ./... example.com/c.A"},
		{text: "enable . example.com/c.Suite[*]", want: "enable . example.com/c.Suite[*]"},
		{text: "enable . example.com/c.Suite[A,B]", want: "enable . example.com/c.Suite[A,B]"},
		{text: "enable . example.com/c.Suite [A, B]", want: "enable . example.com/c.Suite[A,B]"},
		{text: "disable . example.com/c.Suite[A]", want: "disable . example.com/c.Suite[A]"},
		{text: "enable . fmt/internal/x.A", want: "enable . fmt/internal/x.A"},
		{text: "enable . example.com/a.b/c.A", want: "enable . example.com/a.b/c.A"},
		{text: "enable . example.com/c.A // comment", want: "enable . example.com/c.A"},
		{text: "enable (\n\t. example.com/c.A\n)", want: "enable . example.com/c.A"},
		{text: `disable . "printf"`, want: "disable . \"printf\""},
		{text: "disable . `printf`", want: "disable . \"printf\""},
		{text: `disable ./... "printf"`, want: "disable ./... \"printf\""},
		{text: "enable . example.com/c.S[A] ", want: "enable . example.com/c.S[A]"},

		// empty files
		{text: "// enable . example.com/c.A", want: ""},
		{text: "", want: ""},

		// errors
		{text: "enable", want: "malformed enable declaration"},
		{text: "enable .", want: "malformed enable declaration: want scope and reference"},
		{text: "enable . a.B c", want: `malformed enable declaration: extra operand "c"`},
		{text: "disable", want: "malformed disable declaration"},
		{text: "enable everywhere example.com/c.A", want: `invalid scope "everywhere" in enable declaration`},
		{text: "enable .. example.com/c.A", want: `invalid scope ".." in enable declaration`},
		{text: "disable importers example.com/c.A", want: `disable does not accept scope "importers"`},
		{text: "disable everywhere example.com/c.A", want: `invalid scope "everywhere" in disable declaration`},
		{text: "enable . A", want: "want qualified name pkgpath.Var"},
		{text: `enable . "printf"`, want: "analyzer names may be used only in disable"},
		{text: `enable importers "printf"`, want: "analyzer names may be used only in disable"},
		{text: `disable importers "printf"`, want: `disable does not accept scope "importers"`},
		{text: "disable . printf[A]", want: "want qualified name pkgpath.Var"},
		{text: "disable . print-f", want: "want qualified name pkgpath.Var"},
		{text: "disable . printf", want: `want qualified name pkgpath.Var (to disable analyzers by name, quote it: "printf")`},
		{text: `disable . "print-f"`, want: "not an identifier"},
		{text: `disable . "printf"[A]`, want: "want a quoted identifier"},
		{text: "enable . .A", want: "empty package path"},
		{text: "enable . example.com/c.a", want: `"a" is not an exported identifier`},
		{text: "enable . example.com/c.", want: `"" is not an exported identifier`},
		{text: "enable . example.com/c.A-B", want: `"A-B" is not an exported identifier`},
		{text: "enable . example.com/c.S[", want: "missing ']'"},
		{text: "enable . example.com/c.S[A", want: "missing ']'"},
		{text: "enable . example.com/c.S[]", want: "empty member name"},
		{text: "enable . example.com/c.S[A,]", want: "empty member name"},
		{text: "enable . example.com/c.S[,A]", want: "empty member name"},
		{text: "enable . example.com/c.S[A,,B]", want: "empty member name"},
		{text: "enable . example.com/c.S[A,A]", want: `duplicate member name "A"`},
		{text: "enable . example.com/c.S[A,*]", want: `invalid member name "*"`},
		{text: "enable . example.com/c.S[1]", want: `invalid member name "1"`},
		{text: "enable . example.com/c.S[A]x", want: "missing ']'"},
		{text: "enable . example com/c.A", want: "malformed enable declaration"},
		{text: "enable . /abs.A", want: "invalid reference"},
		{text: `enable . "example.com/c.A"`, want: "invalid reference"},
		{text: "enabler . example.com/c.A", want: "unknown directive: enabler"},
		{text: "frobnicate (\n\t. example.com/c.A\n)", want: "unknown block type: frobnicate"},
		{text: "enable . example.com/c.A /* comment */", want: "mod files must use // comments"},
	} {
		file := Parse("go.vet", []byte(test.text))
		if len(file.Diagnostics) > 0 {
			got := file.Diagnostics[0].String()
			if !strings.Contains(got, test.want) {
				t.Errorf("Parse(%q) = %#q, want substring %q", test.text, got, test.want)
			}
			continue
		}
		if len(file.Decls) != 1 {
			if test.want != "" {
				t.Errorf("Parse(%q) returned %d declarations (want 1)", test.text, len(file.Decls))
			}
			continue
		}
		got := file.Decls[0].String()
		if got != test.want {
			t.Errorf("Parse(%q) = %#q, want %#q", test.text, got, test.want)
		}
	}
}

func TestParse(t *testing.T) {
	const src = `// Analyzers for this tree.

enable . example.com/c.A
enable bad example.com/c.A
// A doc comment.
disable ./... example.com/c.S[X]

enable (
	importers example.com/c.B
	./...     example.com/c.C // suffix comment
)
`
	f := Parse("dir/go.vet", []byte(src))
	var got []string
	for _, d := range f.Decls {
		got = append(got, d.Position()+" "+d.String())
	}
	want := []string{
		"dir/go.vet:3:1 enable . example.com/c.A",
		"dir/go.vet:6:1 disable ./... example.com/c.S[X]",
		"dir/go.vet:9:2 enable importers example.com/c.B",
		"dir/go.vet:10:2 enable ./... example.com/c.C",
	}
	if !slices.Equal(got, want) {
		t.Errorf("references = %q, want %q", got, want)
	}
	if diags := f.Diagnostics; len(diags) != 1 || !strings.HasPrefix(diags[0].String(), "dir/go.vet:4:1: invalid scope \"bad\"") {
		t.Errorf("diagnostics = %v", diags)
	}
}

func TestModuleRoot(t *testing.T) {
	for _, test := range []struct {
		dir, pkg, mod, want string
	}{
		{"/m", "example.com/m", "example.com/m", "/m"},
		{"/m/a/b", "example.com/m/a/b", "example.com/m", "/m"},
		{"/gopath/pkg/mod/example.com/m@v1.0.0/a", "example.com/m/a", "example.com/m", "/gopath/pkg/mod/example.com/m@v1.0.0"},
		{"/w/vendor/example.com/m/a", "example.com/m/a", "example.com/m", "/w/vendor/example.com/m"},
		{"/replacement/a", "example.com/m/a", "example.com/m", "/replacement"},
		{"/m/a", "example.com/mx/a", "example.com/m", ""}, // not within module
		{"/m/b", "example.com/m/a", "example.com/m", ""},  // dir does not match package
	} {
		dir := filepath.FromSlash(test.dir)
		got := moduleRoot(dir, test.pkg, test.mod)
		if want := filepath.FromSlash(test.want); got != want {
			t.Errorf("moduleRoot(%q, %q, %q) = %q, want %q", dir, test.pkg, test.mod, got, want)
		}
	}
}

func TestDirs(t *testing.T) {
	gorootSrc := filepath.FromSlash("/goroot/src")
	// Directories beneath $GOROOT/src are relative to gorootSrc.
	fromSlash := func(dir string) string {
		if rest, ok := strings.CutPrefix(dir, "$GOROOT/src"); ok {
			return filepath.Join(gorootSrc, filepath.FromSlash(rest))
		}
		return filepath.FromSlash(dir)
	}
	for _, test := range []struct {
		pkgdir, pkgPath, modPath string
		goroot                   bool
		want                     []string // "dir=path"
	}{
		{"/m", "example.com/m", "example.com/m", false, []string{"/m=example.com/m"}},
		{"/m/a/b", "example.com/m/a/b", "example.com/m", false, []string{"/m/a/b=example.com/m/a/b", "/m/a=example.com/m/a", "/m=example.com/m"}},
		{"/m/a/testdata/b", "example.com/m/a/testdata/b", "example.com/m", false, []string{"/m/a/testdata/b=example.com/m/a/testdata/b", "/m/a/testdata=example.com/m/a/testdata"}},
		{"/m/_x/b", "example.com/m/_x/b", "example.com/m", false, []string{"/m/_x/b=example.com/m/_x/b", "/m/_x=example.com/m/_x"}},
		{"/w/vendor/example.com/m/a", "example.com/m/a", "example.com/m", false, []string{"/w/vendor/example.com/m/a=example.com/m/a", "/w/vendor/example.com/m=example.com/m"}},
		{"/w/vendor/example.com/m/a", "example.com/w/vendor/example.com/m/a", "example.com/w", false, []string{"/w/vendor/example.com/m/a=example.com/w/vendor/example.com/m/a", "/w/vendor/example.com/m=example.com/w/vendor/example.com/m", "/w/vendor/example.com=example.com/w/vendor/example.com", "/w/vendor=example.com/w/vendor"}},
		{"$GOROOT/src/vendor/golang.org/x/a", "vendor/golang.org/x/a", "", true, []string{"$GOROOT/src/vendor/golang.org/x/a=vendor/golang.org/x/a", "$GOROOT/src/vendor/golang.org/x=vendor/golang.org/x", "$GOROOT/src/vendor/golang.org=vendor/golang.org", "$GOROOT/src/vendor=vendor", "$GOROOT/src="}},
		{"$GOROOT/src/a/testdata/b", "a/testdata/b", "", true, []string{"$GOROOT/src/a/testdata/b=a/testdata/b", "$GOROOT/src/a/testdata=a/testdata"}},
		{"$GOROOT/src/cmd/go/internal/load", "cmd/go/internal/load", "cmd", true, []string{"$GOROOT/src/cmd/go/internal/load=cmd/go/internal/load", "$GOROOT/src/cmd/go/internal=cmd/go/internal", "$GOROOT/src/cmd/go=cmd/go", "$GOROOT/src/cmd=cmd"}},
		{"$GOROOT/src/cmd/vendor/golang.org/x/mod", "golang.org/x/mod", "golang.org/x/mod", true, []string{"$GOROOT/src/cmd/vendor/golang.org/x/mod=cmd/vendor/golang.org/x/mod", "$GOROOT/src/cmd/vendor/golang.org/x=cmd/vendor/golang.org/x", "$GOROOT/src/cmd/vendor/golang.org=cmd/vendor/golang.org", "$GOROOT/src/cmd/vendor=cmd/vendor", "$GOROOT/src/cmd=cmd"}},
		{"/m/b", "example.com/m/a", "example.com/m", false, nil}, // dir does not match package
		{"/m", "example.com/m", "", false, nil},                  // no module
	} {
		var want []string
		for _, d := range test.want {
			dir, path, _ := strings.Cut(d, "=")
			want = append(want, fromSlash(dir)+"="+path)
		}
		var got []string
		for dir, path := range Dirs(fromSlash(test.pkgdir), test.pkgPath, test.modPath, test.goroot, gorootSrc) {
			got = append(got, dir+"="+path)
		}
		if !slices.Equal(got, want) {
			t.Errorf("Dirs(%q, %q, %q, %t) = %q, want %q", test.pkgdir, test.pkgPath, test.modPath, test.goroot, got, want)
		}
	}
}

func TestVisible(t *testing.T) {
	for _, test := range []struct {
		from    string
		fromStd bool
		pkg     string
		want    bool
	}{
		{"example.com/m/x", false, "example.com/c", true},                        // not internal
		{"example.com/m", false, "example.com/m/internal/c", true},               // parent itself
		{"example.com/m/x/y", false, "example.com/m/internal/c", true},           // beneath parent
		{"example.com/mx", false, "example.com/m/internal/c", false},             // not a path prefix
		{"example.com/other", false, "example.com/m/internal/c", false},          // elsewhere
		{"example.com/m/p/q", false, "example.com/m/p/internal", true},           // .../internal
		{"example.com/m/q", false, "example.com/m/p/internal", false},            //
		{"example.com/m/p", false, "example.com/m/internal/a/internal/b", false}, // final internal governs
		{"example.com/m/internal/a/x", false, "example.com/m/internal/a/internal/b", true},
		{"fmt", true, "internal/vet/printf", true},             // std top-level internal, from std
		{"", true, "internal/vet/printf", true},                // GOROOT/src itself
		{"example.com/m", false, "internal/vet/printf", false}, // ...from user code
		{"fmt", true, "fmt/internal/zz", true},                 //
		{"log", true, "fmt/internal/zz", false},                // std, but outside fmt
		{"cmd/go", true, "cmd/internal/x", true},               //
		{"fmt", true, "cmd/internal/x", false},                 //
		{"fmt", true, "internal", true},                        // top-level internal itself
		{"example.com/m", false, "internal", false},            //
	} {
		if got := Visible(test.from, test.fromStd, test.pkg); got != test.want {
			t.Errorf("Visible(%q, %t, %q) = %t, want %t", test.from, test.fromStd, test.pkg, got, test.want)
		}
	}
}

func TestDiagnostic(t *testing.T) {
	a := &File{Filename: "a/go.vet"}
	b := &File{Filename: "b/go.vet"}
	for _, test := range []struct {
		diag Diagnostic
		want string
	}{
		{Diagnostic{File: a, Pos: modfile.Position{Line: 1, LineRune: 1}, Message: "x"}, "a/go.vet:1:1: x"},
		{Diagnostic{File: a, Pos: modfile.Position{Line: 2, LineRune: 3, Byte: 20}, Message: "y"}, "a/go.vet:2:3: y"},
		{Diagnostic{File: b, Message: "z"}, "b/go.vet: z"}, // whole file
	} {
		if got := test.diag.String(); got != test.want {
			t.Errorf("String() = %q, want %q", got, test.want)
		}
	}
}
