// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import "fmt"

// Kind describes the kind of the file in question.
// It can be one of Go,mod, Sum, or Tmpl.
type Kind int

const (
	// UnknownKind is a file type we don't know about.
	UnknownKind = Kind(iota)

	// Go is a Go source file.
	Go
	// Mod is a go.mod file.
	Mod
	// Sum is a go.sum file.
	Sum
	// Tmpl is a template file.
	Tmpl
	// Work is a go.work file.
	Work
)

func (k Kind) String() string {
	switch k {
	case Go:
		return "go"
	case Mod:
		return "go.mod"
	case Sum:
		return "go.sum"
	case Tmpl:
		return "tmpl"
	case Work:
		return "go.work"
	default:
		return fmt.Sprintf("internal error: unknown file kind %d", k)
	}
}

// KindForLang returns the file kind associated with the given language ID
// (from protocol.TextDocumentItem.LanguageID), or UnknownKind if the language
// ID is not recognized.
func KindForLang(langID string) Kind {
	switch langID {
	case "go":
		return Go
	case "go.mod":
		return Mod
	case "go.sum":
		return Sum
	case "tmpl", "gotmpl":
		return Tmpl
	case "go.work":
		return Work
	default:
		return UnknownKind
	}
}
