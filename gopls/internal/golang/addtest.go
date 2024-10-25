// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file defines the behavior of the "Add test for FUNC" command.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"html/template"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	goplsastutil "golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/internal/typesinternal"
)

const testTmplString = `func {{.TestFuncName}}(t *testing.T) {
  {{- /* Functions/methods input parameters struct declaration. */}}
  {{- if gt (len .Args) 1}}
  type args struct {
  {{- range .Args}}
    {{.Name}} {{.Type}}
  {{- end}}
  }
  {{- end}}
  {{- /* Test cases struct declaration and empty initialization. */}}
  tests := []struct {
    name string // description of this test case
    {{- if gt (len .Args) 1}}
    args args
    {{- end}}
    {{- if eq (len .Args) 1}}
    arg {{(index .Args 0).Type}}
    {{- end}}
    {{- range $index, $res := .Results}}
    {{if eq $index 0}}want{{else}}want{{add $index 1}}{{end}} {{$res.Type}}
    {{- /* TODO(hxjiang): check whether the last return type is error and handle it using field "wantErr". */}}
    {{- end}}
  }{
    // TODO: Add test cases.
  }
  {{- /* Loop over all the test cases. */}}
  for _, tt := range tests {
    {{/* Got variables. */}}
    {{- if .Results}}{{fieldNames .Results ""}} := {{end}}

    {{- /* Call expression. In xtest package test, call function by PACKAGE.FUNC. */}}
    {{- /* TODO(hxjiang): consider any renaming in existing xtest package imports. E.g. import renamedfoo "foo". */}}
    {{- /* TODO(hxjiang): support add test for methods by calling the right constructor. */}}
    {{- if .PackageName}}{{.PackageName}}.{{end}}{{.FuncName}}

    {{- /* Input parameters.  */ -}}
    ({{if eq (len .Args) 1}}tt.arg{{end}}{{if gt (len .Args) 1}}{{fieldNames .Args "tt.args."}}{{end}})

    {{- if .Results}}
    // TODO: update the condition below to compare got with tt.want.
    {{- range $index, $res := .Results}}
    if true {
      t.Errorf("%s: {{$.FuncName}}() = %v, want %v", tt.name, {{.Name}}, tt.{{if eq $index 0}}want{{else}}want{{add $index 1}}{{end}})
    }
    {{- end}}
    {{- end}}
  }
}
`

type field struct {
	Name, Type string
}

type testInfo struct {
	PackageName  string
	FuncName     string
	TestFuncName string
	Args         []field
	Results      []field
}

var testTmpl = template.Must(template.New("test").Funcs(template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"fieldNames": func(fields []field, qualifier string) (res string) {
		var names []string
		for _, f := range fields {
			names = append(names, qualifier+f.Name)
		}
		return strings.Join(names, ", ")
	},
}).Parse(testTmplString))

// AddTestForFunc adds a test for the function enclosing the given input range.
// It creates a _test.go file if one does not already exist.
func AddTestForFunc(ctx context.Context, snapshot *cache.Snapshot, loc protocol.Location) (changes []protocol.DocumentChange, _ error) {
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, loc.URI)
	if err != nil {
		return nil, err
	}

	if errors := pkg.ParseErrors(); len(errors) > 0 {
		return nil, fmt.Errorf("package has parse errors: %v", errors[0])
	}
	if errors := pkg.TypeErrors(); len(errors) > 0 {
		return nil, fmt.Errorf("package has type errors: %v", errors[0])
	}

	// imports is a map from package path to local package name.
	var imports = make(map[string]string)

	var collectImports = func(file *ast.File) error {
		for _, spec := range file.Imports {
			// TODO(hxjiang): support dot imports.
			if spec.Name != nil && spec.Name.Name == "." {
				return fmt.Errorf("\"add a test for FUNC\" does not support files containing dot imports")
			}
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if spec.Name != nil && spec.Name.Name != "_" {
				imports[path] = spec.Name.Name
			} else {
				imports[path] = filepath.Base(path)
			}
		}
		return nil
	}

	// Collect all the imports from the x.go, keep track of the local package name.
	if err := collectImports(pgf.File); err != nil {
		return nil, err
	}

	testBase := strings.TrimSuffix(filepath.Base(loc.URI.Path()), ".go") + "_test.go"
	goTestFileURI := protocol.URIFromPath(filepath.Join(loc.URI.Dir().Path(), testBase))

	testFH, err := snapshot.ReadFile(ctx, goTestFileURI)
	if err != nil {
		return nil, err
	}

	// TODO(hxjiang): use a fresh name if the same test function name already
	// exist.

	var (
		eofRange protocol.Range // empty selection at end of new file
		// edits contains all the text edits to be applied to the test file.
		edits []protocol.TextEdit
		// xtest indicates whether the test file use package x or x_test.
		// TODO(hxjiang): For now, we try to interpret the user's intention by
		// reading the foo_test.go's package name. Instead, we can discuss the option
		// to interpret the user's intention by which function they are selecting.
		// Have one file for x_test package testing, one file for x package testing.
		xtest = true
	)

	if testPGF, err := snapshot.ParseGo(ctx, testFH, parsego.Header); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		changes = append(changes, protocol.DocumentChangeCreate(goTestFileURI))

		// header is the buffer containing the text to add to the beginning of the file.
		var header bytes.Buffer

		// If this test file was created by the gopls, add a copyright header and
		// package decl based on the originating file.
		// Search for something that looks like a copyright header, to replicate
		// in the new file.
		if groups := pgf.File.Comments; len(groups) > 0 {
			// Copyright should appear before package decl and must be the first
			// comment group.
			// Avoid copying any other comment like package doc or directive comment.
			if c := groups[0]; c.Pos() < pgf.File.Package && c != pgf.File.Doc &&
				!isDirective(c.List[0].Text) &&
				strings.Contains(strings.ToLower(c.List[0].Text), "copyright") {
				start, end, err := pgf.NodeOffsets(c)
				if err != nil {
					return nil, err
				}
				header.Write(pgf.Src[start:end])
				// One empty line between copyright header and package decl.
				header.WriteString("\n\n")
			}
		}
		// One empty line between package decl and rest of the file.
		fmt.Fprintf(&header, "package %s_test\n\n", pkg.Types().Name())

		// Write the copyright and package decl to the beginning of the file.
		edits = append(edits, protocol.TextEdit{
			Range:   protocol.Range{},
			NewText: header.String(),
		})
	} else { // existing _test.go file.
		if testPGF.File.Name == nil || testPGF.File.Name.NamePos == token.NoPos {
			return nil, fmt.Errorf("missing package declaration")
		}
		switch testPGF.File.Name.Name {
		case pgf.File.Name.Name:
			xtest = false
		case pgf.File.Name.Name + "_test":
			xtest = true
		default:
			return nil, fmt.Errorf("invalid package declaration %q in test file %q", testPGF.File.Name, testPGF)
		}

		eofRange, err = testPGF.PosRange(testPGF.File.FileEnd, testPGF.File.FileEnd)
		if err != nil {
			return nil, err
		}

		// Collect all the imports from the x_test.go, overwrite the local pakcage
		// name collected from x.go.
		if err := collectImports(testPGF.File); err != nil {
			return nil, err
		}
	}

	// qf qualifier returns the local package name need to use in x_test.go by
	// consulting the consolidated imports map.
	qf := func(p *types.Package) string {
		// When generating test in x packages, any type/function defined in the same
		// x package can emit package name.
		if !xtest && p == pkg.Types() {
			return ""
		}
		if local, ok := imports[p.Path()]; ok {
			return local
		}
		return p.Name()
	}

	// TODO(hxjiang): modify existing imports or add new imports.

	start, end, err := pgf.RangePos(loc.Range)
	if err != nil {
		return nil, err
	}

	path, _ := astutil.PathEnclosingInterval(pgf.File, start, end)
	if len(path) < 2 {
		return nil, fmt.Errorf("no enclosing function")
	}

	decl, ok := path[len(path)-2].(*ast.FuncDecl)
	if !ok {
		return nil, fmt.Errorf("no enclosing function")
	}

	fn := pkg.TypesInfo().Defs[decl.Name].(*types.Func)
	sig := fn.Signature()

	if xtest {
		// Reject if function/method is unexported.
		if !fn.Exported() {
			return nil, fmt.Errorf("cannot add test of unexported function %s to external test package %s_test", decl.Name, pgf.File.Name)
		}

		// Reject if receiver is unexported.
		if sig.Recv() != nil {
			if _, ident, _ := goplsastutil.UnpackRecv(decl.Recv.List[0].Type); !ident.IsExported() {
				return nil, fmt.Errorf("cannot add external test for method %s.%s as receiver type is not exported", ident.Name, decl.Name)
			}
		}
		// TODO(hxjiang): reject if the any input parameter type is unexported.
		// TODO(hxjiang): reject if any return value type is unexported. Explore
		// the option to drop the return value if the type is unexported.
	}

	testName, err := testName(fn)
	if err != nil {
		return nil, err
	}
	data := testInfo{
		FuncName:     fn.Name(),
		TestFuncName: testName,
	}

	if sig.Recv() == nil && xtest {
		data.PackageName = qf(pkg.Types())
	}

	for i := range sig.Params().Len() {
		if i == 0 {
			data.Args = append(data.Args, field{
				Name: "in",
				Type: types.TypeString(sig.Params().At(i).Type(), qf),
			})
		} else {
			data.Args = append(data.Args, field{
				Name: fmt.Sprintf("in%d", i+1),
				Type: types.TypeString(sig.Params().At(i).Type(), qf),
			})
		}
	}

	for i := range sig.Results().Len() {
		if i == 0 {
			data.Results = append(data.Results, field{
				Name: "got",
				Type: types.TypeString(sig.Results().At(i).Type(), qf),
			})
		} else {
			data.Results = append(data.Results, field{
				Name: fmt.Sprintf("got%d", i+1),
				Type: types.TypeString(sig.Results().At(i).Type(), qf),
			})
		}
	}

	var test bytes.Buffer
	if err := testTmpl.Execute(&test, data); err != nil {
		return nil, err
	}

	edits = append(edits, protocol.TextEdit{
		Range:   eofRange,
		NewText: test.String(),
	})

	return append(changes, protocol.DocumentChangeEdit(testFH, edits)), nil
}

// testName returns the name of the function to use for the new function that
// tests fn.
// Returns empty string if the fn is ill typed or nil.
func testName(fn *types.Func) (string, error) {
	if fn == nil {
		return "", fmt.Errorf("input nil function")
	}
	testName := "Test"
	if recv := fn.Signature().Recv(); recv != nil { // method declaration.
		// Retrieve the unpointered receiver type to ensure the test name is based
		// on the topmost alias or named type, not the alias' RHS type (potentially
		// unexported) type.
		// For example:
		// type Foo = foo // Foo is an exported alias for the unexported type foo
		recvType := recv.Type()
		if ptr, ok := recv.Type().(*types.Pointer); ok {
			recvType = ptr.Elem()
		}

		t, ok := recvType.(typesinternal.NamedOrAlias)
		if !ok {
			return "", fmt.Errorf("receiver type is not named type or alias type")
		}

		if !t.Obj().Exported() {
			testName += "_"
		}

		testName += t.Obj().Name() + "_"
	} else if !fn.Exported() { // unexported function declaration.
		testName += "_"
	}
	return testName + fn.Name(), nil
}
