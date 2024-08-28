// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

// The file defines helpers for semantics tokens.

import "fmt"

// SemanticTypes to use in case there is no client, as in the command line, or tests.
func SemanticTypes() []string {
	return semanticTypes[:]
}

// SemanticModifiers to use in case there is no client.
func SemanticModifiers() []string {
	return semanticModifiers[:]
}

// SemType returns a string equivalent of the type, for gopls semtok
func SemType(n int) string {
	tokTypes := SemanticTypes()
	tokMods := SemanticModifiers()
	if n >= 0 && n < len(tokTypes) {
		return tokTypes[n]
	}
	// not found for some reason
	return fmt.Sprintf("?%d[%d,%d]?", n, len(tokTypes), len(tokMods))
}

// SemMods returns the []string equivalent of the mods, for gopls semtok.
func SemMods(n int) []string {
	tokMods := SemanticModifiers()
	mods := []string{}
	for i := 0; i < len(tokMods); i++ {
		if (n & (1 << uint(i))) != 0 {
			mods = append(mods, tokMods[i])
		}
	}
	return mods
}

// From https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#textDocument_semanticTokens
var (
	semanticTypes = [...]string{
		"namespace", "type", "class", "enum", "interface",
		"struct", "typeParameter", "parameter", "variable", "property", "enumMember",
		"event", "function", "method", "macro", "keyword", "modifier", "comment",
		"string", "number", "regexp", "operator",
	}
	semanticModifiers = [...]string{
		"declaration", "definition", "readonly", "static",
		"deprecated", "abstract", "async", "modification", "documentation", "defaultLibrary",
		// Additional modifiers
		"interface", "struct", "signature", "pointer", "array", "map", "slice", "chan", "string", "number", "bool", "invalid",
	}
)
