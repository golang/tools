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
	Type        Type
	Modifiers   []Modifier
}

type Type string

const (
	// These are the tokens defined by LSP 3.18, but a client is
	// free to send its own set; any tokens that the server emits
	// that are not in this set are simply not encoded in the bitfield.
	TokComment   Type = "comment"       // for a comment
	TokFunction  Type = "function"      // for a function
	TokKeyword   Type = "keyword"       // for a keyword
	TokLabel     Type = "label"         // for a control label (LSP 3.18)
	TokMacro     Type = "macro"         // for text/template tokens
	TokMethod    Type = "method"        // for a method
	TokNamespace Type = "namespace"     // for an imported package name
	TokNumber    Type = "number"        // for a numeric literal
	TokOperator  Type = "operator"      // for an operator
	TokParameter Type = "parameter"     // for a parameter variable
	TokString    Type = "string"        // for a string literal
	TokType      Type = "type"          // for a type name (plus other uses)
	TokTypeParam Type = "typeParameter" // for a type parameter
	TokVariable  Type = "variable"      // for a var or const
	// The section below defines a subset of token types in standard token types
	// that gopls does not use.
	//
	// If you move types to above, document it in
	// gopls/doc/features/passive.md#semantic-tokens.
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

// TokenTypes is a slice of types gopls will return as its server capabilities.
var TokenTypes = []Type{
	TokNamespace,
	TokType,
	TokTypeParam,
	TokParameter,
	TokVariable,
	TokFunction,
	TokMethod,
	TokMacro,
	TokKeyword,
	TokComment,
	TokString,
	TokNumber,
	TokOperator,
	TokLabel,
}

type Modifier string

const (
	// LSP 3.18 standard modifiers
	// As with TokenTypes, clients get only the modifiers they request.
	//
	// The section below defines a subset of modifiers in standard modifiers
	// that gopls understand.
	ModDefaultLibrary Modifier = "defaultLibrary" // for predeclared symbols
	ModDefinition     Modifier = "definition"     // for the declaring identifier of a symbol
	ModReadonly       Modifier = "readonly"       // for constants (TokVariable)
	// The section below defines the rest of the modifiers in standard modifiers
	// that gopls does not use.
	//
	// If you move modifiers to above, document it in
	// gopls/doc/features/passive.md#semantic-tokens.
	// ModAbstract      Modifier = "abstract"
	// ModAsync         Modifier = "async"
	// ModDeclaration   Modifier = "declaration"
	// ModDeprecated    Modifier = "deprecated"
	// ModDocumentation Modifier = "documentation"
	// ModModification  Modifier = "modification"
	// ModStatic        Modifier = "static"

	// non-standard modifiers
	//
	// Since the type of a symbol is orthogonal to its kind,
	// (e.g. a variable can have function type),
	// we use modifiers for the top-level type constructor.
	ModArray     Modifier = "array"
	ModBool      Modifier = "bool"
	ModChan      Modifier = "chan"
	ModFormat    Modifier = "format" // for format string directives such as "%s"
	ModInterface Modifier = "interface"
	ModMap       Modifier = "map"
	ModNumber    Modifier = "number"
	ModPointer   Modifier = "pointer"
	ModSignature Modifier = "signature" // for function types
	ModSlice     Modifier = "slice"
	ModString    Modifier = "string"
	ModStruct    Modifier = "struct"
)

// TokenModifiers is a slice of modifiers gopls will return as its server
// capabilities.
var TokenModifiers = []Modifier{
	// LSP 3.18 standard modifiers.
	ModDefinition,
	ModReadonly,
	ModDefaultLibrary,
	// Additional custom modifiers.
	ModArray,
	ModBool,
	ModChan,
	ModFormat,
	ModInterface,
	ModMap,
	ModNumber,
	ModPointer,
	ModSignature,
	ModSlice,
	ModString,
	ModStruct,
}

// Encode returns the LSP encoding of a sequence of tokens.
// encodeType and encodeModifier maps control which types and modifiers are
// excluded in the response. If a type or modifier maps to false, it will be
// omitted from the output.
func Encode(
	tokens []Token,
	encodeType map[Type]bool,
	encodeModifier map[Modifier]bool) []uint32 {

	// binary operators, at least, will be out of order
	sort.Slice(tokens, func(i, j int) bool {
		if tokens[i].Line != tokens[j].Line {
			return tokens[i].Line < tokens[j].Line
		}
		return tokens[i].Start < tokens[j].Start
	})

	typeMap := make(map[Type]int)
	for i, t := range TokenTypes {
		if enable, ok := encodeType[t]; ok && !enable {
			continue
		}
		typeMap[Type(t)] = i
	}

	modMap := make(map[Modifier]int)
	for i, m := range TokenModifiers {
		if enable, ok := encodeModifier[m]; ok && !enable {
			continue
		}
		modMap[Modifier(m)] = 1 << i
	}

	// each semantic token needs five values but some tokens might be skipped.
	// (see Integer Encoding for Tokens in the LSP spec)
	x := make([]uint32, 5*len(tokens))
	var j int
	var last Token
	for i := 0; i < len(tokens); i++ {
		item := tokens[i]
		typ, ok := typeMap[item.Type]
		if !ok {
			continue // client doesn't want semantic token info.
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
