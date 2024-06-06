// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run ../../doc/generate

// The doc package provides JSON metadata that documents gopls' public
// interfaces.
package doc

import _ "embed"

// API is a JSON value of type API.
// The 'gopls api-json' command prints it.
//
//go:embed api.json
var JSON string

// API is a JSON-encodable representation of gopls' public interfaces.
//
// TODO(adonovan): document these data types.
type API struct {
	Options   map[string][]*Option
	Commands  []*Command
	Lenses    []*Lens
	Analyzers []*Analyzer
	Hints     []*Hint
}

type Option struct {
	Name       string
	Type       string
	Doc        string
	EnumKeys   EnumKeys
	EnumValues []EnumValue
	Default    string
	Status     string
	Hierarchy  string
}

type EnumKeys struct {
	ValueType string
	Keys      []EnumKey
}

type EnumKey struct {
	Name    string
	Doc     string
	Default string
}

type EnumValue struct {
	Value string
	Doc   string
}

type Command struct {
	Command   string // e.g. "gopls.run_tests"
	Title     string
	Doc       string
	ArgDoc    string
	ResultDoc string
}

type Lens struct {
	FileType string // e.g. "Go", "go.mod"
	Lens     string
	Title    string
	Doc      string
	Default  bool
}

type Analyzer struct {
	Name    string
	Doc     string // from analysis.Analyzer.Doc ("title: summary\ndescription"; not Markdown)
	URL     string
	Default bool
}

type Hint struct {
	Name    string
	Doc     string
	Default bool
}
