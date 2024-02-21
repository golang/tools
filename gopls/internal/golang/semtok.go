// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file defines the Semantic Tokens operation for Go source.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/semtok"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/gopls/internal/util/typesutil"
	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/event"
)

// semDebug enables comprehensive logging of decisions
// (gopls semtok foo.go > /dev/null shows log output).
// It should never be true in checked-in code.
const semDebug = false

func SemanticTokens(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng *protocol.Range) (*protocol.SemanticTokens, error) {
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}

	// Select range.
	var start, end token.Pos
	if rng != nil {
		var err error
		start, end, err = pgf.RangePos(*rng)
		if err != nil {
			return nil, err // e.g. invalid range
		}
	} else {
		tok := pgf.Tok
		start, end = tok.Pos(0), tok.Pos(tok.Size()) // entire file
	}

	// Reject full semantic token requests for large files.
	//
	// The LSP says that errors for the semantic token requests
	// should only be returned for exceptions (a word not
	// otherwise defined). This code treats a too-large file as an
	// exception. On parse errors, the code does what it can.
	const maxFullFileSize = 100000
	if int(end-start) > maxFullFileSize {
		return nil, fmt.Errorf("semantic tokens: range %s too large (%d > %d)",
			fh.URI().Path(), end-start, maxFullFileSize)
	}

	tv := tokenVisitor{
		ctx:            ctx,
		metadataSource: snapshot,
		metadata:       pkg.Metadata(),
		info:           pkg.TypesInfo(),
		fset:           pkg.FileSet(),
		pkg:            pkg,
		pgf:            pgf,
		start:          start,
		end:            end,
	}
	tv.visit()
	return &protocol.SemanticTokens{
		Data: semtok.Encode(
			tv.tokens,
			snapshot.Options().NoSemanticString,
			snapshot.Options().NoSemanticNumber,
			snapshot.Options().SemanticTypes,
			snapshot.Options().SemanticMods),
		ResultID: time.Now().String(), // for delta requests, but we've never seen any
	}, nil
}

type tokenVisitor struct {
	// inputs
	ctx            context.Context // for event logging
	metadataSource metadata.Source // used to resolve imports
	metadata       *metadata.Package
	info           *types.Info
	fset           *token.FileSet
	pkg            *cache.Package
	pgf            *parsego.File
	start, end     token.Pos // range of interest

	// working state
	stack  []ast.Node     // path from root of the syntax tree
	tokens []semtok.Token // computed sequence of semantic tokens
}

func (tv *tokenVisitor) visit() {
	f := tv.pgf.File
	// may not be in range, but harmless
	tv.token(f.Package, len("package"), semtok.TokKeyword, nil)
	if f.Name != nil {
		tv.token(f.Name.NamePos, len(f.Name.Name), semtok.TokNamespace, nil)
	}
	for _, decl := range f.Decls {
		// Only look at the decls that overlap the range.
		if decl.End() <= tv.start || decl.Pos() >= tv.end {
			continue
		}
		ast.Inspect(decl, tv.inspect)
	}

	// Scan all files for imported pkgs, ignore the ambiguous pkg.
	// This is to be consistent with the behavior in [go/doc]: https://pkg.go.dev/pkg/go/doc.
	importByName := make(map[string]*types.PkgName)
	for _, pgf := range tv.pkg.CompiledGoFiles() {
		for _, imp := range pgf.File.Imports {
			if obj, _ := typesutil.ImportedPkgName(tv.pkg.TypesInfo(), imp); obj != nil {
				if old, ok := importByName[obj.Name()]; ok {
					if old != nil && old.Imported() != obj.Imported() {
						importByName[obj.Name()] = nil // nil => ambiguous across files
					}
					continue
				}
				importByName[obj.Name()] = obj
			}
		}
	}

	for _, cg := range f.Comments {
		for _, c := range cg.List {
			tv.comment(c, importByName)
		}
	}
}

// Matches (for example) "[F]", "[*p.T]", "[p.T.M]"
// unless followed by a colon (exclude url link, e.g. "[go]: https://go.dev").
// The first group is reference name. e.g. The first group of "[*p.T.M]" is "p.T.M".
var docLinkRegex = regexp.MustCompile(`\[\*?([\pL_][\pL_0-9]*(\.[\pL_][\pL_0-9]*){0,2})](?:[^:]|$)`)

// comment emits semantic tokens for a comment.
// If the comment contains doc links or "go:" directives,
// it emits a separate token for each link or directive and
// each comment portion between them.
func (tv *tokenVisitor) comment(c *ast.Comment, importByName map[string]*types.PkgName) {
	if strings.HasPrefix(c.Text, "//go:") {
		tv.godirective(c)
		return
	}

	pkgScope := tv.pkg.Types().Scope()
	// lookupObjects interprets the name in various forms
	// (X, p.T, p.T.M, etc) and return the list of symbols
	// denoted by each identifier in the dotted list.
	lookupObjects := func(name string) (objs []types.Object) {
		scope := pkgScope
		if pkg, suffix, ok := strings.Cut(name, "."); ok {
			if obj, _ := importByName[pkg]; obj != nil {
				objs = append(objs, obj)
				scope = obj.Imported().Scope()
				name = suffix
			}
		}

		if recv, method, ok := strings.Cut(name, "."); ok {
			obj, ok := scope.Lookup(recv).(*types.TypeName)
			if !ok {
				return nil
			}
			objs = append(objs, obj)
			t, ok := obj.Type().(*types.Named)
			if !ok {
				return nil
			}
			m, _, _ := types.LookupFieldOrMethod(t, true, tv.pkg.Types(), method)
			if m == nil {
				return nil
			}
			objs = append(objs, m)
			return objs
		} else {
			obj := scope.Lookup(name)
			if obj == nil {
				return nil
			}
			if _, ok := obj.(*types.PkgName); !ok && !obj.Exported() {
				return nil
			}
			objs = append(objs, obj)
			return objs

		}
	}

	tokenTypeByObject := func(obj types.Object) semtok.TokenType {
		switch obj.(type) {
		case *types.PkgName:
			return semtok.TokNamespace
		case *types.Func:
			return semtok.TokFunction
		case *types.TypeName:
			return semtok.TokType
		case *types.Const, *types.Var:
			return semtok.TokVariable
		default:
			return semtok.TokComment
		}
	}

	pos := c.Pos()
	for _, line := range strings.Split(c.Text, "\n") {
		last := 0

		for _, idx := range docLinkRegex.FindAllStringSubmatchIndex(line, -1) {
			// The first group is the reference name. e.g. "X", "p.T", "p.T.M".
			name := line[idx[2]:idx[3]]
			if objs := lookupObjects(name); len(objs) > 0 {
				if last < idx[2] {
					tv.token(pos+token.Pos(last), idx[2]-last, semtok.TokComment, nil)
				}
				offset := pos + token.Pos(idx[2])
				for i, obj := range objs {
					if i > 0 {
						tv.token(offset, len("."), semtok.TokComment, nil)
						offset += token.Pos(len("."))
					}
					id, rest, _ := strings.Cut(name, ".")
					name = rest
					tv.token(offset, len(id), tokenTypeByObject(obj), nil)
					offset += token.Pos(len(id))
				}
				last = idx[3]
			}
		}
		if last != len(c.Text) {
			tv.token(pos+token.Pos(last), len(line)-last, semtok.TokComment, nil)
		}
		pos += token.Pos(len(line) + 1)
	}
}

// token emits a token of the specified extent and semantics.
func (tv *tokenVisitor) token(start token.Pos, length int, typ semtok.TokenType, modifiers []string) {
	if length <= 0 {
		return // vscode doesn't like 0-length Tokens
	}
	if !start.IsValid() {
		// This is not worth reporting. TODO(pjw): does it still happen?
		return
	}
	end := start + token.Pos(length)
	if start >= tv.end || end <= tv.start {
		return
	}
	// want a line and column from start (in LSP coordinates). Ignore line directives.
	rng, err := tv.pgf.PosRange(start, end)
	if err != nil {
		event.Error(tv.ctx, "failed to convert to range", err)
		return
	}
	if rng.End.Line != rng.Start.Line {
		// this happens if users are typing at the end of the file, but report nothing
		return
	}
	tv.tokens = append(tv.tokens, semtok.Token{
		Line:      rng.Start.Line,
		Start:     rng.Start.Character,
		Len:       rng.End.Character - rng.Start.Character, // (on same line)
		Type:      typ,
		Modifiers: modifiers,
	})
}

// strStack converts the stack to a string, for debugging and error messages.
func (tv *tokenVisitor) strStack() string {
	msg := []string{"["}
	for i := len(tv.stack) - 1; i >= 0; i-- {
		n := tv.stack[i]
		msg = append(msg, strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast."))
	}
	if len(tv.stack) > 0 {
		pos := tv.stack[len(tv.stack)-1].Pos()
		if _, err := safetoken.Offset(tv.pgf.Tok, pos); err != nil {
			msg = append(msg, fmt.Sprintf("invalid position %v for %s", pos, tv.pgf.URI))
		} else {
			posn := safetoken.Position(tv.pgf.Tok, pos)
			msg = append(msg, fmt.Sprintf("(%s:%d,col:%d)",
				filepath.Base(posn.Filename), posn.Line, posn.Column))
		}
	}
	msg = append(msg, "]")
	return strings.Join(msg, " ")
}

// srcLine returns the source text for n (truncated at first newline).
func (tv *tokenVisitor) srcLine(n ast.Node) string {
	file := tv.pgf.Tok
	line := safetoken.Line(file, n.Pos())
	start, err := safetoken.Offset(file, file.LineStart(line))
	if err != nil {
		return ""
	}
	end := start
	for ; end < len(tv.pgf.Src) && tv.pgf.Src[end] != '\n'; end++ {

	}
	return string(tv.pgf.Src[start:end])
}

func (tv *tokenVisitor) inspect(n ast.Node) (descend bool) {
	if n == nil {
		tv.stack = tv.stack[:len(tv.stack)-1] // pop
		return true
	}
	tv.stack = append(tv.stack, n) // push
	defer func() {
		if !descend {
			tv.stack = tv.stack[:len(tv.stack)-1] // pop
		}
	}()

	switch n := n.(type) {
	case *ast.ArrayType:
	case *ast.AssignStmt:
		tv.token(n.TokPos, len(n.Tok.String()), semtok.TokOperator, nil)
	case *ast.BasicLit:
		if strings.Contains(n.Value, "\n") {
			// has to be a string.
			tv.multiline(n.Pos(), n.End(), semtok.TokString)
			break
		}
		what := semtok.TokNumber
		if n.Kind == token.STRING {
			what = semtok.TokString
		}
		tv.token(n.Pos(), len(n.Value), what, nil)
	case *ast.BinaryExpr:
		tv.token(n.OpPos, len(n.Op.String()), semtok.TokOperator, nil)
	case *ast.BlockStmt:
	case *ast.BranchStmt:
		tv.token(n.TokPos, len(n.Tok.String()), semtok.TokKeyword, nil)
		if n.Label != nil {
			tv.token(n.Label.Pos(), len(n.Label.Name), semtok.TokLabel, nil)
		}
	case *ast.CallExpr:
		if n.Ellipsis.IsValid() {
			tv.token(n.Ellipsis, len("..."), semtok.TokOperator, nil)
		}
	case *ast.CaseClause:
		iam := "case"
		if n.List == nil {
			iam = "default"
		}
		tv.token(n.Case, len(iam), semtok.TokKeyword, nil)
	case *ast.ChanType:
		// chan | chan <- | <- chan
		switch {
		case n.Arrow == token.NoPos:
			tv.token(n.Begin, len("chan"), semtok.TokKeyword, nil)
		case n.Arrow == n.Begin:
			tv.token(n.Arrow, 2, semtok.TokOperator, nil)
			pos := tv.findKeyword("chan", n.Begin+2, n.Value.Pos())
			tv.token(pos, len("chan"), semtok.TokKeyword, nil)
		case n.Arrow != n.Begin:
			tv.token(n.Begin, len("chan"), semtok.TokKeyword, nil)
			tv.token(n.Arrow, 2, semtok.TokOperator, nil)
		}
	case *ast.CommClause:
		length := len("case")
		if n.Comm == nil {
			length = len("default")
		}
		tv.token(n.Case, length, semtok.TokKeyword, nil)
	case *ast.CompositeLit:
	case *ast.DeclStmt:
	case *ast.DeferStmt:
		tv.token(n.Defer, len("defer"), semtok.TokKeyword, nil)
	case *ast.Ellipsis:
		tv.token(n.Ellipsis, len("..."), semtok.TokOperator, nil)
	case *ast.EmptyStmt:
	case *ast.ExprStmt:
	case *ast.Field:
	case *ast.FieldList:
	case *ast.ForStmt:
		tv.token(n.For, len("for"), semtok.TokKeyword, nil)
	case *ast.FuncDecl:
	case *ast.FuncLit:
	case *ast.FuncType:
		if n.Func != token.NoPos {
			tv.token(n.Func, len("func"), semtok.TokKeyword, nil)
		}
	case *ast.GenDecl:
		tv.token(n.TokPos, len(n.Tok.String()), semtok.TokKeyword, nil)
	case *ast.GoStmt:
		tv.token(n.Go, len("go"), semtok.TokKeyword, nil)
	case *ast.Ident:
		tv.ident(n)
	case *ast.IfStmt:
		tv.token(n.If, len("if"), semtok.TokKeyword, nil)
		if n.Else != nil {
			// x.Body.End() or x.Body.End()+1, not that it matters
			pos := tv.findKeyword("else", n.Body.End(), n.Else.Pos())
			tv.token(pos, len("else"), semtok.TokKeyword, nil)
		}
	case *ast.ImportSpec:
		tv.importSpec(n)
		return false
	case *ast.IncDecStmt:
		tv.token(n.TokPos, len(n.Tok.String()), semtok.TokOperator, nil)
	case *ast.IndexExpr:
	case *ast.IndexListExpr:
	case *ast.InterfaceType:
		tv.token(n.Interface, len("interface"), semtok.TokKeyword, nil)
	case *ast.KeyValueExpr:
	case *ast.LabeledStmt:
		tv.token(n.Label.Pos(), len(n.Label.Name), semtok.TokLabel, []string{"definition"})
	case *ast.MapType:
		tv.token(n.Map, len("map"), semtok.TokKeyword, nil)
	case *ast.ParenExpr:
	case *ast.RangeStmt:
		tv.token(n.For, len("for"), semtok.TokKeyword, nil)
		// x.TokPos == token.NoPos is legal (for range foo {})
		offset := n.TokPos
		if offset == token.NoPos {
			offset = n.For
		}
		pos := tv.findKeyword("range", offset, n.X.Pos())
		tv.token(pos, len("range"), semtok.TokKeyword, nil)
	case *ast.ReturnStmt:
		tv.token(n.Return, len("return"), semtok.TokKeyword, nil)
	case *ast.SelectStmt:
		tv.token(n.Select, len("select"), semtok.TokKeyword, nil)
	case *ast.SelectorExpr:
	case *ast.SendStmt:
		tv.token(n.Arrow, len("<-"), semtok.TokOperator, nil)
	case *ast.SliceExpr:
	case *ast.StarExpr:
		tv.token(n.Star, len("*"), semtok.TokOperator, nil)
	case *ast.StructType:
		tv.token(n.Struct, len("struct"), semtok.TokKeyword, nil)
	case *ast.SwitchStmt:
		tv.token(n.Switch, len("switch"), semtok.TokKeyword, nil)
	case *ast.TypeAssertExpr:
		if n.Type == nil {
			pos := tv.findKeyword("type", n.Lparen, n.Rparen)
			tv.token(pos, len("type"), semtok.TokKeyword, nil)
		}
	case *ast.TypeSpec:
	case *ast.TypeSwitchStmt:
		tv.token(n.Switch, len("switch"), semtok.TokKeyword, nil)
	case *ast.UnaryExpr:
		tv.token(n.OpPos, len(n.Op.String()), semtok.TokOperator, nil)
	case *ast.ValueSpec:
	// things only seen with parsing or type errors, so ignore them
	case *ast.BadDecl, *ast.BadExpr, *ast.BadStmt:
		return false
	// not going to see these
	case *ast.File, *ast.Package:
		tv.errorf("implement %T %s", n, safetoken.Position(tv.pgf.Tok, n.Pos()))
	// other things we knowingly ignore
	case *ast.Comment, *ast.CommentGroup:
		return false
	default:
		tv.errorf("failed to implement %T", n)
	}
	return true
}

func (tv *tokenVisitor) ident(id *ast.Ident) {
	var obj types.Object

	// emit emits a token for the identifier's extent.
	emit := func(tok semtok.TokenType, modifiers ...string) {
		tv.token(id.Pos(), len(id.Name), tok, modifiers)
		if semDebug {
			q := "nil"
			if obj != nil {
				q = fmt.Sprintf("%T", obj.Type()) // e.g. "*types.Map"
			}
			log.Printf(" use %s/%T/%s got %s %v (%s)",
				id.Name, obj, q, tok, modifiers, tv.strStack())
		}
	}

	// definition?
	obj = tv.info.Defs[id]
	if obj != nil {
		if tok, modifiers := tv.definitionFor(id, obj); tok != "" {
			emit(tok, modifiers...)
		} else if semDebug {
			log.Printf(" for %s/%T/%T got '' %v (%s)",
				id.Name, obj, obj.Type(), modifiers, tv.strStack())
		}
		return
	}

	// use?
	obj = tv.info.Uses[id]
	switch obj := obj.(type) {
	case *types.Builtin:
		emit(semtok.TokFunction, "defaultLibrary")
	case *types.Const:
		if is[*types.Named](obj.Type()) &&
			(id.Name == "iota" || id.Name == "true" || id.Name == "false") {
			emit(semtok.TokVariable, "readonly", "defaultLibrary")
		} else {
			emit(semtok.TokVariable, "readonly")
		}
	case *types.Func:
		emit(semtok.TokFunction)
	case *types.Label:
		// Labels are reliably covered by the syntax traversal.
	case *types.Nil:
		// nil is a predeclared identifier
		emit(semtok.TokVariable, "readonly", "defaultLibrary")
	case *types.PkgName:
		emit(semtok.TokNamespace)
	case *types.TypeName: // could be a TypeParam
		if is[*types.TypeParam](aliases.Unalias(obj.Type())) {
			emit(semtok.TokTypeParam)
		} else if is[*types.Basic](obj.Type()) {
			emit(semtok.TokType, "defaultLibrary")
		} else {
			emit(semtok.TokType)
		}
	case *types.Var:
		if is[*types.Signature](aliases.Unalias(obj.Type())) {
			emit(semtok.TokFunction)
		} else if tv.isParam(obj.Pos()) {
			// variable, unless use.pos is the pos of a Field in an ancestor FuncDecl
			// or FuncLit and then it's a parameter
			emit(semtok.TokParameter)
		} else {
			emit(semtok.TokVariable)
		}
	case nil:
		if tok, modifiers := tv.unkIdent(id); tok != "" {
			emit(tok, modifiers...)
		}
	default:
		panic(obj)
	}
}

// isParam reports whether the position is that of a parameter name of
// an enclosing function.
func (tv *tokenVisitor) isParam(pos token.Pos) bool {
	for i := len(tv.stack) - 1; i >= 0; i-- {
		switch n := tv.stack[i].(type) {
		case *ast.FuncDecl:
			for _, f := range n.Type.Params.List {
				for _, id := range f.Names {
					if id.Pos() == pos {
						return true
					}
				}
			}
		case *ast.FuncLit:
			for _, f := range n.Type.Params.List {
				for _, id := range f.Names {
					if id.Pos() == pos {
						return true
					}
				}
			}
		}
	}
	return false
}

// unkIdent handles identifiers with no types.Object (neither use nor
// def), use the parse stack.
// A lot of these only happen when the package doesn't compile,
// but in that case it is all best-effort from the parse tree.
func (tv *tokenVisitor) unkIdent(id *ast.Ident) (semtok.TokenType, []string) {
	def := []string{"definition"}
	n := len(tv.stack) - 2 // parent of Ident; stack is [File ... Ident]
	if n < 0 {
		tv.errorf("no stack") // can't happen
		return "", nil
	}
	switch parent := tv.stack[n].(type) {
	case *ast.BinaryExpr, *ast.UnaryExpr, *ast.ParenExpr, *ast.StarExpr,
		*ast.IncDecStmt, *ast.SliceExpr, *ast.ExprStmt, *ast.IndexExpr,
		*ast.ReturnStmt, *ast.ChanType, *ast.SendStmt,
		*ast.ForStmt,      // possibly incomplete
		*ast.IfStmt,       /* condition */
		*ast.KeyValueExpr, // either key or value
		*ast.IndexListExpr:
		return semtok.TokVariable, nil
	case *ast.Ellipsis:
		return semtok.TokType, nil
	case *ast.CaseClause:
		if n-2 >= 0 && is[ast.TypeSwitchStmt](tv.stack[n-2]) {
			return semtok.TokType, nil
		}
		return semtok.TokVariable, nil
	case *ast.ArrayType:
		if id == parent.Len {
			// or maybe a Type Param, but we can't just from the parse tree
			return semtok.TokVariable, nil
		} else {
			return semtok.TokType, nil
		}
	case *ast.MapType:
		return semtok.TokType, nil
	case *ast.CallExpr:
		if id == parent.Fun {
			return semtok.TokFunction, nil
		}
		return semtok.TokVariable, nil
	case *ast.SwitchStmt:
		return semtok.TokVariable, nil
	case *ast.TypeAssertExpr:
		if id == parent.X {
			return semtok.TokVariable, nil
		} else if id == parent.Type {
			return semtok.TokType, nil
		}
	case *ast.ValueSpec:
		for _, p := range parent.Names {
			if p == id {
				return semtok.TokVariable, def
			}
		}
		for _, p := range parent.Values {
			if p == id {
				return semtok.TokVariable, nil
			}
		}
		return semtok.TokType, nil
	case *ast.SelectorExpr: // e.ti.Selections[nd] is nil, so no help
		if n-1 >= 0 {
			if ce, ok := tv.stack[n-1].(*ast.CallExpr); ok {
				// ... CallExpr SelectorExpr Ident (_.x())
				if ce.Fun == parent && parent.Sel == id {
					return semtok.TokFunction, nil
				}
			}
		}
		return semtok.TokVariable, nil
	case *ast.AssignStmt:
		for _, p := range parent.Lhs {
			// x := ..., or x = ...
			if p == id {
				if parent.Tok != token.DEFINE {
					def = nil
				}
				return semtok.TokVariable, def // '_' in _ = ...
			}
		}
		// RHS, = x
		return semtok.TokVariable, nil
	case *ast.TypeSpec: // it's a type if it is either the Name or the Type
		if id == parent.Type {
			def = nil
		}
		return semtok.TokType, def
	case *ast.Field:
		// ident could be type in a field, or a method in an interface type, or a variable
		if id == parent.Type {
			return semtok.TokType, nil
		}
		if n > 2 &&
			is[*ast.InterfaceType](tv.stack[n-2]) &&
			is[*ast.FieldList](tv.stack[n-1]) {

			return semtok.TokMethod, def
		}
		return semtok.TokVariable, nil
	case *ast.LabeledStmt:
		if id == parent.Label {
			return semtok.TokLabel, def
		}
	case *ast.BranchStmt:
		if id == parent.Label {
			return semtok.TokLabel, nil
		}
	case *ast.CompositeLit:
		if parent.Type == id {
			return semtok.TokType, nil
		}
		return semtok.TokVariable, nil
	case *ast.RangeStmt:
		if parent.Tok != token.DEFINE {
			def = nil
		}
		return semtok.TokVariable, def
	case *ast.FuncDecl:
		return semtok.TokFunction, def
	default:
		tv.errorf("%T unexpected: %s %s%q", parent, id.Name, tv.strStack(), tv.srcLine(id))
	}
	return "", nil
}

func isDeprecated(n *ast.CommentGroup) bool {
	if n != nil {
		for _, c := range n.List {
			if strings.HasPrefix(c.Text, "// Deprecated") {
				return true
			}
		}
	}
	return false
}

// definitionFor handles a defining identifier.
func (tv *tokenVisitor) definitionFor(id *ast.Ident, obj types.Object) (semtok.TokenType, []string) {
	// The definition of a types.Label cannot be found by
	// ascending the syntax tree, and doing so will reach the
	// FuncDecl, causing us to misinterpret the label as a
	// parameter (#65494).
	//
	// However, labels are reliably covered by the syntax
	// traversal, so we don't need to use type information.
	if is[*types.Label](obj) {
		return "", nil
	}

	// PJW: look into replacing these syntactic tests with types more generally
	modifiers := []string{"definition"}
	for i := len(tv.stack) - 1; i >= 0; i-- {
		switch ancestor := tv.stack[i].(type) {
		case *ast.AssignStmt, *ast.RangeStmt:
			if id.Name == "_" {
				return "", nil // not really a variable
			}
			return semtok.TokVariable, modifiers
		case *ast.GenDecl:
			if isDeprecated(ancestor.Doc) {
				modifiers = append(modifiers, "deprecated")
			}
			if ancestor.Tok == token.CONST {
				modifiers = append(modifiers, "readonly")
			}
			return semtok.TokVariable, modifiers
		case *ast.FuncDecl:
			// If x is immediately under a FuncDecl, it is a function or method
			if i == len(tv.stack)-2 {
				if isDeprecated(ancestor.Doc) {
					modifiers = append(modifiers, "deprecated")
				}
				if ancestor.Recv != nil {
					return semtok.TokMethod, modifiers
				}
				return semtok.TokFunction, modifiers
			}
			// if x < ... < FieldList < FuncDecl, this is the receiver, a variable
			// PJW: maybe not. it might be a typeparameter in the type of the receiver
			if is[*ast.FieldList](tv.stack[i+1]) {
				if is[*types.TypeName](obj) {
					return semtok.TokTypeParam, modifiers
				}
				return semtok.TokVariable, nil
			}
			// if x < ... < FieldList < FuncType < FuncDecl, this is a param
			return semtok.TokParameter, modifiers
		case *ast.FuncType:
			if isTypeParam(id, ancestor) {
				return semtok.TokTypeParam, modifiers
			}
			return semtok.TokParameter, modifiers
		case *ast.InterfaceType:
			return semtok.TokMethod, modifiers
		case *ast.TypeSpec:
			// GenDecl/Typespec/FuncType/FieldList/Field/Ident
			// (type A func(b uint64)) (err error)
			// b and err should not be semtok.TokType, but semtok.TokVariable
			// and in GenDecl/TpeSpec/StructType/FieldList/Field/Ident
			// (type A struct{b uint64}
			// but on type B struct{C}), C is a type, but is not being defined.
			// GenDecl/TypeSpec/FieldList/Field/Ident is a typeParam
			if is[*ast.FieldList](tv.stack[i+1]) {
				return semtok.TokTypeParam, modifiers
			}
			fldm := tv.stack[len(tv.stack)-2]
			if fld, ok := fldm.(*ast.Field); ok {
				// if len(fld.names) == 0 this is a semtok.TokType, being used
				if len(fld.Names) == 0 {
					return semtok.TokType, nil
				}
				return semtok.TokVariable, modifiers
			}
			return semtok.TokType, modifiers
		}
	}
	// can't happen
	tv.errorf("failed to find the decl for %s", safetoken.Position(tv.pgf.Tok, id.Pos()))
	return "", nil
}

func isTypeParam(id *ast.Ident, t *ast.FuncType) bool {
	if tp := t.TypeParams; tp != nil {
		for _, p := range tp.List {
			for _, n := range p.Names {
				if id == n {
					return true
				}
			}
		}
	}
	return false
}

// multiline emits a multiline token (`string` or /*comment*/).
func (tv *tokenVisitor) multiline(start, end token.Pos, tok semtok.TokenType) {
	// TODO(adonovan): test with non-ASCII.

	f := tv.fset.File(start)
	// the hard part is finding the lengths of lines. include the \n
	length := func(line int) int {
		n := f.LineStart(line)
		if line >= f.LineCount() {
			return f.Size() - int(n)
		}
		return int(f.LineStart(line+1) - n)
	}
	spos := safetoken.StartPosition(tv.fset, start)
	epos := safetoken.EndPosition(tv.fset, end)
	sline := spos.Line
	eline := epos.Line
	// first line is from spos.Column to end
	tv.token(start, length(sline)-spos.Column, tok, nil) // leng(sline)-1 - (spos.Column-1)
	for i := sline + 1; i < eline; i++ {
		// intermediate lines are from 1 to end
		tv.token(f.LineStart(i), length(i)-1, tok, nil) // avoid the newline
	}
	// last line is from 1 to epos.Column
	tv.token(f.LineStart(eline), epos.Column-1, tok, nil) // columns are 1-based
}

// findKeyword returns the position of a keyword by searching within
// the specified range, for when its cannot be exactly known from the AST.
func (tv *tokenVisitor) findKeyword(keyword string, start, end token.Pos) token.Pos {
	// TODO(adonovan): use safetoken.Offset.
	offset := int(start) - tv.pgf.Tok.Base()
	last := int(end) - tv.pgf.Tok.Base()
	buf := tv.pgf.Src
	idx := bytes.Index(buf[offset:last], []byte(keyword))
	if idx != -1 {
		return start + token.Pos(idx)
	}
	//(in unparsable programs: type _ <-<-chan int)
	tv.errorf("not found:%s %v", keyword, safetoken.StartPosition(tv.fset, start))
	return token.NoPos
}

func (tv *tokenVisitor) importSpec(spec *ast.ImportSpec) {
	// a local package name or the last component of the Path
	if spec.Name != nil {
		name := spec.Name.String()
		if name != "_" && name != "." {
			tv.token(spec.Name.Pos(), len(name), semtok.TokNamespace, nil)
		}
		return // don't mark anything for . or _
	}
	importPath := metadata.UnquoteImportPath(spec)
	if importPath == "" {
		return
	}
	// Import strings are implementation defined. Try to match with parse information.
	depID := tv.metadata.DepsByImpPath[importPath]
	if depID == "" {
		return
	}
	depMD := tv.metadataSource.Metadata(depID)
	if depMD == nil {
		// unexpected, but impact is that maybe some import is not colored
		return
	}
	// Check whether the original literal contains the package's declared name.
	j := strings.LastIndex(spec.Path.Value, string(depMD.Name))
	if j < 0 {
		// Package name does not match import path, so there is nothing to report.
		return
	}
	// Report virtual declaration at the position of the substring.
	start := spec.Path.Pos() + token.Pos(j)
	tv.token(start, len(depMD.Name), semtok.TokNamespace, nil)
}

// errorf logs an error and reports a bug.
func (tv *tokenVisitor) errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	bug.Report(msg)
	event.Error(tv.ctx, tv.strStack(), errors.New(msg))
}

var godirectives = map[string]struct{}{
	// https://pkg.go.dev/cmd/compile
	"noescape":       {},
	"uintptrescapes": {},
	"noinline":       {},
	"norace":         {},
	"nosplit":        {},
	"linkname":       {},

	// https://pkg.go.dev/go/build
	"build":               {},
	"binary-only-package": {},
	"embed":               {},
}

// Tokenize godirective at the start of the comment c, if any, and the surrounding comment.
// If there is any failure, emits the entire comment as a TokComment token.
// Directives are highlighted as-is, even if used incorrectly. Typically there are
// dedicated analyzers that will warn about misuse.
func (tv *tokenVisitor) godirective(c *ast.Comment) {
	// First check if '//go:directive args...' is a valid directive.
	directive, args, _ := strings.Cut(c.Text, " ")
	kind, _ := stringsCutPrefix(directive, "//go:")
	if _, ok := godirectives[kind]; !ok {
		// Unknown 'go:' directive.
		tv.token(c.Pos(), len(c.Text), semtok.TokComment, nil)
		return
	}

	// Make the 'go:directive' part stand out, the rest is comments.
	tv.token(c.Pos(), len("//"), semtok.TokComment, nil)

	directiveStart := c.Pos() + token.Pos(len("//"))
	tv.token(directiveStart, len(directive[len("//"):]), semtok.TokNamespace, nil)

	if len(args) > 0 {
		tailStart := c.Pos() + token.Pos(len(directive)+len(" "))
		tv.token(tailStart, len(args), semtok.TokComment, nil)
	}
}

// Go 1.20 strings.CutPrefix.
func stringsCutPrefix(s, prefix string) (after string, found bool) {
	if !strings.HasPrefix(s, prefix) {
		return s, false
	}
	return s[len(prefix):], true
}

func is[T any](x any) bool {
	_, ok := x.(T)
	return ok
}
