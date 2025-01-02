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
	goplsastutil "golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/imports"
	internalastutil "golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/refactor/inline"
	"golang.org/x/tools/internal/tokeninternal"
	"golang.org/x/tools/internal/typesinternal"
)

// Changing a signature works as follows, supposing we have the following
// original function declaration:
//
//  func Foo(a, b, c int)
//
// Step 1: Write the declaration according to the given signature change. For
// example, given the parameter transformation [2, 0, 1], we construct a new
// ast.FuncDecl for the signature:
//
//   func Foo0(c, a, b int)
//
// Step 2: Build a wrapper function that delegates to the new function.
// With this example, the wrapper would look like this:
//
//   func Foo1(a, b, c int) {
//     Foo0(c, a, b int)
//   }
//
// Step 3: Swap in the wrapper for the original, and inline all calls. The
// trick here is to rename Foo1 to Foo, inline all calls (replacing them with
// a call to Foo0), and then rename Foo0 back to Foo, using a simple string
// replacement.
//
// For example, given a call
//
// 	func _() {
// 		Foo(1, 2, 3)
// 	}
//
// The inlining results in
//
// 	func _() {
// 		Foo0(3, 1, 2)
// 	}
//
// And then renaming results in
//
// 	func _() {
//  	Foo(3, 1, 2)
// 	}
//
// And the desired signature rewriting has occurred! Note: in practice, we
// don't use the names Foo0 and Foo1, as they are too likely to conflict with
// an existing declaration name. (Instead, we use the prefix G_o_ + p_l_s)
//
// The advantage of going through the inliner is that we get all of the
// semantic considerations for free: the inliner will check for side effects
// of arguments, check if the last use of a variable is being removed, check
// for unnecessary imports, etc.
//
// Furthermore, by running the change signature rewriting through the inliner,
// we ensure that the inliner gets better to the point that it can handle a
// change signature rewrite just as well as if we had implemented change
// signature as its own operation. For example, suppose we support reordering
// the results of a function. In that case, the wrapper would be:
//
// 	func Foo1() (int, int) {
// 		y, x := Foo0()
// 		return x, y
// 	}
//
// And a call would be rewritten from
//
// 	x, y := Foo()
//
// To
//
//  r1, r2 := Foo()
//  x, y := r2, r1
//
// In order to make this idiomatic, we'd have to teach the inliner to rewrite
// this as y, x := Foo(). The simplest and most general way to achieve this is
// to teach the inliner to recognize when a variable is redundant (r1 and r2,
// in this case), lifting declarations. That's probably a very useful skill for
// the inliner to have.

// removeParam computes a refactoring to remove the parameter indicated by the
// given range.
func removeParam(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) ([]protocol.DocumentChange, error) {
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	// Find the unused parameter to remove.
	info := findParam(pgf, rng)
	if info == nil || info.paramIndex == -1 {
		return nil, fmt.Errorf("no param found")
	}
	// Write a transformation to remove the param.
	var newParams []int
	for i := 0; i < info.decl.Type.Params.NumFields(); i++ {
		if i != info.paramIndex {
			newParams = append(newParams, i)
		}
	}
	return ChangeSignature(ctx, snapshot, pkg, pgf, rng, newParams)
}

// ChangeSignature computes a refactoring to update the signature according to
// the provided parameter transformation, for the signature definition
// surrounding rng.
//
// newParams expresses the new parameters for the signature in terms of the old
// parameters. Each entry in newParams is the index of the new parameter in the
// original parameter list. For example, given func Foo(a, b, c int) and newParams
// [2, 0, 1], the resulting changed signature is Foo(c, a, b int). If newParams
// omits an index of the original signature, that parameter is removed.
//
// This operation is a work in progress. Remaining TODO:
//   - Handle adding parameters.
//   - Handle adding/removing/reordering results.
//   - Improve the extra newlines in output.
//   - Stream type checking via ForEachPackage.
//   - Avoid unnecessary additional type checking.
func ChangeSignature(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, pgf *parsego.File, rng protocol.Range, newParams []int) ([]protocol.DocumentChange, error) {
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

	info := findParam(pgf, rng)
	if info == nil || info.decl == nil {
		return nil, fmt.Errorf("failed to find declaration")
	}

	// Step 1: create the new declaration, which is a copy of the original decl
	// with the rewritten signature.

	// Flatten, transform and regroup fields, using the flatField intermediate
	// representation. A flatField is the result of flattening an *ast.FieldList
	// along with type information.
	type flatField struct {
		name     string // empty if the field is unnamed
		typeExpr ast.Expr
		typ      types.Type
	}

	var newParamFields []flatField
	for id, field := range goplsastutil.FlatFields(info.decl.Type.Params) {
		typ := pkg.TypesInfo().TypeOf(field.Type)
		if typ == nil {
			return nil, fmt.Errorf("missing field type for field #%d", len(newParamFields))
		}
		field := flatField{
			typeExpr: field.Type,
			typ:      typ,
		}
		if id != nil {
			field.name = id.Name
		}
		newParamFields = append(newParamFields, field)
	}

	// Select the new parameter fields.
	newParamFields, ok := selectElements(newParamFields, newParams)
	if !ok {
		return nil, fmt.Errorf("failed to apply parameter transformation %v", newParams)
	}

	// writeFields performs the regrouping of named fields.
	writeFields := func(flatFields []flatField) *ast.FieldList {
		list := new(ast.FieldList)
		for i, f := range flatFields {
			var field *ast.Field
			if i > 0 && f.name != "" && flatFields[i-1].name != "" && types.Identical(f.typ, flatFields[i-1].typ) {
				// Group named fields if they have the same type.
				field = list.List[len(list.List)-1]
			} else {
				// Otherwise, create a new field.
				field = &ast.Field{
					Type: internalastutil.CloneNode(f.typeExpr),
				}
				list.List = append(list.List, field)
			}
			if f.name != "" {
				field.Names = append(field.Names, ast.NewIdent(f.name))
			}
		}
		return list
	}

	newDecl := internalastutil.CloneNode(info.decl)
	newDecl.Type.Params = writeFields(newParamFields)

	// Step 2: build a wrapper function calling the new declaration.

	var (
		params   = internalastutil.CloneNode(info.decl.Type.Params) // parameters of wrapper func: "_" names must be modified
		args     = make([]ast.Expr, len(newParams))                 // arguments to the delegated call
		variadic = false                                            // whether the signature is variadic
	)
	{
		// Record names used by non-blank parameters, just in case the user had a
		// parameter named 'blank0', which would conflict with the synthetic names
		// we construct below.
		// TODO(rfindley): add an integration test for this behavior.
		nonBlankNames := make(map[string]bool) // for detecting conflicts with renamed blanks
		for _, fld := range params.List {
			for _, n := range fld.Names {
				if n.Name != "_" {
					nonBlankNames[n.Name] = true
				}
			}
			if len(fld.Names) == 0 {
				// All parameters must have a non-blank name. For convenience, give
				// this field a blank name.
				fld.Names = append(fld.Names, ast.NewIdent("_")) // will be named below
			}
		}
		// oldParams maps parameters to their argument in the delegated call.
		// In other words, it is the inverse of newParams, but it is represented as
		// a map rather than a slice, as not every old param need exist in
		// newParams.
		oldParams := make(map[int]int)
		for new, old := range newParams {
			oldParams[old] = new
		}
		blanks := 0
		paramIndex := 0 // global param index.
		for id, field := range goplsastutil.FlatFields(params) {
			argIndex, ok := oldParams[paramIndex]
			paramIndex++
			if !ok {
				continue // parameter is removed
			}
			if id.Name == "_" { // from above: every field has names
				// Create names for blank (_) parameters so the delegating wrapper
				// can refer to them.
				for {
					// These names will not be seen by the user, so give them an
					// arbitrary name.
					newName := fmt.Sprintf("blank%d", blanks)
					blanks++
					if !nonBlankNames[newName] {
						id.Name = newName
						break
					}
				}
			}
			args[argIndex] = ast.NewIdent(id.Name)
			// Record whether the call has an ellipsis.
			// (Only the last loop iteration matters.)
			_, variadic = field.Type.(*ast.Ellipsis)
		}
	}

	// Step 3: Rewrite all referring calls, by swapping in the wrapper and
	// inlining all.

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
	paramIndex int           // index of param among all params, or -1
	field      *ast.Field    // enclosing field of Decl, or nil if range not among parameters
	name       *ast.Ident    // indicated name (either enclosing, or Field.Names[0] if len(Field.Names) == 1)
}

// findParam finds the parameter information spanned by the given range.
func findParam(pgf *parsego.File, rng protocol.Range) *paramInfo {
	info := paramInfo{paramIndex: -1}
	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil
	}

	path, _ := astutil.PathEnclosingInterval(pgf.File, start, end)
	var (
		id    *ast.Ident
		field *ast.Field
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
			info.decl = n
		}
	}
	if info.decl == nil {
		return nil
	}
	if field == nil {
		return &info
	}
	pi := 0
	// Search for field and id among parameters of decl.
	// This search may fail, even if one or both of id and field are non nil:
	// field could be from a result or local declaration, and id could be part of
	// the field type rather than names.
	for _, f := range info.decl.Type.Params.List {
		if f == field {
			info.paramIndex = pi // may be modified later
			info.field = f
			for _, n := range f.Names {
				if n == id {
					info.paramIndex = pi
					info.name = n
					break
				}
				pi++
			}
			if info.name == nil && len(info.field.Names) == 1 {
				info.name = info.field.Names[0]
			}
			break
		} else {
			m := len(f.Names)
			if m == 0 {
				m = 1
			}
			pi += m
		}
	}
	return &info
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
	opts := &inline.Options{
		Logf:          logf,
		IgnoreEffects: true,
	}
	return inlineAllCalls(ctx, rw.snapshot, rw.pkg, rw.pgf, rw.origDecl, calleeInfo, post, opts)
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
		Types:        make(map[ast.Expr]types.TypeAndValue),
		Defs:         make(map[*ast.Ident]types.Object),
		Uses:         make(map[*ast.Ident]types.Object),
		Implicits:    make(map[ast.Node]types.Object),
		Selections:   make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:       make(map[ast.Node]*types.Scope),
		Instances:    make(map[*ast.Ident]types.Instance),
		FileVersions: make(map[*ast.File]string),
	}
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

// selectElements returns a new array of elements of s indicated by the
// provided list of indices. It returns false if any index was out of bounds.
//
// For example, given the slice []string{"a", "b", "c", "d"}, the
// indices []int{3, 0, 1} results in the slice []string{"d", "a", "b"}.
func selectElements[T any](s []T, indices []int) ([]T, bool) {
	res := make([]T, len(indices))
	for i, index := range indices {
		if index < 0 || index >= len(s) {
			return nil, false
		}
		res[i] = s[index]
	}
	return res, true
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
