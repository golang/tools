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
	"unicode"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/protocol"
	goplsastutil "golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/internal/typesinternal"
)

const testTmplString = `func {{.TestFuncName}}(t *testing.T) {
  {{- /* Constructor input parameters struct declaration. */}}
  {{- if and .Receiver .Receiver.Constructor}}
  {{- if gt (len .Receiver.Constructor.Args) 1}}
  type constructorArgs struct {
  {{- range .Receiver.Constructor.Args}}
    {{.Name}} {{.Type}}
  {{- end}}
  }
  {{- end}}
  {{- end}}

  {{- /* Functions/methods input parameters struct declaration. */}}
  {{- if gt (len .Func.Args) 1}}
  type args struct {
  {{- range .Func.Args}}
    {{.Name}} {{.Type}}
  {{- end}}
  }
  {{- end}}

  {{- /* Test cases struct declaration and empty initialization. */}}
  tests := []struct {
    name string // description of this test case
    {{- if and .Receiver .Receiver.Constructor}}
    {{- if gt (len .Receiver.Constructor.Args) 1}}
    constructorArgs constructorArgs
    {{- end}}
    {{- if eq (len .Receiver.Constructor.Args) 1}}
    constructorArg {{(index .Receiver.Constructor.Args 0).Type}}
    {{- end}}
    {{- end}}

    {{- if gt (len .Func.Args) 1}}
    args args
    {{- end}}
    {{- if eq (len .Func.Args) 1}}
    arg {{(index .Func.Args 0).Type}}
    {{- end}}
    {{- range $index, $res := .Func.Results}}
    {{- if eq $res.Name "gotErr"}}
    wantErr bool
    {{- else if eq $index 0}}
    want {{$res.Type}}
    {{- else}}
    want{{add $index 1}} {{$res.Type}}
    {{- end}}
    {{- end}}
  }{
    // TODO: Add test cases.
  }

  {{- /* Loop over all the test cases. */}}
  for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
      {{- /* Constructor or empty initialization. */}}
      {{- if .Receiver}}
      {{- if .Receiver.Constructor}}
      {{- /* Receiver variable by calling constructor. */}}
      {{fieldNames .Receiver.Constructor.Results ""}} := {{if .PackageName}}{{.PackageName}}.{{end}}
      {{- .Receiver.Constructor.Name}}

      {{- /* Constructor input parameters. */ -}}
      ({{- if eq (len .Receiver.Constructor.Args) 1}}tt.constructorArg{{end}}{{if gt (len .Func.Args) 1}}{{fieldNames .Receiver.Constructor.Args "tt.constructorArgs."}}{{end}})

      {{- /* Handles the error return from constructor. */}}
      {{- $last := last .Receiver.Constructor.Results}}
      {{- if eq $last.Type "error"}}
      if err != nil {
        t.Fatalf("could not contruct receiver type: %v", err)
      }
      {{- end}}
      {{- else}}
      {{- /* Receiver variable declaration. */}}
      // TODO: construct the receiver type.
      var {{.Receiver.Var.Name}} {{.Receiver.Var.Type}}
      {{- end}}
      {{- end}}

      {{- /* Got variables. */}}
      {{if .Func.Results}}{{fieldNames .Func.Results ""}} := {{end}}

      {{- /* Call expression. */}}
      {{- if .Receiver}}{{/* Call method by VAR.METHOD. */}}
      {{- .Receiver.Var.Name}}.
      {{- else if .PackageName}}{{/* Call function by PACKAGE.FUNC. */}}
      {{- .PackageName}}.
      {{- end}}{{.Func.Name}}

      {{- /* Input parameters. */ -}}
      ({{- if eq (len .Func.Args) 1}}tt.arg{{end}}{{if gt (len .Func.Args) 1}}{{fieldNames .Func.Args "tt.args."}}{{end}})

      {{- /* Handles the returned error before the rest of return value. */}}
      {{- $last := last .Func.Results}}
      {{- if eq $last.Type "error"}}
      if gotErr != nil {
        if !tt.wantErr {
          t.Errorf("{{$.Func.Name}}() failed: %v", gotErr)
        }
        return
      }
      if tt.wantErr {
        t.Fatal("{{$.Func.Name}}() succeeded unexpectedly")
      }
      {{- end}}

      {{- /* Compare the returned values except for the last returned error. */}}
      {{- if or (and .Func.Results (ne $last.Type "error")) (and (gt (len .Func.Results) 1) (eq $last.Type "error"))}}
      // TODO: update the condition below to compare got with tt.want.
      {{- range $index, $res := .Func.Results}}
      {{- if ne $res.Name "gotErr"}}
      if true {
        t.Errorf("{{$.Func.Name}}() = %v, want %v", {{.Name}}, tt.{{if eq $index 0}}want{{else}}want{{add $index 1}}{{end}})
      }
      {{- end}}
      {{- end}}
      {{- end}}
    })
  }
}
`

type field struct {
	Name, Type string
}

type function struct {
	Name    string
	Args    []field
	Results []field
}

type receiver struct {
	// Var is the name and type of the receiver variable.
	Var field
	// Constructor holds information about the constructor for the receiver type.
	// If no qualified constructor is found, this field will be nil.
	Constructor *function
}

type testInfo struct {
	PackageName  string
	TestFuncName string
	// Func holds information about the function or method being tested.
	Func function
	// Receiver holds information about the receiver of the function or method
	// being tested.
	// This field is nil for functions and non-nil for methods.
	Receiver *receiver
}

var testTmpl = template.Must(template.New("test").Funcs(template.FuncMap{
	"add": func(a, b int) int { return a + b },
	"last": func(slice []field) field {
		return slice[len(slice)-1]
	},
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
		PackageName:  qf(pkg.Types()),
		TestFuncName: testName,
		Func: function{
			Name: fn.Name(),
		},
	}

	errorType := types.Universe.Lookup("error").Type()

	// TODO(hxjiang): if input parameter is not named (meaning it's not used),
	// pass the zero value to the function call.
	// TODO(hxjiang): if the input parameter is named, define the field by using
	// the parameter's name instead of in%d.
	// TODO(hxjiang): handle special case for ctx.Context input.
	for index := range sig.Params().Len() {
		var name string
		if index == 0 {
			name = "in"
		} else {
			name = fmt.Sprintf("in%d", index+1)
		}
		data.Func.Args = append(data.Func.Args, field{
			Name: name,
			Type: types.TypeString(sig.Params().At(index).Type(), qf),
		})
	}

	for index := range sig.Results().Len() {
		var name string
		if index == sig.Results().Len()-1 && types.Identical(sig.Results().At(index).Type(), errorType) {
			name = "gotErr"
		} else if index == 0 {
			name = "got"
		} else {
			name = fmt.Sprintf("got%d", index+1)
		}
		data.Func.Results = append(data.Func.Results, field{
			Name: name,
			Type: types.TypeString(sig.Results().At(index).Type(), qf),
		})
	}

	if sig.Recv() != nil {
		// Find the preferred type for the receiver. We don't use
		// typesinternal.ReceiverNamed here as we want to preserve aliases.
		recvType := sig.Recv().Type()
		if ptr, ok := recvType.(*types.Pointer); ok {
			recvType = ptr.Elem()
		}

		t, ok := recvType.(typesinternal.NamedOrAlias)
		if !ok {
			return nil, fmt.Errorf("the receiver type is neither named type nor alias type")
		}

		var varName string
		{
			var possibleNames []string // list of candidates, preferring earlier entries.
			if len(sig.Recv().Name()) > 0 {
				possibleNames = append(possibleNames,
					sig.Recv().Name(),            // receiver name.
					string(sig.Recv().Name()[0]), // first character of receiver name.
				)
			}
			possibleNames = append(possibleNames,
				string(t.Obj().Name()[0]), // first character of receiver type name.
			)
			if len(t.Obj().Name()) >= 2 {
				possibleNames = append(possibleNames,
					string(t.Obj().Name()[:2]), // first two character of receiver type name.
				)
			}
			var camelCase []rune
			for i, s := range t.Obj().Name() {
				if i == 0 || unicode.IsUpper(s) {
					camelCase = append(camelCase, s)
				}
			}
			possibleNames = append(possibleNames,
				string(camelCase), // captalized initials.
			)
			for _, name := range possibleNames {
				name = strings.ToLower(name)
				if name == "" || name == "t" || name == "tt" {
					continue
				}
				varName = name
				break
			}
			if varName == "" {
				varName = "r" // default as "r" for "receiver".
			}
		}

		data.Receiver = &receiver{
			Var: field{
				Name: varName,
				Type: types.TypeString(recvType, qf),
			},
		}

		// constructor is the selected constructor for type T.
		var constructor *types.Func

		// When finding the qualified constructor, the function should return the
		// any type whose named type is the same type as T's named type.
		_, wantType := typesinternal.ReceiverNamed(sig.Recv())
		for _, name := range pkg.Types().Scope().Names() {
			f, ok := pkg.Types().Scope().Lookup(name).(*types.Func)
			if !ok {
				continue
			}
			if f.Signature().Recv() != nil {
				continue
			}
			// Unexported constructor is not visible in x_test package.
			if xtest && !f.Exported() {
				continue
			}
			// Only allow constructors returning T, T, (T, error), or (T, error).
			if f.Signature().Results().Len() > 2 || f.Signature().Results().Len() == 0 {
				continue
			}

			_, gotType := typesinternal.ReceiverNamed(f.Signature().Results().At(0))
			if gotType == nil || !types.Identical(gotType, wantType) {
				continue
			}

			if f.Signature().Results().Len() == 2 && !types.Identical(f.Signature().Results().At(1).Type(), errorType) {
				continue
			}

			if constructor == nil {
				constructor = f
			}

			// Functions named NewType are prioritized as constructors over other
			// functions that match only the signature criteria.
			if strings.EqualFold(strings.ToLower(f.Name()), strings.ToLower("new"+t.Obj().Name())) {
				constructor = f
			}
		}

		if constructor != nil {
			data.Receiver.Constructor = &function{Name: constructor.Name()}
			for index := range constructor.Signature().Params().Len() {
				var name string
				if index == 0 {
					name = "in"
				} else {
					name = fmt.Sprintf("in%d", index+1)
				}
				data.Receiver.Constructor.Args = append(data.Receiver.Constructor.Args, field{
					Name: name,
					Type: types.TypeString(constructor.Signature().Params().At(index).Type(), qf),
				})
			}
			for index := range constructor.Signature().Results().Len() {
				var name string
				if index == 0 {
					// The first return value must be of type T, *T, or a type whose named
					// type is the same as named type of T.
					name = varName
				} else if index == constructor.Signature().Results().Len()-1 && types.Identical(constructor.Signature().Results().At(index).Type(), errorType) {
					name = "err"
				} else {
					// Drop any return values beyond the first and the last.
					// e.g., "f, _, _, err := NewFoo()".
					name = "_"
				}
				data.Receiver.Constructor.Results = append(data.Receiver.Constructor.Results, field{
					Name: name,
					Type: types.TypeString(constructor.Signature().Results().At(index).Type(), qf),
				})
			}
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
