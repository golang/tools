// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parsego

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"sync"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/astutil/cursor"
)

// A File contains the results of parsing a Go file.
type File struct {
	URI  protocol.DocumentURI
	Mode parser.Mode

	// File is the file resulting from parsing. It is always non-nil.
	//
	// Clients must not access the AST's legacy ast.Object-related
	// fields without first ensuring that [File.Resolve] was
	// already called.
	File *ast.File
	Tok  *token.File
	// Source code used to build the AST. It may be different from the
	// actual content of the file if we have fixed the AST.
	Src []byte

	Cursor cursor.Cursor // cursor of *ast.File, sans sibling files

	// fixedSrc and fixedAST report on "fixing" that occurred during parsing of
	// this file.
	//
	// fixedSrc means Src holds file content that was modified to improve parsing.
	// fixedAST means File was modified after parsing, so AST positions may not
	// reflect the content of Src.
	//
	// TODO(rfindley): there are many places where we haphazardly use the Src or
	// positions without checking these fields. Audit these places and guard
	// accordingly. After doing so, we may find that we don't need to
	// differentiate fixedSrc and fixedAST.
	fixedSrc bool
	fixedAST bool
	Mapper   *protocol.Mapper // may map fixed Src, not file content
	ParseErr scanner.ErrorList

	// resolveOnce guards the lazy ast.Object resolution. See [File.Resolve].
	resolveOnce sync.Once
}

func (pgf *File) String() string { return string(pgf.URI) }

// Fixed reports whether p was "Fixed", meaning that its source or positions
// may not correlate with the original file.
func (pgf *File) Fixed() bool {
	return pgf.fixedSrc || pgf.fixedAST
}

// -- go/token domain convenience helpers --

// PositionPos returns the token.Pos of protocol position p within the file.
func (pgf *File) PositionPos(p protocol.Position) (token.Pos, error) {
	offset, err := pgf.Mapper.PositionOffset(p)
	if err != nil {
		return token.NoPos, err
	}
	return safetoken.Pos(pgf.Tok, offset)
}

// PosPosition returns a protocol Position for the token.Pos in this file.
func (pgf *File) PosPosition(pos token.Pos) (protocol.Position, error) {
	return pgf.Mapper.PosPosition(pgf.Tok, pos)
}

// PosRange returns a protocol Range for the token.Pos interval in this file.
func (pgf *File) PosRange(start, end token.Pos) (protocol.Range, error) {
	return pgf.Mapper.PosRange(pgf.Tok, start, end)
}

// PosLocation returns a protocol Location for the token.Pos interval in this file.
func (pgf *File) PosLocation(start, end token.Pos) (protocol.Location, error) {
	return pgf.Mapper.PosLocation(pgf.Tok, start, end)
}

// NodeRange returns a protocol Range for the ast.Node interval in this file.
func (pgf *File) NodeRange(node ast.Node) (protocol.Range, error) {
	return pgf.Mapper.NodeRange(pgf.Tok, node)
}

// NodeOffsets returns offsets for the ast.Node.
func (pgf *File) NodeOffsets(node ast.Node) (start int, end int, _ error) {
	return safetoken.Offsets(pgf.Tok, node.Pos(), node.End())
}

// NodeLocation returns a protocol Location for the ast.Node interval in this file.
func (pgf *File) NodeLocation(node ast.Node) (protocol.Location, error) {
	return pgf.Mapper.PosLocation(pgf.Tok, node.Pos(), node.End())
}

// RangePos parses a protocol Range back into the go/token domain.
func (pgf *File) RangePos(r protocol.Range) (token.Pos, token.Pos, error) {
	start, end, err := pgf.Mapper.RangeOffsets(r)
	if err != nil {
		return token.NoPos, token.NoPos, err
	}
	return pgf.Tok.Pos(start), pgf.Tok.Pos(end), nil
}

// CheckNode asserts that the Node's positions are valid w.r.t. pgf.Tok.
func (pgf *File) CheckNode(node ast.Node) {
	// Avoid safetoken.Offsets, and put each assertion on its own source line.
	pgf.CheckPos(node.Pos())
	pgf.CheckPos(node.End())
}

// CheckPos asserts that the position is valid w.r.t. pgf.Tok.
func (pgf *File) CheckPos(pos token.Pos) {
	if !pos.IsValid() {
		bug.Report("invalid token.Pos")
	} else if _, err := safetoken.Offset(pgf.Tok, pos); err != nil {
		bug.Report("token.Pos out of range")
	}
}

// Resolve lazily resolves ast.Ident.Objects in the enclosed syntax tree.
//
// Resolve must be called before accessing any of:
//   - pgf.File.Scope
//   - pgf.File.Unresolved
//   - Ident.Obj, for any Ident in pgf.File
func (pgf *File) Resolve() {
	pgf.resolveOnce.Do(func() {
		if pgf.File.Scope != nil {
			return // already resolved by parsing without SkipObjectResolution.
		}
		defer func() {
			// (panic handler duplicated from go/parser)
			if e := recover(); e != nil {
				// A bailout indicates the resolution stack has exceeded max depth.
				if _, ok := e.(bailout); !ok {
					panic(e)
				}
			}
		}()
		declErr := func(token.Pos, string) {}
		resolveFile(pgf.File, pgf.Tok, declErr)
	})
}
