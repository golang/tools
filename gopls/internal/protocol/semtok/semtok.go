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
	Modifiers   []Modifier
}

type TokenType string

const (
	// These are the tokens defined by LSP 3.18, but a client is
	// free to send its own set; any tokens that the server emits
	// that are not in this set are simply not encoded in the bitfield.
	//
	// If you add or uncomment a token type, document it in
	// gopls/doc/features/passive.md#semantic-tokens.
	TokComment   TokenType = "comment"       // for a comment
	TokFunction  TokenType = "function"      // for a function
	TokKeyword   TokenType = "keyword"       // for a keyword
	TokLabel     TokenType = "label"         // for a control label (LSP 3.18)
	TokMacro     TokenType = "macro"         // for text/template tokens
	TokMethod    TokenType = "method"        // for a method
	TokNamespace TokenType = "namespace"     // for an imported package name
	TokNumber    TokenType = "number"        // for a numeric literal
	TokOperator  TokenType = "operator"      // for an operator
	TokParameter TokenType = "parameter"     // for a parameter variable
	TokString    TokenType = "string"        // for a string literal
	TokType      TokenType = "type"          // for a type name (plus other uses)
	TokTypeParam TokenType = "typeParameter" // for a type parameter
	TokVariable  TokenType = "variable"      // for a var or const
	// TokClass      TokenType = "class"
	// TokDecorator  TokenType = "decorator"
	// TokEnum       TokenType = "enum"
	// TokEnumMember TokenType = "enumMember"
	// TokEvent      TokenType = "event"
	// TokInterface  TokenType = "interface"
	// TokModifier   TokenType = "modifier"
	// TokProperty   TokenType = "property"
	// TokRegexp     TokenType = "regexp"
	// TokStruct     TokenType = "struct"
)

type Modifier string

const (
	// LSP 3.18 standard modifiers
	// As with TokenTypes, clients get only the modifiers they request.
	//
	// If you add or uncomment a modifier, document it in
	// gopls/doc/features/passive.md#semantic-tokens.
	ModDefaultLibrary Modifier = "defaultLibrary" // for predeclared symbols
	ModDefinition     Modifier = "definition"     // for the declaring identifier of a symbol
	ModReadonly       Modifier = "readonly"       // for constants (TokVariable)
	// ModAbstract       Modifier = "abstract"
	// ModAsync          Modifier = "async"
	// ModDeclaration    Modifier = "declaration"
	// ModDeprecated     Modifier = "deprecated"
	// ModDocumentation  Modifier = "documentation"
	// ModModification   Modifier = "modification"
	// ModStatic         Modifier = "static"

	// non-standard modifiers
	//
	// Since the type of a symbol is orthogonal to its kind,
	// (e.g. a variable can have function type),
	// we use modifiers for the top-level type constructor.
	ModArray     Modifier = "array"
	ModBool      Modifier = "bool"
	ModChan      Modifier = "chan"
	ModInterface Modifier = "interface"
	ModMap       Modifier = "map"
	ModNumber    Modifier = "number"
	ModPointer   Modifier = "pointer"
	ModSignature Modifier = "signature" // for function types
	ModSlice     Modifier = "slice"
	ModString    Modifier = "string"
	ModStruct    Modifier = "struct"
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

	modMap := make(map[Modifier]int)
	for i, m := range modifiers {
		modMap[Modifier(m)] = 1 << i
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
