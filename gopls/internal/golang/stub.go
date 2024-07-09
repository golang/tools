// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	pathpkg "path"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/analysis/stubmethods"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/tokeninternal"
)

// stubMethodsFixer returns a suggested fix to declare the missing
// methods of the concrete type that is assigned to an interface type
// at the cursor position.
func stubMethodsFixer(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	nodes, _ := astutil.PathEnclosingInterval(pgf.File, start, end)
	si := stubmethods.GetStubInfo(pkg.FileSet(), pkg.TypesInfo(), nodes, start)
	if si == nil {
		return nil, nil, fmt.Errorf("nil interface request")
	}

	// A function-local type cannot be stubbed
	// since there's nowhere to put the methods.
	conc := si.Concrete.Obj()
	if conc.Parent() != conc.Pkg().Scope() {
		return nil, nil, fmt.Errorf("local type %q cannot be stubbed", conc.Name())
	}

	// Parse the file declaring the concrete type.
	//
	// Beware: declPGF is not necessarily covered by pkg.FileSet() or si.Fset.
	declPGF, _, err := parseFull(ctx, snapshot, si.Fset, conc.Pos())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse file %q declaring implementation type: %w", declPGF.URI, err)
	}
	if declPGF.Fixed() {
		return nil, nil, fmt.Errorf("file contains parse errors: %s", declPGF.URI)
	}

	// Find metadata for the concrete type's declaring package
	// as we'll need its import mapping.
	declMeta := findFileInDeps(snapshot, pkg.Metadata(), declPGF.URI)
	if declMeta == nil {
		return nil, nil, bug.Errorf("can't find metadata for file %s among dependencies of %s", declPGF.URI, pkg)
	}

	// Record all direct methods of the current object
	concreteFuncs := make(map[string]struct{})
	for i := 0; i < si.Concrete.NumMethods(); i++ {
		concreteFuncs[si.Concrete.Method(i).Name()] = struct{}{}
	}

	// Find subset of interface methods that the concrete type lacks.
	ifaceType := si.Interface.Type().Underlying().(*types.Interface)

	type missingFn struct {
		fn         *types.Func
		needSubtle string
	}

	var (
		missing                  []missingFn
		concreteStruct, isStruct = si.Concrete.Origin().Underlying().(*types.Struct)
	)

	for i := 0; i < ifaceType.NumMethods(); i++ {
		imethod := ifaceType.Method(i)
		cmethod, index, _ := types.LookupFieldOrMethod(si.Concrete, si.Pointer, imethod.Pkg(), imethod.Name())
		if cmethod == nil {
			missing = append(missing, missingFn{fn: imethod})
			continue
		}

		if _, ok := cmethod.(*types.Var); ok {
			// len(LookupFieldOrMethod.index) = 1 => conflict, >1 => shadow.
			return nil, nil, fmt.Errorf("adding method %s.%s would conflict with (or shadow) existing field",
				conc.Name(), imethod.Name())
		}

		if _, exist := concreteFuncs[imethod.Name()]; exist {
			if !types.Identical(cmethod.Type(), imethod.Type()) {
				return nil, nil, fmt.Errorf("method %s.%s already exists but has the wrong type: got %s, want %s",
					conc.Name(), imethod.Name(), cmethod.Type(), imethod.Type())
			}
			continue
		}

		mf := missingFn{fn: imethod}
		if isStruct && len(index) > 0 {
			field := concreteStruct.Field(index[0])

			fn := field.Name()
			if is[*types.Pointer](field.Type()) {
				fn = "*" + fn
			}

			mf.needSubtle = fmt.Sprintf("// Subtle: this method shadows the method (%s).%s of %s.%s.\n", fn, imethod.Name(), si.Concrete.Obj().Name(), field.Name())
		}

		missing = append(missing, mf)
	}
	if len(missing) == 0 {
		return nil, nil, fmt.Errorf("no missing methods found")
	}

	// Build import environment for the declaring file.
	// (typesutil.FileQualifier works only for complete
	// import mappings, and requires types.)
	importEnv := make(map[ImportPath]string) // value is local name
	for _, imp := range declPGF.File.Imports {
		importPath := metadata.UnquoteImportPath(imp)
		var name string
		if imp.Name != nil {
			name = imp.Name.Name
			if name == "_" {
				continue
			} else if name == "." {
				name = "" // see types.Qualifier
			}
		} else {
			// Use the correct name from the metadata of the imported
			// package---not a guess based on the import path.
			mp := snapshot.Metadata(declMeta.DepsByImpPath[importPath])
			if mp == nil {
				continue // can't happen?
			}
			name = string(mp.Name)
		}
		importEnv[importPath] = name // latest alias wins
	}

	// Create a package name qualifier that uses the
	// locally appropriate imported package name.
	// It records any needed new imports.
	// TODO(adonovan): factor with golang.FormatVarType?
	//
	// Prior to CL 469155 this logic preserved any renaming
	// imports from the file that declares the interface
	// method--ostensibly the preferred name for imports of
	// frequently renamed packages such as protobufs.
	// Now we use the package's declared name. If this turns out
	// to be a mistake, then use parseHeader(si.iface.Pos()).
	//
	type newImport struct{ name, importPath string }
	var newImports []newImport // for AddNamedImport
	qual := func(pkg *types.Package) string {
		// TODO(adonovan): don't ignore vendor prefix.
		//
		// Ignore the current package import.
		if pkg.Path() == conc.Pkg().Path() {
			return ""
		}

		importPath := ImportPath(pkg.Path())
		name, ok := importEnv[importPath]
		if !ok {
			// Insert new import using package's declared name.
			//
			// TODO(adonovan): resolve conflict between declared
			// name and existing file-level (declPGF.File.Imports)
			// or package-level (si.Concrete.Pkg.Scope) decls by
			// generating a fresh name.
			name = pkg.Name()
			importEnv[importPath] = name
			new := newImport{importPath: string(importPath)}
			// For clarity, use a renaming import whenever the
			// local name does not match the path's last segment.
			if name != pathpkg.Base(trimVersionSuffix(new.importPath)) {
				new.name = name
			}
			newImports = append(newImports, new)
		}
		return name
	}

	// Format interface name (used only in a comment).
	iface := si.Interface.Name()
	if ipkg := si.Interface.Pkg(); ipkg != nil && ipkg != conc.Pkg() {
		iface = ipkg.Name() + "." + iface
	}

	// Pointer receiver?
	var star string
	if si.Pointer {
		star = "*"
	}

	// If there are any that have named receiver, choose the first one.
	// Otherwise, use lowercase for the first letter of the object.
	rn := strings.ToLower(si.Concrete.Obj().Name()[0:1])
	for i := 0; i < si.Concrete.NumMethods(); i++ {
		if recv := si.Concrete.Method(i).Type().(*types.Signature).Recv(); recv.Name() != "" {
			rn = recv.Name()
			break
		}
	}

	// Check for receiver name conflicts
	checkRecvName := func(tuple *types.Tuple) bool {
		for i := 0; i < tuple.Len(); i++ {
			if rn == tuple.At(i).Name() {
				return true
			}
		}
		return false
	}

	// Format the new methods.
	var newMethods bytes.Buffer

	for index := range missing {
		mrn := rn + " "
		sig := missing[index].fn.Type().(*types.Signature)
		if checkRecvName(sig.Params()) || checkRecvName(sig.Results()) {
			mrn = ""
		}

		fmt.Fprintf(&newMethods, `// %s implements %s.
%sfunc (%s%s%s%s) %s%s {
	panic("unimplemented")
}
`,
			missing[index].fn.Name(),
			iface,
			missing[index].needSubtle,
			mrn,
			star,
			si.Concrete.Obj().Name(),
			FormatTypeParams(si.Concrete.TypeParams()),
			missing[index].fn.Name(),
			strings.TrimPrefix(types.TypeString(missing[index].fn.Type(), qual), "func"))
	}

	// Compute insertion point for new methods:
	// after the top-level declaration enclosing the (package-level) type.
	insertOffset, err := safetoken.Offset(declPGF.Tok, declPGF.File.End())
	if err != nil {
		return nil, nil, bug.Errorf("internal error: end position outside file bounds: %v", err)
	}
	concOffset, err := safetoken.Offset(si.Fset.File(conc.Pos()), conc.Pos())
	if err != nil {
		return nil, nil, bug.Errorf("internal error: finding type decl offset: %v", err)
	}
	for _, decl := range declPGF.File.Decls {
		declEndOffset, err := safetoken.Offset(declPGF.Tok, decl.End())
		if err != nil {
			return nil, nil, bug.Errorf("internal error: finding decl offset: %v", err)
		}
		if declEndOffset > concOffset {
			insertOffset = declEndOffset
			break
		}
	}

	// Splice the new methods into the file content.
	var buf bytes.Buffer
	input := declPGF.Mapper.Content // unfixed content of file
	buf.Write(input[:insertOffset])
	buf.WriteByte('\n')
	io.Copy(&buf, &newMethods)
	buf.Write(input[insertOffset:])

	// Re-parse the file.
	fset := token.NewFileSet()
	newF, err := parser.ParseFile(fset, declPGF.URI.Path(), buf.Bytes(), parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("could not reparse file: %w", err)
	}

	// Splice the new imports into the syntax tree.
	for _, imp := range newImports {
		astutil.AddNamedImport(fset, newF, imp.name, imp.importPath)
	}

	// Pretty-print.
	var output bytes.Buffer
	if err := format.Node(&output, fset, newF); err != nil {
		return nil, nil, fmt.Errorf("format.Node: %w", err)
	}

	// Report the diff.
	diffs := diff.Bytes(input, output.Bytes())
	return tokeninternal.FileSetFor(declPGF.Tok), // edits use declPGF.Tok
		&analysis.SuggestedFix{TextEdits: diffToTextEdits(declPGF.Tok, diffs)},
		nil
}

// diffToTextEdits converts diff (offset-based) edits to analysis (token.Pos) form.
func diffToTextEdits(tok *token.File, diffs []diff.Edit) []analysis.TextEdit {
	edits := make([]analysis.TextEdit, 0, len(diffs))
	for _, edit := range diffs {
		edits = append(edits, analysis.TextEdit{
			Pos:     tok.Pos(edit.Start),
			End:     tok.Pos(edit.End),
			NewText: []byte(edit.New),
		})
	}
	return edits
}

// trimVersionSuffix removes a trailing "/v2" (etc) suffix from a module path.
//
// This is only a heuristic as to the package's declared name, and
// should only be used for stylistic decisions, such as whether it
// would be clearer to use an explicit local name in the import
// because the declared name differs from the result of this function.
// When the name matters for correctness, look up the imported
// package's Metadata.Name.
func trimVersionSuffix(path string) string {
	dir, base := pathpkg.Split(path)
	if len(base) > 1 && base[0] == 'v' && strings.Trim(base[1:], "0123456789") == "" {
		return dir // sans "/v2"
	}
	return path
}
