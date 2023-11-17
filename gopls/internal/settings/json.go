// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

type APIJSON struct {
	Options   map[string][]*OptionJSON
	Commands  []*CommandJSON
	Lenses    []*LensJSON
	Analyzers []*AnalyzerJSON
	Hints     []*HintJSON
}

type OptionJSON struct {
	Name       string
	Type       string
	Doc        string
	EnumKeys   EnumKeys
	EnumValues []EnumValue
	Default    string
	Status     string
	Hierarchy  string
}

func (o *OptionJSON) String() string {
	return o.Name
}

func (o *OptionJSON) Write(w io.Writer) {
	fmt.Fprintf(w, "**%v** *%v*\n\n", o.Name, o.Type)
	writeStatus(w, o.Status)
	enumValues := collectEnums(o)
	fmt.Fprintf(w, "%v%v\nDefault: `%v`.\n\n", o.Doc, enumValues, o.Default)
}

func writeStatus(section io.Writer, status string) {
	switch status {
	case "":
	case "advanced":
		fmt.Fprint(section, "**This is an advanced setting and should not be configured by most `gopls` users.**\n\n")
	case "debug":
		fmt.Fprint(section, "**This setting is for debugging purposes only.**\n\n")
	case "experimental":
		fmt.Fprint(section, "**This setting is experimental and may be deleted.**\n\n")
	default:
		fmt.Fprintf(section, "**Status: %s.**\n\n", status)
	}
}

var parBreakRE = regexp.MustCompile("\n{2,}")

func collectEnums(opt *OptionJSON) string {
	var b strings.Builder
	write := func(name, doc string) {
		if doc != "" {
			unbroken := parBreakRE.ReplaceAllString(doc, "\\\n")
			fmt.Fprintf(&b, "* %s\n", strings.TrimSpace(unbroken))
		} else {
			fmt.Fprintf(&b, "* `%s`\n", name)
		}
	}
	if len(opt.EnumValues) > 0 && opt.Type == "enum" {
		b.WriteString("\nMust be one of:\n\n")
		for _, val := range opt.EnumValues {
			write(val.Value, val.Doc)
		}
	} else if len(opt.EnumKeys.Keys) > 0 && shouldShowEnumKeysInSettings(opt.Name) {
		b.WriteString("\nCan contain any of:\n\n")
		for _, val := range opt.EnumKeys.Keys {
			write(val.Name, val.Doc)
		}
	}
	return b.String()
}

func shouldShowEnumKeysInSettings(name string) bool {
	// These fields have too many possible options to print.
	return !(name == "analyses" || name == "codelenses" || name == "hints")
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

type CommandJSON struct {
	Command   string
	Title     string
	Doc       string
	ArgDoc    string
	ResultDoc string
}

func (c *CommandJSON) String() string {
	return c.Command
}

func (c *CommandJSON) Write(w io.Writer) {
	fmt.Fprintf(w, "### **%v**\nIdentifier: `%v`\n\n%v\n\n", c.Title, c.Command, c.Doc)
	if c.ArgDoc != "" {
		fmt.Fprintf(w, "Args:\n\n```\n%s\n```\n\n", c.ArgDoc)
	}
	if c.ResultDoc != "" {
		fmt.Fprintf(w, "Result:\n\n```\n%s\n```\n\n", c.ResultDoc)
	}
}

type LensJSON struct {
	Lens  string
	Title string
	Doc   string
}

func (l *LensJSON) String() string {
	return l.Title
}

func (l *LensJSON) Write(w io.Writer) {
	fmt.Fprintf(w, "%s (%s): %s", l.Title, l.Lens, l.Doc)
}

type AnalyzerJSON struct {
	Name    string
	Doc     string
	URL     string
	Default bool
}

func (a *AnalyzerJSON) String() string {
	return a.Name
}

func (a *AnalyzerJSON) Write(w io.Writer) {
	fmt.Fprintf(w, "%s (%s): %v", a.Name, a.Doc, a.Default)
}

type HintJSON struct {
	Name    string
	Doc     string
	Default bool
}

func (h *HintJSON) String() string {
	return h.Name
}

func (h *HintJSON) Write(w io.Writer) {
	fmt.Fprintf(w, "%s (%s): %v", h.Name, h.Doc, h.Default)
}
