// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package parsego

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

// A File contains the results of parsing a Go file.
type File struct {
	URI  protocol.DocumentURI
	Mode parser.Mode
	File *ast.File
	Tok  *token.File
	// Source code used to build the AST. It may be different from the
	// actual content of the file if we have fixed the AST.
	Src []byte

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
}

func (pgf File) String() string { return string(pgf.URI) }

// Fixed reports whether p was "Fixed", meaning that its source or positions
// may not correlate with the original file.
func (pgf File) Fixed() bool {
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

// PosRange returns a protocol Range for the token.Pos interval in this file.
func (pgf *File) PosRange(start, end token.Pos) (protocol.Range, error) {
	return pgf.Mapper.PosRange(pgf.Tok, start, end)
}

// PosMappedRange returns a MappedRange for the token.Pos interval in this file.
// A MappedRange can be converted to any other form.
func (pgf *File) PosMappedRange(start, end token.Pos) (protocol.MappedRange, error) {
	return pgf.Mapper.PosMappedRange(pgf.Tok, start, end)
}

// PosLocation returns a protocol Location for the token.Pos interval in this file.
func (pgf *File) PosLocation(start, end token.Pos) (protocol.Location, error) {
	return pgf.Mapper.PosLocation(pgf.Tok, start, end)
}

// NodeRange returns a protocol Range for the ast.Node interval in this file.
func (pgf *File) NodeRange(node ast.Node) (protocol.Range, error) {
	return pgf.Mapper.NodeRange(pgf.Tok, node)
}

// NodeMappedRange returns a MappedRange for the ast.Node interval in this file.
// A MappedRange can be converted to any other form.
func (pgf *File) NodeMappedRange(node ast.Node) (protocol.MappedRange, error) {
	return pgf.Mapper.NodeMappedRange(pgf.Tok, node)
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
