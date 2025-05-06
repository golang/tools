package golang

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func TestGroupComment(t *testing.T) {
	cases := []struct {
		src          string
		groupComment []string
	}{
		{
			src: `package test
const (
	A = iota
	B
	C
)
`,
			groupComment: []string{"", "", ""},
		},
		{
			src: `package test
const (
	// doc comment
	A = iota
	B
	C
)
`,
			groupComment: []string{"", "", ""},
		},
		{
			src: `package test
const (
	// doc comment
	/* doc comment */
	A = iota
	B
	C
)
`,
			groupComment: []string{"", "", ""},
		},
		{
			src: `package test
const (
	/* group */
	A = iota
	B
	C
)
`,
			groupComment: []string{"/* group */", "/* group */", "/* group */"},
		},
		{
			src: `package test
const (
	/* group */
	// doc comment
	A = iota // line comment
	B        // line comment
	C        // line comment
)
`,
			groupComment: []string{"/* group */", "/* group */", "/* group */"},
		},
		{
			src: `package test
const (
	/* group */
	A = iota
	B
	C

	D
	E
	F
)
`,
			groupComment: []string{"/* group */", "/* group */", "/* group */", "", "", ""},
		},
		{
			src: `package test
const (
	/* foo */
	A = iota
	C

	/* bar */
	D
	E
)
`,
			groupComment: []string{"foo", "foo", "bar", "bar"},
		},
		{
			src: `package test
const (
	/* foo */
	A = iota
	B
	/* bar */
	D
	E
)
`,
			groupComment: []string{"foo", "foo", "bar", "bar"},
		},
		{
			src: `package test
const (
	/* foo */
	A = iota
	// doc comment
	C

	/* bar */
	D
	// doc comment
	E // line comment
)
`,
			groupComment: []string{"foo", "foo", "bar", "bar"},
		},
		{
			src: `package test
const (
	/* foo */
	A = iota
	B

	/* bar */
	D
	E

	F
)
`,
			groupComment: []string{"foo", "foo", "bar", "bar", ""},
		},
	}

	for i, tt := range cases {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "test.go", tt.src, parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}

		t.Run(strconv.Itoa(i), func(t *testing.T) {
			if testing.Verbose() {
				t.Logf("src:\n%s", tt.src)
			}
			decl := f.Decls[0].(*ast.GenDecl)
			for i, expect := range tt.groupComment {
				gc := specGroupComment(fset, decl, decl.Specs[i])
				var got string
				if gc != nil {
					got = gc.Text
				}
				if !strings.Contains(got, expect) {
					t.Errorf("specGroupComment(%v) = %q; want = %q", i, got, expect)
				}
			}
		})
	}
}
