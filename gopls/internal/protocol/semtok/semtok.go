// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The semtok package provides an encoder for LSP's semantic tokens.
package semtok

import "sort"

// A Token provides the extent and semantics of a token.
type Token struct {
	Line, Start uint32
	Len         uint32
	Type        TokenType
	Modifiers   []string
}

type TokenType string

const (
	TokNamespace TokenType = "namespace"
	TokType      TokenType = "type"
	TokInterface TokenType = "interface"
	TokTypeParam TokenType = "typeParameter"
	TokParameter TokenType = "parameter"
	TokVariable  TokenType = "variable"
	TokMethod    TokenType = "method"
	TokFunction  TokenType = "function"
	TokKeyword   TokenType = "keyword"
	TokComment   TokenType = "comment"
	TokString    TokenType = "string"
	TokNumber    TokenType = "number"
	TokOperator  TokenType = "operator"
	TokMacro     TokenType = "macro" // for templates
)

// Encode returns the LSP encoding of a sequence of tokens.
// The noStrings, noNumbers options cause strings, numbers to be skipped.
// The lists of types and modifiers determines the bitfield encoding.
func Encode(
	tokens []Token,
	noStrings, noNumbers bool,
	types, modifiers []string) []uint32 {

	// binary operators, at least, will be out of order
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].Line != tokens[j].Line {
			return tokens[i].Line < tokens[j].Line
		}
		return tokens[i].Start < tokens[j].Start
	})

	typeMap := make(map[TokenType]int)
	for i, t := range types {
		typeMap[TokenType(t)] = i
	}

	modMap := make(map[string]int)
	for i, m := range modifiers {
		modMap[m] = 1 << uint(i) // go 1.12 compatibility
	}

	// each semantic token needs five values
	// (see Integer Encoding for Tokens in the LSP spec)
	x := make([]uint32, 5*len(tokens))
	var j int
	var last Token
	for i := 0; i < len(tokens); i++ {
		item := tokens[i]
		typ, ok := typeMap[item.Type]
		if !ok {
			continue // client doesn't want typeStr
		}
		if item.Type == TokString && noStrings {
			continue
		}
		if item.Type == TokNumber && noNumbers {
			continue
		}
		if j == 0 {
			x[0] = tokens[0].Line
		} else {
			x[j] = item.Line - last.Line
		}
		x[j+1] = item.Start
		if j > 0 && x[j] == 0 {
			x[j+1] = item.Start - last.Start
		}
		x[j+2] = item.Len
		x[j+3] = uint32(typ)
		mask := 0
		for _, s := range item.Modifiers {
			// modMap[s] is 0 if the client doesn't want this modifier
			mask |= modMap[s]
		}
		x[j+4] = uint32(mask)
		j += 5
		last = item
	}
	return x[:j]
}
