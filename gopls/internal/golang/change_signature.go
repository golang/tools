// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"regexp"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/imports"
	internalastutil "golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/refactor/inline"
	"golang.org/x/tools/internal/tokeninternal"
	"golang.org/x/tools/internal/typesinternal"
	"golang.org/x/tools/internal/versions"
)

// RemoveUnusedParameter computes a refactoring to remove the parameter
// indicated by the given range, which must be contained within an unused
// parameter name or field.
//
// This operation is a work in progress. Remaining TODO:
//   - Handle function assignment correctly.
//   - Improve the extra newlines in output.
//   - Stream type checking via ForEachPackage.
//   - Avoid unnecessary additional type checking.
func RemoveUnusedParameter(ctx context.Context, fh file.Handle, rng protocol.Range, snapshot *cache.Snapshot) ([]protocol.DocumentChange, error) {
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}

	// Changes to our heuristics for whether we can remove a parameter must also
	// be reflected in the canRemoveParameter helper.
	if perrors, terrors := pkg.ParseErrors(), pkg.TypeErrors(); len(perrors) > 0 || len(terrors) > 0 {
		var sample string
		if len(perrors) > 0 {
			sample = perrors[0].Error()
		} else {
			sample = terrors[0].Error()
		}
		return nil, fmt.Errorf("can't change signatures for packages with parse or type errors: (e.g. %s)", sample)
	}

	info, err := findParam(pgf, rng)
	if err != nil {
		return nil, err // e.g. invalid range
	}
	if info.field == nil {
		return nil, fmt.Errorf("failed to find field")
	}

	// Create the new declaration, which is a copy of the original decl with the
	// unnecessary parameter removed.
	newDecl := internalastutil.CloneNode(info.decl)
	if info.name != nil {
		names := remove(newDecl.Type.Params.List[info.fieldIndex].Names, info.nameIndex)
		newDecl.Type.Params.List[info.fieldIndex].Names = names
	}
	if len(newDecl.Type.Params.List[info.fieldIndex].Names) == 0 {
		// Unnamed, or final name was removed: in either case, remove the field.
		newDecl.Type.Params.List = remove(newDecl.Type.Params.List, info.fieldIndex)
	}

	// Compute inputs into building a wrapper function around the modified
	// signature.
	var (
		params   = internalastutil.CloneNode(info.decl.Type.Params) // "_" names will be modified
		args     []ast.Expr                                         // arguments to delegate
		variadic = false                                            // whether the signature is variadic
	)
	{
		allNames := make(map[string]bool) // for renaming blanks
		for _, fld := range params.List {
			for _, n := range fld.Names {
				if n.Name != "_" {
					allNames[n.Name] = true
				}
			}
		}
		blanks := 0
		for i, fld := range params.List {
			for j, n := range fld.Names {
				if i == info.fieldIndex && j == info.nameIndex {
					continue
				}
				if n.Name == "_" {
					// Create names for blank (_) parameters so the delegating wrapper
					// can refer to them.
					for {
						newName := fmt.Sprintf("blank%d", blanks)
						blanks++
						if !allNames[newName] {
							n.Name = newName
							break
						}
					}
				}
				args = append(args, &ast.Ident{Name: n.Name})
				if i == len(params.List)-1 {
					_, variadic = fld.Type.(*ast.Ellipsis)
				}
			}
		}
	}

	// Rewrite all referring calls.
	newContent, err := rewriteCalls(ctx, signatureRewrite{
		snapshot: snapshot,
		pkg:      pkg,
		pgf:      pgf,
		origDecl: info.decl,
		newDecl:  newDecl,
		params:   params,
		callArgs: args,
		variadic: variadic,
	})
	if err != nil {
		return nil, err
	}

	// Finally, rewrite the original declaration. We do this after inlining all
	// calls, as there may be calls in the same file as the declaration. But none
	// of the inlining should have changed the location of the original
	// declaration.
	{
		idx := findDecl(pgf.File, info.decl)
		if idx < 0 {
			return nil, bug.Errorf("didn't find original decl")
		}

		src, ok := newContent[pgf.URI]
		if !ok {
			src = pgf.Src
		}
		fset := tokeninternal.FileSetFor(pgf.Tok)
		src, err := rewriteSignature(fset, idx, src, newDecl)
		if err != nil {
			return nil, err
		}
		newContent[pgf.URI] = src
	}

	// Translate the resulting state into document changes.
	var changes []protocol.DocumentChange
	for uri, after := range newContent {
		fh, err := snapshot.ReadFile(ctx, uri)
		if err != nil {
			return nil, err
		}
		before, err := fh.Content()
		if err != nil {
			return nil, err
		}
		edits := diff.Bytes(before, after)
		mapper := protocol.NewMapper(uri, before)
		textedits, err := protocol.EditsFromDiffEdits(mapper, edits)
		if err != nil {
			return nil, fmt.Errorf("computing edits for %s: %v", uri, err)
		}
		change := protocol.DocumentChangeEdit(fh, textedits)
		changes = append(changes, change)
	}
	return changes, nil
}

// rewriteSignature rewrites the signature of the declIdx'th declaration in src
// to use the signature of newDecl (described by fset).
//
// TODO(rfindley): I think this operation could be generalized, for example by
// using a concept of a 'nodepath' to correlate nodes between two related
// files.
//
// Note that with its current application, rewriteSignature is expected to
// succeed. Separate bug.Errorf calls are used below (rather than one call at
// the callsite) in order to have greater precision.
func rewriteSignature(fset *token.FileSet, declIdx int, src0 []byte, newDecl *ast.FuncDecl) ([]byte, error) {
	// Parse the new file0 content, to locate the original params.
	file0, err := parser.ParseFile(fset, "", src0, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, bug.Errorf("re-parsing declaring file failed: %v", err)
	}
	decl0, _ := file0.Decls[declIdx].(*ast.FuncDecl)
	// Inlining shouldn't have changed the location of any declarations, but do
	// a sanity check.
	if decl0 == nil || decl0.Name.Name != newDecl.Name.Name {
		return nil, bug.Errorf("inlining affected declaration order: found %v, not func %s", decl0, newDecl.Name.Name)
	}
	opening0, closing0, err := safetoken.Offsets(fset.File(decl0.Pos()), decl0.Type.Params.Opening, decl0.Type.Params.Closing)
	if err != nil {
		return nil, bug.Errorf("can't find params: %v", err)
	}

	// Format the modified signature and apply a textual replacement. This
	// minimizes comment disruption.
	formattedType := FormatNode(fset, newDecl.Type)
	expr, err := parser.ParseExprFrom(fset, "", []byte(formattedType), 0)
	if err != nil {
		return nil, bug.Errorf("parsing modified signature: %v", err)
	}
	newType := expr.(*ast.FuncType)
	opening1, closing1, err := safetoken.Offsets(fset.File(newType.Pos()), newType.Params.Opening, newType.Params.Closing)
	if err != nil {
		return nil, bug.Errorf("param offsets: %v", err)
	}
	newParams := formattedType[opening1 : closing1+1]

	// Splice.
	var buf bytes.Buffer
	buf.Write(src0[:opening0])
	buf.WriteString(newParams)
	buf.Write(src0[closing0+1:])
	newSrc := buf.Bytes()
	if len(file0.Imports) > 0 {
		formatted, err := imports.Process("output", newSrc, nil)
		if err != nil {
			return nil, bug.Errorf("imports.Process failed: %v", err)
		}
		newSrc = formatted
	}
	return newSrc, nil
}

// paramInfo records information about a param identified by a position.
type paramInfo struct {
	decl       *ast.FuncDecl // enclosing func decl (non-nil)
	fieldIndex int           // index of Field in Decl.Type.Params, or -1
	field      *ast.Field    // enclosing field of Decl, or nil if range not among parameters
	nameIndex  int           // index of Name in Field.Names, or nil
	name       *ast.Ident    // indicated name (either enclosing, or Field.Names[0] if len(Field.Names) == 1)
}

// findParam finds the parameter information spanned by the given range.
func findParam(pgf *parsego.File, rng protocol.Range) (*paramInfo, error) {
	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, err
	}

	path, _ := astutil.PathEnclosingInterval(pgf.File, start, end)
	var (
		id    *ast.Ident
		field *ast.Field
		decl  *ast.FuncDecl
	)
	// Find the outermost enclosing node of each kind, whether or not they match
	// the semantics described in the docstring.
	for _, n := range path {
		switch n := n.(type) {
		case *ast.Ident:
			id = n
		case *ast.Field:
			field = n
		case *ast.FuncDecl:
			decl = n
		}
	}
	// Check the conditions described in the docstring.
	if decl == nil {
		return nil, fmt.Errorf("range is not within a function declaration")
	}
	info := &paramInfo{
		fieldIndex: -1,
		nameIndex:  -1,
		decl:       decl,
	}
	for fi, f := range decl.Type.Params.List {
		if f == field {
			info.fieldIndex = fi
			info.field = f
			for ni, n := range f.Names {
				if n == id {
					info.nameIndex = ni
					info.name = n
					break
				}
			}
			if info.name == nil && len(info.field.Names) == 1 {
				info.nameIndex = 0
				info.name = info.field.Names[0]
			}
			break
		}
	}
	return info, nil
}

// signatureRewrite defines a rewritten function signature.
//
// See rewriteCalls for more details.
type signatureRewrite struct {
	snapshot          *cache.Snapshot
	pkg               *cache.Package
	pgf               *parsego.File
	origDecl, newDecl *ast.FuncDecl
	params            *ast.FieldList
	callArgs          []ast.Expr
	variadic          bool
}

// rewriteCalls returns the document changes required to rewrite the
// signature of origDecl to that of newDecl.
//
// This is a rather complicated factoring of the rewrite operation, but is able
// to describe arbitrary rewrites. Specifically, rewriteCalls creates a
// synthetic copy of pkg, where the original function declaration is changed to
// be a trivial wrapper around the new declaration. params and callArgs are
// used to perform this delegation: params must have the same type as origDecl,
// but may have renamed parameters (such as is required for delegating blank
// parameters). callArgs are the arguments of the delegated call (i.e. using
// params).
//
// For example, consider removing the unused 'b' parameter below, rewriting
//
//	func Foo(a, b, c, _ int) int {
//	  return a+c
//	}
//
// To
//
//	func Foo(a, c, _ int) int {
//	  return a+c
//	}
//
// In this case, rewriteCalls is parameterized as follows:
//   - origDecl is the original declaration
//   - newDecl is the new declaration, which is a copy of origDecl less the 'b'
//     parameter.
//   - params is a new parameter list (a, b, c, blank0 int) to be used for the
//     new wrapper.
//   - callArgs is the argument list (a, c, blank0), to be used to call the new
//     delegate.
//
// rewriting is expressed this way so that rewriteCalls can own the details
// of *how* this rewriting is performed. For example, as of writing it names
// the synthetic delegate G_o_p_l_s_foo, but the caller need not know this.
//
// By passing an entirely new declaration, rewriteCalls may be used for
// signature refactorings that may affect the function body, such as removing
// or adding return values.
func rewriteCalls(ctx context.Context, rw signatureRewrite) (map[protocol.DocumentURI][]byte, error) {
	// tag is a unique prefix that is added to the delegated declaration.
	//
	// It must have a ~0% probability of causing collisions with existing names.
	const tag = "G_o_p_l_s_"

	var (
		modifiedSrc  []byte
		modifiedFile *ast.File
		modifiedDecl *ast.FuncDecl
	)
	{
		delegate := internalastutil.CloneNode(rw.newDecl) // clone before modifying
		delegate.Name.Name = tag + delegate.Name.Name
		if obj := rw.pkg.Types().Scope().Lookup(delegate.Name.Name); obj != nil {
			return nil, fmt.Errorf("synthetic name %q conflicts with an existing declaration", delegate.Name.Name)
		}

		wrapper := internalastutil.CloneNode(rw.origDecl)
		wrapper.Type.Params = rw.params

		// Get the receiver name, creating it if necessary.
		var recv string // nonempty => call is a method call with receiver recv
		if wrapper.Recv.NumFields() > 0 {
			if len(wrapper.Recv.List[0].Names) > 0 {
				recv = wrapper.Recv.List[0].Names[0].Name
			} else {
				// Create unique name for the temporary receiver, which will be inlined away.
				//
				// We use the lexical scope of the original function to avoid conflicts
				// with (e.g.) named result variables. However, since the parameter syntax
				// may have been modified/renamed from the original function, we must
				// reject those names too.
				usedParams := make(map[string]bool)
				for _, fld := range wrapper.Type.Params.List {
					for _, name := range fld.Names {
						usedParams[name.Name] = true
					}
				}
				scope := rw.pkg.TypesInfo().Scopes[rw.origDecl.Type]
				if scope == nil {
					return nil, bug.Errorf("missing function scope for %v", rw.origDecl.Name.Name)
				}
				for i := 0; ; i++ {
					recv = fmt.Sprintf("r%d", i)
					_, obj := scope.LookupParent(recv, token.NoPos)
					if obj == nil && !usedParams[recv] {
						break
					}
				}
				wrapper.Recv.List[0].Names = []*ast.Ident{{Name: recv}}
			}
		}

		name := &ast.Ident{Name: delegate.Name.Name}
		var fun ast.Expr = name
		if recv != "" {
			fun = &ast.SelectorExpr{
				X:   &ast.Ident{Name: recv},
				Sel: name,
			}
		}
		call := &ast.CallExpr{
			Fun:  fun,
			Args: rw.callArgs,
		}
		if rw.variadic {
			call.Ellipsis = 1 // must not be token.NoPos
		}

		var stmt ast.Stmt
		if delegate.Type.Results.NumFields() > 0 {
			stmt = &ast.ReturnStmt{
				Results: []ast.Expr{call},
			}
		} else {
			stmt = &ast.ExprStmt{
				X: call,
			}
		}
		wrapper.Body = &ast.BlockStmt{
			List: []ast.Stmt{stmt},
		}

		fset := tokeninternal.FileSetFor(rw.pgf.Tok)
		var err error
		modifiedSrc, err = replaceFileDecl(rw.pgf, rw.origDecl, delegate)
		if err != nil {
			return nil, err
		}
		// TODO(rfindley): we can probably get away with one fewer parse operations
		// by returning the modified AST from replaceDecl. Investigate if that is
		// accurate.
		modifiedSrc = append(modifiedSrc, []byte("\n\n"+FormatNode(fset, wrapper))...)
		modifiedFile, err = parser.ParseFile(rw.pkg.FileSet(), rw.pgf.URI.Path(), modifiedSrc, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		modifiedDecl = modifiedFile.Decls[len(modifiedFile.Decls)-1].(*ast.FuncDecl)
	}

	// Type check pkg again with the modified file, to compute the synthetic
	// callee.
	logf := logger(ctx, "change signature", rw.snapshot.Options().VerboseOutput)
	pkg2, info, err := reTypeCheck(logf, rw.pkg, map[protocol.DocumentURI]*ast.File{rw.pgf.URI: modifiedFile}, false)
	if err != nil {
		return nil, err
	}
	calleeInfo, err := inline.AnalyzeCallee(logf, rw.pkg.FileSet(), pkg2, info, modifiedDecl, modifiedSrc)
	if err != nil {
		return nil, fmt.Errorf("analyzing callee: %v", err)
	}

	post := func(got []byte) []byte { return bytes.ReplaceAll(got, []byte(tag), nil) }
	return inlineAllCalls(ctx, logf, rw.snapshot, rw.pkg, rw.pgf, rw.origDecl, calleeInfo, post)
}

// reTypeCheck re-type checks orig with new file contents defined by fileMask.
//
// It expects that any newly added imports are already present in the
// transitive imports of orig.
//
// If expectErrors is true, reTypeCheck allows errors in the new package.
// TODO(rfindley): perhaps this should be a filter to specify which errors are
// acceptable.
func reTypeCheck(logf func(string, ...any), orig *cache.Package, fileMask map[protocol.DocumentURI]*ast.File, expectErrors bool) (*types.Package, *types.Info, error) {
	pkg := types.NewPackage(string(orig.Metadata().PkgPath), string(orig.Metadata().Name))
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:     make(map[ast.Node]*types.Scope),
		Instances:  make(map[*ast.Ident]types.Instance),
	}
	versions.InitFileVersions(info)
	{
		var files []*ast.File
		for _, pgf := range orig.CompiledGoFiles() {
			if mask, ok := fileMask[pgf.URI]; ok {
				files = append(files, mask)
			} else {
				files = append(files, pgf.File)
			}
		}

		// Implement a BFS for imports in the transitive package graph.
		//
		// Note that this only works if any newly added imports are expected to be
		// present among transitive imports. In general we cannot assume this to
		// be the case, but in the special case of removing a parameter it works
		// because any parameter types must be present in export data.
		var importer func(importPath string) (*types.Package, error)
		{
			var (
				importsByPath = make(map[string]*types.Package) // cached imports
				toSearch      = []*types.Package{orig.Types()}  // packages to search
				searched      = make(map[string]bool)           // path -> (false, if present in toSearch; true, if already searched)
			)
			importer = func(path string) (*types.Package, error) {
				if p, ok := importsByPath[path]; ok {
					return p, nil
				}
				for len(toSearch) > 0 {
					pkg := toSearch[0]
					toSearch = toSearch[1:]
					searched[pkg.Path()] = true
					for _, p := range pkg.Imports() {
						// TODO(rfindley): this is incorrect: p.Path() is a package path,
						// whereas path is an import path. We can fix this by reporting any
						// newly added imports from inlining, or by using the ImporterFrom
						// interface and package metadata.
						//
						// TODO(rfindley): can't the inliner also be wrong here? It's
						// possible that an import path means different things depending on
						// the location.
						importsByPath[p.Path()] = p
						if _, ok := searched[p.Path()]; !ok {
							searched[p.Path()] = false
							toSearch = append(toSearch, p)
						}
					}
					if p, ok := importsByPath[path]; ok {
						return p, nil
					}
				}
				return nil, fmt.Errorf("missing import")
			}
		}
		cfg := &types.Config{
			Sizes:    orig.Metadata().TypesSizes,
			Importer: ImporterFunc(importer),
		}

		// Copied from cache/check.go.
		// TODO(rfindley): factor this out and fix goVersionRx.
		// Set Go dialect.
		if module := orig.Metadata().Module; module != nil && module.GoVersion != "" {
			goVersion := "go" + module.GoVersion
			// types.NewChecker panics if GoVersion is invalid.
			// An unparsable mod file should probably stop us
			// before we get here, but double check just in case.
			if goVersionRx.MatchString(goVersion) {
				cfg.GoVersion = goVersion
			}
		}
		if expectErrors {
			cfg.Error = func(err error) {
				logf("re-type checking: expected error: %v", err)
			}
		}
		typesinternal.SetUsesCgo(cfg)
		checker := types.NewChecker(cfg, orig.FileSet(), pkg, info)
		if err := checker.Files(files); err != nil && !expectErrors {
			return nil, nil, fmt.Errorf("type checking rewritten package: %v", err)
		}
	}
	return pkg, info, nil
}

// TODO(golang/go#63472): this looks wrong with the new Go version syntax.
var goVersionRx = regexp.MustCompile(`^go([1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func remove[T any](s []T, i int) []T {
	return append(s[:i], s[i+1:]...)
}

// replaceFileDecl replaces old with new in the file described by pgf.
//
// TODO(rfindley): generalize, and combine with rewriteSignature.
func replaceFileDecl(pgf *parsego.File, old, new ast.Decl) ([]byte, error) {
	i := findDecl(pgf.File, old)
	if i == -1 {
		return nil, bug.Errorf("didn't find old declaration")
	}
	start, end, err := safetoken.Offsets(pgf.Tok, old.Pos(), old.End())
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(pgf.Src[:start])
	fset := tokeninternal.FileSetFor(pgf.Tok)
	if err := format.Node(&out, fset, new); err != nil {
		return nil, bug.Errorf("formatting new node: %v", err)
	}
	out.Write(pgf.Src[end:])
	return out.Bytes(), nil
}

// findDecl finds the index of decl in file.Decls.
//
// TODO: use slices.Index when it is available.
func findDecl(file *ast.File, decl ast.Decl) int {
	for i, d := range file.Decls {
		if d == decl {
			return i
		}
	}
	return -1
}
