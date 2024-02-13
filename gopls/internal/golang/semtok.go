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
	"strings"
	"time"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/semtok"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/event"
)

// to control comprehensive logging of decisions (gopls semtok foo.go > /dev/null shows log output)
// semDebug should NEVER be true in checked-in code
const semDebug = false

// The LSP says that errors for the semantic token requests should only be returned
// for exceptions (a word not otherwise defined). This code treats a too-large file
// as an exception. On parse errors, the code does what it can.

// reject full semantic token requests for large files
const maxFullFileSize int = 100000

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
	if int(end-start) > maxFullFileSize {
		err := fmt.Errorf("semantic tokens: range %s too large (%d > %d)",
			fh.URI().Path(), end-start, maxFullFileSize)
		return nil, err
	}

	tv := &tokenVisitor{
		ctx:            ctx,
		metadataSource: snapshot,
		pgf:            pgf,
		start:          start,
		end:            end,
		ti:             pkg.GetTypesInfo(),
		pkg:            pkg,
		fset:           pkg.FileSet(),
	}
	tv.visit()
	return &protocol.SemanticTokens{
		Data: semtok.Encode(
			tv.items,
			snapshot.Options().NoSemanticString,
			snapshot.Options().NoSemanticNumber,
			snapshot.Options().SemanticTypes,
			snapshot.Options().SemanticMods),
		// For delta requests, but we've never seen any.
		ResultID: time.Now().String(),
	}, nil
}

type tokenVisitor struct {
	items []semtok.Token
	ctx   context.Context // for event logging
	// metadataSource is used to resolve imports
	metadataSource metadata.Source
	pgf            *ParsedGoFile
	start, end     token.Pos // range of interest
	ti             *types.Info
	pkg            *cache.Package
	fset           *token.FileSet
	// path from the root of the parse tree, used for debugging
	stack []ast.Node
}

func (tv *tokenVisitor) visit() {
	f := tv.pgf.File
	// may not be in range, but harmless
	tv.token(f.Package, len("package"), semtok.TokKeyword, nil)
	tv.token(f.Name.NamePos, len(f.Name.Name), semtok.TokNamespace, nil)
	inspect := func(n ast.Node) bool {
		return tv.inspector(n)
	}
	for _, d := range f.Decls {
		// only look at the decls that overlap the range
		start, end := d.Pos(), d.End()
		if end <= tv.start || start >= tv.end {
			continue
		}
		ast.Inspect(d, inspect)
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:") {
				tv.godirective(c)
				continue
			}
			if !strings.Contains(c.Text, "\n") {
				tv.token(c.Pos(), len(c.Text), semtok.TokComment, nil)
				continue
			}
			tv.multiline(c.Pos(), c.End(), c.Text, semtok.TokComment)
		}
	}
}

func (tv *tokenVisitor) token(start token.Pos, leng int, typ semtok.TokenType, mods []string) {
	if leng <= 0 {
		return // vscode doesn't like 0-length Tokens
	}
	if !start.IsValid() {
		// This is not worth reporting. TODO(pjw): does it still happen?
		return
	}
	if start >= tv.end || start+token.Pos(leng) <= tv.start {
		return
	}
	// want a line and column from start (in LSP coordinates). Ignore line directives.
	lspRange, err := tv.pgf.PosRange(start, start+token.Pos(leng))
	if err != nil {
		event.Error(tv.ctx, "failed to convert to range", err)
		return
	}
	if lspRange.End.Line != lspRange.Start.Line {
		// this happens if users are typing at the end of the file, but report nothing
		return
	}
	tv.items = append(tv.items, semtok.Token{
		Line:      lspRange.Start.Line,
		Start:     lspRange.Start.Character,
		Len:       lspRange.End.Character - lspRange.Start.Character, // all on one line
		Type:      typ,
		Modifiers: mods,
	})
}

// convert the stack to a string, for debugging
func (tv *tokenVisitor) strStack() string {
	msg := []string{"["}
	for i := len(tv.stack) - 1; i >= 0; i-- {
		s := tv.stack[i]
		msg = append(msg, fmt.Sprintf("%T", s)[5:])
	}
	if len(tv.stack) > 0 {
		loc := tv.stack[len(tv.stack)-1].Pos()
		if _, err := safetoken.Offset(tv.pgf.Tok, loc); err != nil {
			msg = append(msg, fmt.Sprintf("invalid position %v for %s", loc, tv.pgf.URI))
		} else {
			add := safetoken.Position(tv.pgf.Tok, loc)
			nm := filepath.Base(add.Filename)
			msg = append(msg, fmt.Sprintf("(%s:%d,col:%d)", nm, add.Line, add.Column))
		}
	}
	msg = append(msg, "]")
	return strings.Join(msg, " ")
}

// find the line in the source
func (tv *tokenVisitor) srcLine(x ast.Node) string {
	file := tv.pgf.Tok
	line := safetoken.Line(file, x.Pos())
	start, err := safetoken.Offset(file, file.LineStart(line))
	if err != nil {
		return ""
	}
	end := start
	for ; end < len(tv.pgf.Src) && tv.pgf.Src[end] != '\n'; end++ {

	}
	ans := tv.pgf.Src[start:end]
	return string(ans)
}

func (tv *tokenVisitor) inspector(n ast.Node) bool {
	pop := func() {
		tv.stack = tv.stack[:len(tv.stack)-1]
	}
	if n == nil {
		pop()
		return true
	}
	tv.stack = append(tv.stack, n)
	switch x := n.(type) {
	case *ast.ArrayType:
	case *ast.AssignStmt:
		tv.token(x.TokPos, len(x.Tok.String()), semtok.TokOperator, nil)
	case *ast.BasicLit:
		if strings.Contains(x.Value, "\n") {
			// has to be a string.
			tv.multiline(x.Pos(), x.End(), x.Value, semtok.TokString)
			break
		}
		ln := len(x.Value)
		what := semtok.TokNumber
		if x.Kind == token.STRING {
			what = semtok.TokString
		}
		tv.token(x.Pos(), ln, what, nil)
	case *ast.BinaryExpr:
		tv.token(x.OpPos, len(x.Op.String()), semtok.TokOperator, nil)
	case *ast.BlockStmt:
	case *ast.BranchStmt:
		tv.token(x.TokPos, len(x.Tok.String()), semtok.TokKeyword, nil)
		// There's no semantic encoding for labels
	case *ast.CallExpr:
		if x.Ellipsis != token.NoPos {
			tv.token(x.Ellipsis, len("..."), semtok.TokOperator, nil)
		}
	case *ast.CaseClause:
		iam := "case"
		if x.List == nil {
			iam = "default"
		}
		tv.token(x.Case, len(iam), semtok.TokKeyword, nil)
	case *ast.ChanType:
		// chan | chan <- | <- chan
		switch {
		case x.Arrow == token.NoPos:
			tv.token(x.Begin, len("chan"), semtok.TokKeyword, nil)
		case x.Arrow == x.Begin:
			tv.token(x.Arrow, 2, semtok.TokOperator, nil)
			pos := tv.findKeyword("chan", x.Begin+2, x.Value.Pos())
			tv.token(pos, len("chan"), semtok.TokKeyword, nil)
		case x.Arrow != x.Begin:
			tv.token(x.Begin, len("chan"), semtok.TokKeyword, nil)
			tv.token(x.Arrow, 2, semtok.TokOperator, nil)
		}
	case *ast.CommClause:
		iam := len("case")
		if x.Comm == nil {
			iam = len("default")
		}
		tv.token(x.Case, iam, semtok.TokKeyword, nil)
	case *ast.CompositeLit:
	case *ast.DeclStmt:
	case *ast.DeferStmt:
		tv.token(x.Defer, len("defer"), semtok.TokKeyword, nil)
	case *ast.Ellipsis:
		tv.token(x.Ellipsis, len("..."), semtok.TokOperator, nil)
	case *ast.EmptyStmt:
	case *ast.ExprStmt:
	case *ast.Field:
	case *ast.FieldList:
	case *ast.ForStmt:
		tv.token(x.For, len("for"), semtok.TokKeyword, nil)
	case *ast.FuncDecl:
	case *ast.FuncLit:
	case *ast.FuncType:
		if x.Func != token.NoPos {
			tv.token(x.Func, len("func"), semtok.TokKeyword, nil)
		}
	case *ast.GenDecl:
		tv.token(x.TokPos, len(x.Tok.String()), semtok.TokKeyword, nil)
	case *ast.GoStmt:
		tv.token(x.Go, len("go"), semtok.TokKeyword, nil)
	case *ast.Ident:
		tv.ident(x)
	case *ast.IfStmt:
		tv.token(x.If, len("if"), semtok.TokKeyword, nil)
		if x.Else != nil {
			// x.Body.End() or x.Body.End()+1, not that it matters
			pos := tv.findKeyword("else", x.Body.End(), x.Else.Pos())
			tv.token(pos, len("else"), semtok.TokKeyword, nil)
		}
	case *ast.ImportSpec:
		tv.importSpec(x)
		pop()
		return false
	case *ast.IncDecStmt:
		tv.token(x.TokPos, len(x.Tok.String()), semtok.TokOperator, nil)
	case *ast.IndexExpr:
	case *ast.IndexListExpr:
	case *ast.InterfaceType:
		tv.token(x.Interface, len("interface"), semtok.TokKeyword, nil)
	case *ast.KeyValueExpr:
	case *ast.LabeledStmt:
	case *ast.MapType:
		tv.token(x.Map, len("map"), semtok.TokKeyword, nil)
	case *ast.ParenExpr:
	case *ast.RangeStmt:
		tv.token(x.For, len("for"), semtok.TokKeyword, nil)
		// x.TokPos == token.NoPos is legal (for range foo {})
		offset := x.TokPos
		if offset == token.NoPos {
			offset = x.For
		}
		pos := tv.findKeyword("range", offset, x.X.Pos())
		tv.token(pos, len("range"), semtok.TokKeyword, nil)
	case *ast.ReturnStmt:
		tv.token(x.Return, len("return"), semtok.TokKeyword, nil)
	case *ast.SelectStmt:
		tv.token(x.Select, len("select"), semtok.TokKeyword, nil)
	case *ast.SelectorExpr:
	case *ast.SendStmt:
		tv.token(x.Arrow, len("<-"), semtok.TokOperator, nil)
	case *ast.SliceExpr:
	case *ast.StarExpr:
		tv.token(x.Star, len("*"), semtok.TokOperator, nil)
	case *ast.StructType:
		tv.token(x.Struct, len("struct"), semtok.TokKeyword, nil)
	case *ast.SwitchStmt:
		tv.token(x.Switch, len("switch"), semtok.TokKeyword, nil)
	case *ast.TypeAssertExpr:
		if x.Type == nil {
			pos := tv.findKeyword("type", x.Lparen, x.Rparen)
			tv.token(pos, len("type"), semtok.TokKeyword, nil)
		}
	case *ast.TypeSpec:
	case *ast.TypeSwitchStmt:
		tv.token(x.Switch, len("switch"), semtok.TokKeyword, nil)
	case *ast.UnaryExpr:
		tv.token(x.OpPos, len(x.Op.String()), semtok.TokOperator, nil)
	case *ast.ValueSpec:
	// things only seen with parsing or type errors, so ignore them
	case *ast.BadDecl, *ast.BadExpr, *ast.BadStmt:
		return true
	// not going to see these
	case *ast.File, *ast.Package:
		tv.unexpected(fmt.Sprintf("implement %T %s", x, safetoken.Position(tv.pgf.Tok, x.Pos())))
	// other things we knowingly ignore
	case *ast.Comment, *ast.CommentGroup:
		pop()
		return false
	default:
		tv.unexpected(fmt.Sprintf("failed to implement %T", x))
	}
	return true
}

func (tv *tokenVisitor) ident(x *ast.Ident) {
	if tv.ti == nil {
		what, mods := tv.unkIdent(x)
		if what != "" {
			tv.token(x.Pos(), len(x.String()), what, mods)
		}
		if semDebug {
			log.Printf(" nil %s/nil/nil %q %v %s", x.String(), what, mods, tv.strStack())
		}
		return
	}
	def := tv.ti.Defs[x]
	if def != nil {
		what, mods := tv.definitionFor(x, def)
		if what != "" {
			tv.token(x.Pos(), len(x.String()), what, mods)
		}
		if semDebug {
			log.Printf(" for %s/%T/%T got %s %v (%s)", x.String(), def, def.Type(), what, mods, tv.strStack())
		}
		return
	}
	use := tv.ti.Uses[x]
	tok := func(pos token.Pos, lng int, tok semtok.TokenType, mods []string) {
		tv.token(pos, lng, tok, mods)
		q := "nil"
		if use != nil {
			q = fmt.Sprintf("%T", use.Type())
		}
		if semDebug {
			log.Printf(" use %s/%T/%s got %s %v (%s)", x.String(), use, q, tok, mods, tv.strStack())
		}
	}

	switch y := use.(type) {
	case nil:
		what, mods := tv.unkIdent(x)
		if what != "" {
			tok(x.Pos(), len(x.String()), what, mods)
		} else if semDebug {
			// tok() wasn't called, so didn't log
			log.Printf(" nil %s/%T/nil %q %v (%s)", x.String(), use, what, mods, tv.strStack())
		}
		return
	case *types.Builtin:
		tok(x.NamePos, len(x.Name), semtok.TokFunction, []string{"defaultLibrary"})
	case *types.Const:
		mods := []string{"readonly"}
		tt := y.Type()
		if _, ok := tt.(*types.Basic); ok {
			tok(x.Pos(), len(x.String()), semtok.TokVariable, mods)
			break
		}
		if ttx, ok := tt.(*types.Named); ok {
			if x.String() == "iota" {
				tv.unexpected(fmt.Sprintf("iota:%T", ttx))
			}
			if _, ok := ttx.Underlying().(*types.Basic); ok {
				tok(x.Pos(), len(x.String()), semtok.TokVariable, mods)
				break
			}
			tv.unexpected(fmt.Sprintf("%q/%T", x.String(), tt))
		}
		// can this happen? Don't think so
		tv.unexpected(fmt.Sprintf("%s %T %#v", x.String(), tt, tt))
	case *types.Func:
		tok(x.Pos(), len(x.Name), semtok.TokFunction, nil)
	case *types.Label:
		// nothing to map it to
	case *types.Nil:
		// nil is a predeclared identifier
		tok(x.Pos(), len("nil"), semtok.TokVariable, []string{"readonly", "defaultLibrary"})
	case *types.PkgName:
		tok(x.Pos(), len(x.Name), semtok.TokNamespace, nil)
	case *types.TypeName: // could be a TokTypeParam
		var mods []string
		if _, ok := y.Type().(*types.Basic); ok {
			mods = []string{"defaultLibrary"}
		} else if _, ok := y.Type().(*types.TypeParam); ok {
			tok(x.Pos(), len(x.String()), semtok.TokTypeParam, mods)
			break
		}
		tok(x.Pos(), len(x.String()), semtok.TokType, mods)
	case *types.Var:
		if isSignature(y) {
			tok(x.Pos(), len(x.Name), semtok.TokFunction, nil)
		} else if tv.isParam(use.Pos()) {
			// variable, unless use.pos is the pos of a Field in an ancestor FuncDecl
			// or FuncLit and then it's a parameter
			tok(x.Pos(), len(x.Name), semtok.TokParameter, nil)
		} else {
			tok(x.Pos(), len(x.Name), semtok.TokVariable, nil)
		}

	default:
		// can't happen
		if use == nil {
			msg := fmt.Sprintf("%#v %#v %#v", x, tv.ti.Defs[x], tv.ti.Uses[x])
			tv.unexpected(msg)
		}
		if use.Type() != nil {
			tv.unexpected(fmt.Sprintf("%s %T/%T,%#v", x.String(), use, use.Type(), use))
		} else {
			tv.unexpected(fmt.Sprintf("%s %T", x.String(), use))
		}
	}
}

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

func isSignature(use types.Object) bool {
	if _, ok := use.(*types.Var); !ok {
		return false
	}
	v := use.Type()
	if v == nil {
		return false
	}
	if _, ok := v.(*types.Signature); ok {
		return true
	}
	return false
}

// both tv.ti.Defs and tv.ti.Uses are nil. use the parse stack.
// a lot of these only happen when the package doesn't compile
// but in that case it is all best-effort from the parse tree
func (tv *tokenVisitor) unkIdent(x *ast.Ident) (semtok.TokenType, []string) {
	def := []string{"definition"}
	n := len(tv.stack) - 2 // parent of Ident
	if n < 0 {
		tv.unexpected("no stack?")
		return "", nil
	}
	switch nd := tv.stack[n].(type) {
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
		if n-2 >= 0 {
			if _, ok := tv.stack[n-2].(*ast.TypeSwitchStmt); ok {
				return semtok.TokType, nil
			}
		}
		return semtok.TokVariable, nil
	case *ast.ArrayType:
		if x == nd.Len {
			// or maybe a Type Param, but we can't just from the parse tree
			return semtok.TokVariable, nil
		} else {
			return semtok.TokType, nil
		}
	case *ast.MapType:
		return semtok.TokType, nil
	case *ast.CallExpr:
		if x == nd.Fun {
			return semtok.TokFunction, nil
		}
		return semtok.TokVariable, nil
	case *ast.SwitchStmt:
		return semtok.TokVariable, nil
	case *ast.TypeAssertExpr:
		if x == nd.X {
			return semtok.TokVariable, nil
		} else if x == nd.Type {
			return semtok.TokType, nil
		}
	case *ast.ValueSpec:
		for _, p := range nd.Names {
			if p == x {
				return semtok.TokVariable, def
			}
		}
		for _, p := range nd.Values {
			if p == x {
				return semtok.TokVariable, nil
			}
		}
		return semtok.TokType, nil
	case *ast.SelectorExpr: // e.ti.Selections[nd] is nil, so no help
		if n-1 >= 0 {
			if ce, ok := tv.stack[n-1].(*ast.CallExpr); ok {
				// ... CallExpr SelectorExpr Ident (_.x())
				if ce.Fun == nd && nd.Sel == x {
					return semtok.TokFunction, nil
				}
			}
		}
		return semtok.TokVariable, nil
	case *ast.AssignStmt:
		for _, p := range nd.Lhs {
			// x := ..., or x = ...
			if p == x {
				if nd.Tok != token.DEFINE {
					def = nil
				}
				return semtok.TokVariable, def // '_' in _ = ...
			}
		}
		// RHS, = x
		return semtok.TokVariable, nil
	case *ast.TypeSpec: // it's a type if it is either the Name or the Type
		if x == nd.Type {
			def = nil
		}
		return semtok.TokType, def
	case *ast.Field:
		// ident could be type in a field, or a method in an interface type, or a variable
		if x == nd.Type {
			return semtok.TokType, nil
		}
		if n-2 >= 0 {
			_, okit := tv.stack[n-2].(*ast.InterfaceType)
			_, okfl := tv.stack[n-1].(*ast.FieldList)
			if okit && okfl {
				return semtok.TokMethod, def
			}
		}
		return semtok.TokVariable, nil
	case *ast.LabeledStmt, *ast.BranchStmt:
		// nothing to report
	case *ast.CompositeLit:
		if nd.Type == x {
			return semtok.TokType, nil
		}
		return semtok.TokVariable, nil
	case *ast.RangeStmt:
		if nd.Tok != token.DEFINE {
			def = nil
		}
		return semtok.TokVariable, def
	case *ast.FuncDecl:
		return semtok.TokFunction, def
	default:
		msg := fmt.Sprintf("%T undexpected: %s %s%q", nd, x.Name, tv.strStack(), tv.srcLine(x))
		tv.unexpected(msg)
	}
	return "", nil
}

func isDeprecated(n *ast.CommentGroup) bool {
	if n == nil {
		return false
	}
	for _, c := range n.List {
		if strings.HasPrefix(c.Text, "// Deprecated") {
			return true
		}
	}
	return false
}

func (tv *tokenVisitor) definitionFor(x *ast.Ident, def types.Object) (semtok.TokenType, []string) {
	// PJW: def == types.Label? probably a nothing
	// PJW: look into replacing these syntactic tests with types more generally
	mods := []string{"definition"}
	for i := len(tv.stack) - 1; i >= 0; i-- {
		s := tv.stack[i]
		switch y := s.(type) {
		case *ast.AssignStmt, *ast.RangeStmt:
			if x.Name == "_" {
				return "", nil // not really a variable
			}
			return semtok.TokVariable, mods
		case *ast.GenDecl:
			if isDeprecated(y.Doc) {
				mods = append(mods, "deprecated")
			}
			if y.Tok == token.CONST {
				mods = append(mods, "readonly")
			}
			return semtok.TokVariable, mods
		case *ast.FuncDecl:
			// If x is immediately under a FuncDecl, it is a function or method
			if i == len(tv.stack)-2 {
				if isDeprecated(y.Doc) {
					mods = append(mods, "deprecated")
				}
				if y.Recv != nil {
					return semtok.TokMethod, mods
				}
				return semtok.TokFunction, mods
			}
			// if x < ... < FieldList < FuncDecl, this is the receiver, a variable
			// PJW: maybe not. it might be a typeparameter in the type of the receiver
			if _, ok := tv.stack[i+1].(*ast.FieldList); ok {
				if _, ok := def.(*types.TypeName); ok {
					return semtok.TokTypeParam, mods
				}
				return semtok.TokVariable, nil
			}
			// if x < ... < FieldList < FuncType < FuncDecl, this is a param
			return semtok.TokParameter, mods
		case *ast.FuncType: // is it in the TypeParams?
			if isTypeParam(x, y) {
				return semtok.TokTypeParam, mods
			}
			return semtok.TokParameter, mods
		case *ast.InterfaceType:
			return semtok.TokMethod, mods
		case *ast.TypeSpec:
			// GenDecl/Typespec/FuncType/FieldList/Field/Ident
			// (type A func(b uint64)) (err error)
			// b and err should not be semtok.TokType, but semtok.TokVariable
			// and in GenDecl/TpeSpec/StructType/FieldList/Field/Ident
			// (type A struct{b uint64}
			// but on type B struct{C}), C is a type, but is not being defined.
			// GenDecl/TypeSpec/FieldList/Field/Ident is a typeParam
			if _, ok := tv.stack[i+1].(*ast.FieldList); ok {
				return semtok.TokTypeParam, mods
			}
			fldm := tv.stack[len(tv.stack)-2]
			if fld, ok := fldm.(*ast.Field); ok {
				// if len(fld.names) == 0 this is a semtok.TokType, being used
				if len(fld.Names) == 0 {
					return semtok.TokType, nil
				}
				return semtok.TokVariable, mods
			}
			return semtok.TokType, mods
		}
	}
	// can't happen
	msg := fmt.Sprintf("failed to find the decl for %s", safetoken.Position(tv.pgf.Tok, x.Pos()))
	tv.unexpected(msg)
	return "", []string{""}
}

func isTypeParam(x *ast.Ident, y *ast.FuncType) bool {
	tp := y.TypeParams
	if tp == nil {
		return false
	}
	for _, p := range tp.List {
		for _, n := range p.Names {
			if x == n {
				return true
			}
		}
	}
	return false
}

func (tv *tokenVisitor) multiline(start, end token.Pos, val string, tok semtok.TokenType) {
	f := tv.fset.File(start)
	// the hard part is finding the lengths of lines. include the \n
	leng := func(line int) int {
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
	tv.token(start, leng(sline)-spos.Column, tok, nil) // leng(sline)-1 - (spos.Column-1)
	for i := sline + 1; i < eline; i++ {
		// intermediate lines are from 1 to end
		tv.token(f.LineStart(i), leng(i)-1, tok, nil) // avoid the newline
	}
	// last line is from 1 to epos.Column
	tv.token(f.LineStart(eline), epos.Column-1, tok, nil) // columns are 1-based
}

// findKeyword finds a keyword rather than guessing its location
func (tv *tokenVisitor) findKeyword(keyword string, start, end token.Pos) token.Pos {
	offset := int(start) - tv.pgf.Tok.Base()
	last := int(end) - tv.pgf.Tok.Base()
	buf := tv.pgf.Src
	idx := bytes.Index(buf[offset:last], []byte(keyword))
	if idx != -1 {
		return start + token.Pos(idx)
	}
	//(in unparsable programs: type _ <-<-chan int)
	tv.unexpected(fmt.Sprintf("not found:%s %v", keyword, safetoken.StartPosition(tv.fset, start)))
	return token.NoPos
}

func (tv *tokenVisitor) importSpec(d *ast.ImportSpec) {
	// a local package name or the last component of the Path
	if d.Name != nil {
		nm := d.Name.String()
		if nm != "_" && nm != "." {
			tv.token(d.Name.Pos(), len(nm), semtok.TokNamespace, nil)
		}
		return // don't mark anything for . or _
	}
	importPath := metadata.UnquoteImportPath(d)
	if importPath == "" {
		return
	}
	// Import strings are implementation defined. Try to match with parse information.
	depID := tv.pkg.Metadata().DepsByImpPath[importPath]
	if depID == "" {
		return
	}
	depMD := tv.metadataSource.Metadata(depID)
	if depMD == nil {
		// unexpected, but impact is that maybe some import is not colored
		return
	}
	// Check whether the original literal contains the package's declared name.
	j := strings.LastIndex(d.Path.Value, string(depMD.Name))
	if j == -1 {
		// Package name does not match import path, so there is nothing to report.
		return
	}
	// Report virtual declaration at the position of the substring.
	start := d.Path.Pos() + token.Pos(j)
	tv.token(start, len(depMD.Name), semtok.TokNamespace, nil)
}

// log unexpected state
func (tv *tokenVisitor) unexpected(msg string) {
	if semDebug {
		panic(msg)
	}
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
		// Unknown go: directive.
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
