// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package completion

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"log"
	"reflect"
	"strings"
	"sync"
	"text/template"

	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/golang/completion/snippet"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/imports"
	"golang.org/x/tools/internal/typesinternal"
)

// Postfix snippets are artificial methods that allow the user to
// compose common operations in an "argument oriented" fashion. For
// example, instead of "sort.Slice(someSlice, ...)" a user can expand
// "someSlice.sort!".

// postfixTmpl represents a postfix snippet completion candidate.
type postfixTmpl struct {
	// label is the completion candidate's label presented to the user.
	label string

	// details is passed along to the client as the candidate's details.
	details string

	// body is the template text. See postfixTmplArgs for details on the
	// facilities available to the template.
	body string

	tmpl *template.Template
}

// postfixTmplArgs are the template execution arguments available to
// the postfix snippet templates.
type postfixTmplArgs struct {
	// StmtOK is true if it is valid to replace the selector with a
	// statement. For example:
	//
	//    func foo() {
	//      bar.sort! // statement okay
	//
	//      someMethod(bar.sort!) // statement not okay
	//    }
	StmtOK bool

	// X is the textual SelectorExpr.X. For example, when completing
	// "foo.bar.print!", "X" is "foo.bar".
	X string

	// Obj is the types.Object of SelectorExpr.X, if any.
	Obj types.Object

	// Type is the type of "foo.bar" in "foo.bar.print!".
	Type types.Type

	// FuncResult are results of the enclosed function
	FuncResults []*types.Var

	sel            *ast.SelectorExpr
	scope          *types.Scope
	snip           snippet.Builder
	importIfNeeded func(pkgPath string, scope *types.Scope) (name string, edits []protocol.TextEdit, err error)
	edits          []protocol.TextEdit
	qf             types.Qualifier
	varNames       map[string]bool
	placeholders   bool
	currentTabStop int
}

var postfixTmpls = []postfixTmpl{{
	label:   "sort",
	details: "sort.Slice()",
	body: `{{if and (eq .Kind "slice") .StmtOK -}}
{{.Import "sort"}}.Slice({{.X}}, func({{.VarName nil "i"}}, {{.VarName nil "j"}} int) bool {
	{{.Cursor}}
})
{{- end}}`,
}, {
	label:   "last",
	details: "s[len(s)-1]",
	body: `{{if and (eq .Kind "slice") .Obj -}}
{{.X}}[len({{.X}})-1]
{{- end}}`,
}, {
	label:   "reverse",
	details: "reverse slice",
	body: `{{if and (eq .Kind "slice") .StmtOK -}}
{{.Import "slices"}}.Reverse({{.X}})
{{- end}}`,
}, {
	label:   "range",
	details: "range over slice",
	body: `{{if and (eq .Kind "slice") .StmtOK -}}
for {{.VarName nil "i" | .Placeholder }}, {{.VarName .ElemType "v" | .Placeholder}} := range {{.X}} {
	{{.Cursor}}
}
{{- end}}`,
}, {
	label:   "for",
	details: "range over slice by index",
	body: `{{if and (eq .Kind "slice") .StmtOK -}}
for {{ .VarName nil "i" | .Placeholder }} := range {{.X}} {
	{{.Cursor}}
}
{{- end}}`,
}, {
	label:   "forr",
	details: "range over slice by index and value",
	body: `{{if and (eq .Kind "slice") .StmtOK -}}
for {{.VarName nil "i" | .Placeholder }}, {{.VarName .ElemType "v" | .Placeholder }} := range {{.X}} {
	{{.Cursor}}
}
{{- end}}`,
}, {
	label:   "append",
	details: "append and re-assign slice",
	body: `{{if and (eq .Kind "slice") .StmtOK .Obj -}}
{{.X}} = append({{.X}}, {{.Cursor}})
{{- end}}`,
}, {
	label:   "append",
	details: "append to slice",
	body: `{{if and (eq .Kind "slice") (not .StmtOK) -}}
append({{.X}}, {{.Cursor}})
{{- end}}`,
}, {
	label:   "copy",
	details: "duplicate slice",
	body: `{{if and (eq .Kind "slice") .StmtOK .Obj -}}
{{$v := (.VarName nil (printf "%sCopy" .X))}}{{$v}} := make([]{{.TypeName .ElemType}}, len({{.X}}))
copy({{$v}}, {{.X}})
{{end}}`,
}, {
	label:   "range",
	details: "range over map",
	body: `{{if and (eq .Kind "map") .StmtOK -}}
for {{.VarName .KeyType "k" | .Placeholder}}, {{.VarName .ElemType "v" | .Placeholder}} := range {{.X}} {
	{{.Cursor}}
}
{{- end}}`,
}, {
	label:   "for",
	details: "range over map by key",
	body: `{{if and (eq .Kind "map") .StmtOK -}}
for {{.VarName .KeyType "k" | .Placeholder}} := range {{.X}} {
	{{.Cursor}}
}
{{- end}}`,
}, {
	label:   "forr",
	details: "range over map by key and value",
	body: `{{if and (eq .Kind "map") .StmtOK -}}
for {{.VarName .KeyType "k" | .Placeholder}}, {{.VarName .ElemType "v" | .Placeholder}} := range {{.X}} {
	{{.Cursor}}
}
{{- end}}`,
}, {
	label:   "clear",
	details: "clear map contents",
	body: `{{if and (eq .Kind "map") .StmtOK -}}
{{$k := (.VarName .KeyType "k")}}for {{$k}} := range {{.X}} {
	delete({{.X}}, {{$k}})
}
{{end}}`,
}, {
	label:   "keys",
	details: "create slice of keys",
	body: `{{if and (eq .Kind "map") .StmtOK -}}
{{$keysVar := (.VarName nil "keys")}}{{$keysVar}} := make([]{{.TypeName .KeyType}}, 0, len({{.X}}))
{{$k := (.VarName .KeyType "k")}}for {{$k}} := range {{.X}} {
	{{$keysVar}} = append({{$keysVar}}, {{$k}})
}
{{end}}`,
}, {
	label:   "range",
	details: "range over channel",
	body: `{{if and (eq .Kind "chan") .StmtOK -}}
for {{.VarName .ElemType "e" | .Placeholder}} := range {{.X}} {
	{{.Cursor}}
}
{{- end}}`,
}, {
	label:   "for",
	details: "range over channel",
	body: `{{if and (eq .Kind "chan") .StmtOK -}}
for {{.VarName .ElemType "e" | .Placeholder}} := range {{.X}} {
	{{.Cursor}}
}
{{- end}}`,
}, {
	label:   "var",
	details: "assign to variables",
	body: `{{if and (eq .Kind "tuple") .StmtOK -}}
{{$a := .}}{{range $i, $v := .Tuple}}{{if $i}}, {{end}}{{$a.VarName $v.Type $v.Name | $a.Placeholder }}{{end}} := {{.X}}
{{- end}}`,
}, {
	label:   "var",
	details: "assign to variable",
	body: `{{if and (ne .Kind "tuple") .StmtOK -}}
{{.VarName .Type "" | .Placeholder }} := {{.X}}
{{- end}}`,
}, {
	label:   "print",
	details: "print to stdout",
	body: `{{if and (ne .Kind "tuple") .StmtOK -}}
{{.Import "fmt"}}.Printf("{{.EscapeQuotes .X}}: %v\n", {{.X}})
{{- end}}`,
}, {
	label:   "print",
	details: "print to stdout",
	body: `{{if and (eq .Kind "tuple") .StmtOK -}}
{{.Import "fmt"}}.Println({{.X}})
{{- end}}`,
}, {
	label:   "split",
	details: "split string",
	body: `{{if (eq (.TypeName .Type) "string") -}}
{{.Import "strings"}}.Split({{.X}}, "{{.Cursor}}")
{{- end}}`,
}, {
	label:   "join",
	details: "join string slice",
	body: `{{if and (eq .Kind "slice") (eq (.TypeName .ElemType) "string") -}}
{{.Import "strings"}}.Join({{.X}}, "{{.Cursor}}")
{{- end}}`,
}, {
	label:   "ifnotnil",
	details: "if expr != nil",
	body: `{{if and (or (eq .Kind "pointer") (eq .Kind "chan") (eq .Kind "signature") (eq .Kind "interface") (eq .Kind "map") (eq .Kind "slice")) .StmtOK -}}
if {{.X}} != nil {
	{{.Cursor}}
}
{{- end}}`,
}, {
	label:   "len",
	details: "len(s)",
	body: `{{if (eq .Kind "slice" "map" "array" "chan") -}}
len({{.X}})
{{- end}}`,
}, {
	label:   "iferr",
	details: "check error and return",
	body: `{{if and .StmtOK (eq (.TypeName .Type) "error") -}}
{{- $errName := (or (and .IsIdent .X) "err") -}}
if {{if not .IsIdent}}err := {{.X}}; {{end}}{{$errName}} != nil {
	return {{$a := .}}{{range $i, $v := .FuncResults}}
		{{- if $i}}, {{end -}}
		{{- if eq ($a.TypeName $v.Type) "error" -}}
			{{$a.Placeholder $errName}}
		{{- else -}}
			{{$a.Zero $v.Type}}
		{{- end -}}
	{{end}}
}
{{end}}`,
}, {
	label:   "iferr",
	details: "check error and return",
	body: `{{if and .StmtOK (eq .Kind "tuple") (len .Tuple) (eq (.TypeName .TupleLast.Type) "error") -}}
{{- $a := . -}}
if {{range $i, $v := .Tuple}}{{if $i}}, {{end}}{{if and (eq ($a.TypeName $v.Type) "error") (eq (inc $i) (len $a.Tuple))}}err{{else}}_{{end}}{{end}} := {{.X -}}
; err != nil {
	return {{range $i, $v := .FuncResults}}
		{{- if $i}}, {{end -}}
		{{- if eq ($a.TypeName $v.Type) "error" -}}
			{{$a.Placeholder "err"}}
		{{- else -}}
			{{$a.Zero $v.Type}}
		{{- end -}}
	{{end}}
}
{{end}}`,
}, {
	// variferr snippets use nested placeholders, as described in
	// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#snippet_syntax,
	// so that users can wrap the returned error without modifying the error
	// variable name.
	label:   "variferr",
	details: "assign variables and check error",
	body: `{{if and .StmtOK (eq .Kind "tuple") (len .Tuple) (eq (.TypeName .TupleLast.Type) "error") -}}
{{- $a := . -}}
{{- $errName := "err" -}}
{{- range $i, $v := .Tuple -}}
	{{- if $i}}, {{end -}}
	{{- if and (eq ($a.TypeName $v.Type) "error") (eq (inc $i) (len $a.Tuple)) -}}
		{{$errName | $a.SpecifiedPlaceholder (len $a.Tuple)}}
	{{- else -}}
		{{$a.VarName $v.Type $v.Name | $a.Placeholder}}
	{{- end -}}
{{- end}} := {{.X}}
if {{$errName | $a.SpecifiedPlaceholder (len $a.Tuple)}} != nil {
	return {{range $i, $v := .FuncResults}}
		{{- if $i}}, {{end -}}
		{{- if eq ($a.TypeName $v.Type) "error" -}}
			{{$errName | $a.SpecifiedPlaceholder (len $a.Tuple) |
				$a.SpecifiedPlaceholder (inc (len $a.Tuple))}}
		{{- else -}}
			{{$a.Zero $v.Type}}
		{{- end -}}
	{{end}}
}
{{end}}`,
}, {
	label:   "variferr",
	details: "assign variables and check error",
	body: `{{if and .StmtOK (eq (.TypeName .Type) "error") -}}
{{- $a := . -}}
{{- $errName := .VarName nil "err" -}}
{{$errName | $a.SpecifiedPlaceholder 1}} := {{.X}}
if {{$errName | $a.SpecifiedPlaceholder 1}} != nil {
	return {{range $i, $v := .FuncResults}}
		{{- if $i}}, {{end -}}
		{{- if eq ($a.TypeName $v.Type) "error" -}}
			{{$errName | $a.SpecifiedPlaceholder 1 | $a.SpecifiedPlaceholder 2}}
		{{- else -}}
			{{$a.Zero $v.Type}}
		{{- end -}}
	{{end}}
}
{{end}}`,
}}

// Cursor indicates where the client's cursor should end up after the
// snippet is done.
func (a *postfixTmplArgs) Cursor() string {
	return "$0"
}

// Placeholder indicate a tab stop with the placeholder string, the order
// of tab stops is the same as the order of invocation
func (a *postfixTmplArgs) Placeholder(placeholder string) string {
	if !a.placeholders {
		placeholder = ""
	}
	return fmt.Sprintf("${%d:%s}", a.nextTabStop(), placeholder)
}

// nextTabStop returns the next tab stop index for a new placeholder.
func (a *postfixTmplArgs) nextTabStop() int {
	// Tab stops start from 1, so increment before returning.
	a.currentTabStop++
	return a.currentTabStop
}

// SpecifiedPlaceholder indicate a specified tab stop with the placeholder string.
// Sometimes the same tab stop appears in multiple places and their numbers
// need to be specified. e.g. variferr
func (a *postfixTmplArgs) SpecifiedPlaceholder(tabStop int, placeholder string) string {
	if !a.placeholders {
		placeholder = ""
	}
	return fmt.Sprintf("${%d:%s}", tabStop, placeholder)
}

// Import makes sure the package corresponding to path is imported,
// returning the identifier to use to refer to the package.
func (a *postfixTmplArgs) Import(path string) (string, error) {
	name, edits, err := a.importIfNeeded(path, a.scope)
	if err != nil {
		return "", fmt.Errorf("couldn't import %q: %w", path, err)
	}
	a.edits = append(a.edits, edits...)

	return name, nil
}

func (a *postfixTmplArgs) EscapeQuotes(v string) string {
	return strings.ReplaceAll(v, `"`, `\\"`)
}

// ElemType returns the Elem() type of xType, if applicable.
func (a *postfixTmplArgs) ElemType() types.Type {
	type hasElem interface{ Elem() types.Type } // Array, Chan, Map, Pointer, Slice
	if e, ok := a.Type.Underlying().(hasElem); ok {
		return e.Elem()
	}
	return nil
}

// Kind returns the underlying kind of type, e.g. "slice", "struct",
// etc.
func (a *postfixTmplArgs) Kind() string {
	t := reflect.TypeOf(a.Type.Underlying())
	return strings.ToLower(strings.TrimPrefix(t.String(), "*types."))
}

// KeyType returns the type of X's key. KeyType panics if X is not a
// map.
func (a *postfixTmplArgs) KeyType() types.Type {
	return a.Type.Underlying().(*types.Map).Key()
}

// Tuple returns the tuple result vars if the type of X is tuple.
func (a *postfixTmplArgs) Tuple() []*types.Var {
	tuple, _ := a.Type.(*types.Tuple)
	if tuple == nil {
		return nil
	}

	typs := make([]*types.Var, 0, tuple.Len())
	for i := 0; i < tuple.Len(); i++ {
		typs = append(typs, tuple.At(i))
	}
	return typs
}

// TupleLast returns the last tuple result vars if the type of X is tuple.
func (a *postfixTmplArgs) TupleLast() *types.Var {
	tuple, _ := a.Type.(*types.Tuple)
	if tuple == nil {
		return nil
	}
	if tuple.Len() == 0 {
		return nil
	}
	return tuple.At(tuple.Len() - 1)
}

// TypeName returns the textual representation of type t.
func (a *postfixTmplArgs) TypeName(t types.Type) (string, error) {
	if t == nil || t == types.Typ[types.Invalid] {
		return "", fmt.Errorf("invalid type: %v", t)
	}
	return types.TypeString(t, a.qf), nil
}

// Zero return the zero value representation of type t
func (a *postfixTmplArgs) Zero(t types.Type) string {
	return formatZeroValue(t, a.qf)
}

func (a *postfixTmplArgs) IsIdent() bool {
	_, ok := a.sel.X.(*ast.Ident)
	return ok
}

// VarName returns a suitable variable name for the type t. If t
// implements the error interface, "err" is used. If t is not a named
// type then nonNamedDefault is used. Otherwise a name is made by
// abbreviating the type name. If the resultant name is already in
// scope, an integer is appended to make a unique name.
func (a *postfixTmplArgs) VarName(t types.Type, nonNamedDefault string) string {
	if t == nil {
		t = types.Typ[types.Invalid]
	}

	var name string
	// go/types predicates are undefined on types.Typ[types.Invalid].
	if !types.Identical(t, types.Typ[types.Invalid]) && types.Implements(t, errorIntf) {
		name = "err"
	} else if !is[*types.Named](aliases.Unalias(typesinternal.Unpointer(t))) {
		name = nonNamedDefault
	}

	if name == "" {
		name = types.TypeString(t, func(p *types.Package) string {
			return ""
		})
		name = abbreviateTypeName(name)
	}

	if dot := strings.LastIndex(name, "."); dot > -1 {
		name = name[dot+1:]
	}

	uniqueName := name
	for i := 2; ; i++ {
		if s, _ := a.scope.LookupParent(uniqueName, token.NoPos); s == nil && !a.varNames[uniqueName] {
			break
		}
		uniqueName = fmt.Sprintf("%s%d", name, i)
	}

	a.varNames[uniqueName] = true

	return uniqueName
}

func (c *completer) addPostfixSnippetCandidates(ctx context.Context, sel *ast.SelectorExpr) {
	if !c.opts.postfix {
		return
	}

	initPostfixRules()

	if sel == nil || sel.Sel == nil {
		return
	}

	selType := c.pkg.TypesInfo().TypeOf(sel.X)
	if selType == nil {
		return
	}

	// Skip empty tuples since there is no value to operate on.
	if tuple, ok := selType.(*types.Tuple); ok && tuple == nil {
		return
	}

	tokFile := c.pkg.FileSet().File(c.pos)

	// Only replace sel with a statement if sel is already a statement.
	var stmtOK bool
	for i, n := range c.path {
		if n == sel && i < len(c.path)-1 {
			switch p := c.path[i+1].(type) {
			case *ast.ExprStmt:
				stmtOK = true
			case *ast.AssignStmt:
				// In cases like:
				//
				//   foo.<>
				//   bar = 123
				//
				// detect that "foo." makes up the entire statement since the
				// apparent selector spans lines.
				stmtOK = safetoken.Line(tokFile, c.pos) < safetoken.Line(tokFile, p.TokPos)
			}
			break
		}
	}

	var funcResults []*types.Var
	if c.enclosingFunc != nil {
		results := c.enclosingFunc.sig.Results()
		if results != nil {
			funcResults = make([]*types.Var, results.Len())
			for i := 0; i < results.Len(); i++ {
				funcResults[i] = results.At(i)
			}
		}
	}

	scope := c.pkg.Types().Scope().Innermost(c.pos)
	if scope == nil {
		return
	}

	// afterDot is the position after selector dot, e.g. "|" in
	// "foo.|print".
	afterDot := sel.Sel.Pos()

	// We must detect dangling selectors such as:
	//
	//    foo.<>
	//    bar
	//
	// and adjust afterDot so that we don't mistakenly delete the
	// newline thinking "bar" is part of our selector.
	if startLine := safetoken.Line(tokFile, sel.Pos()); startLine != safetoken.Line(tokFile, afterDot) {
		if safetoken.Line(tokFile, c.pos) != startLine {
			return
		}
		afterDot = c.pos
	}

	for _, rule := range postfixTmpls {
		// When completing foo.print<>, "print" is naturally overwritten,
		// but we need to also remove "foo." so the snippet has a clean
		// slate.
		edits, err := c.editText(sel.Pos(), afterDot, "")
		if err != nil {
			event.Error(ctx, "error calculating postfix edits", err)
			return
		}

		tmplArgs := postfixTmplArgs{
			X:              golang.FormatNode(c.pkg.FileSet(), sel.X),
			StmtOK:         stmtOK,
			Obj:            exprObj(c.pkg.TypesInfo(), sel.X),
			Type:           selType,
			FuncResults:    funcResults,
			sel:            sel,
			qf:             c.qf,
			importIfNeeded: c.importIfNeeded,
			scope:          scope,
			varNames:       make(map[string]bool),
			placeholders:   c.opts.placeholders,
		}

		// Feed the template straight into the snippet builder. This
		// allows templates to build snippets as they are executed.
		err = rule.tmpl.Execute(&tmplArgs.snip, &tmplArgs)
		if err != nil {
			event.Error(ctx, "error executing postfix template", err)
			continue
		}

		if strings.TrimSpace(tmplArgs.snip.String()) == "" {
			continue
		}

		score := c.matcher.Score(rule.label)
		if score <= 0 {
			continue
		}

		c.items = append(c.items, CompletionItem{
			Label:               rule.label + "!",
			Detail:              rule.details,
			Score:               float64(score) * 0.01,
			Kind:                protocol.SnippetCompletion,
			snippet:             &tmplArgs.snip,
			AdditionalTextEdits: append(edits, tmplArgs.edits...),
		})
	}
}

var postfixRulesOnce sync.Once

func initPostfixRules() {
	postfixRulesOnce.Do(func() {
		var idx int
		for _, rule := range postfixTmpls {
			var err error
			rule.tmpl, err = template.New("postfix_snippet").Funcs(template.FuncMap{
				"inc": inc,
			}).Parse(rule.body)
			if err != nil {
				log.Panicf("error parsing postfix snippet template: %v", err)
			}
			postfixTmpls[idx] = rule
			idx++
		}
		postfixTmpls = postfixTmpls[:idx]
	})
}

func inc(i int) int {
	return i + 1
}

// importIfNeeded returns the package identifier and any necessary
// edits to import package pkgPath.
func (c *completer) importIfNeeded(pkgPath string, scope *types.Scope) (string, []protocol.TextEdit, error) {
	defaultName := imports.ImportPathToAssumedName(pkgPath)

	// Check if file already imports pkgPath.
	for _, s := range c.file.Imports {
		// TODO(adonovan): what if pkgPath has a vendor/ suffix?
		// This may be the cause of go.dev/issue/56291.
		if string(metadata.UnquoteImportPath(s)) == pkgPath {
			if s.Name == nil {
				return defaultName, nil, nil
			}
			if s.Name.Name != "_" {
				return s.Name.Name, nil, nil
			}
		}
	}

	// Give up if the package's name is already in use by another object.
	if _, obj := scope.LookupParent(defaultName, token.NoPos); obj != nil {
		return "", nil, fmt.Errorf("import name %q of %q already in use", defaultName, pkgPath)
	}

	edits, err := c.importEdits(&importInfo{
		importPath: pkgPath,
	})
	if err != nil {
		return "", nil, err
	}

	return defaultName, edits, nil
}
