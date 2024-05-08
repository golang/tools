// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The generate command updates the following files of documentation:
//
//	gopls/doc/settings.md   -- from linking gopls/internal/settings.DefaultOptions
//	gopls/doc/commands.md   -- from loading gopls/internal/protocol/command
//	gopls/doc/analyzers.md  -- from linking gopls/internal/settings.DefaultAnalyzers
//	gopls/doc/inlayHints.md -- from linking gopls/internal/golang.AllInlayHints
//	gopls/internal/doc/api.json -- all of the above in a single value, for 'gopls api-json'
//
// Run it with this command:
//
//	$ cd gopls/doc && go generate
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/doc"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/mod"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/protocol/command/commandmeta"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/safetoken"
)

func main() {
	if _, err := doMain(true); err != nil {
		fmt.Fprintf(os.Stderr, "Generation failed: %v\n", err)
		os.Exit(1)
	}
}

// doMain regenerates the output files. On success:
// - if write, it updates them;
// - if !write, it reports whether they would change.
func doMain(write bool) (bool, error) {
	// TODO(adonovan): when we can rely on go1.23,
	// switch to gotypesalias=1 behavior.
	//
	// (Since this program is run by 'go run',
	// the gopls/go.mod file's go 1.19 directive doesn't
	// have its usual effect of setting gotypesalias=0.)
	os.Setenv("GODEBUG", "gotypesalias=0")

	api, err := loadAPI()
	if err != nil {
		return false, err
	}

	goplsDir, err := pkgDir("golang.org/x/tools/gopls")
	if err != nil {
		return false, err
	}

	for _, f := range []struct {
		name    string // relative to gopls
		rewrite rewriter
	}{
		{"internal/doc/api.json", rewriteAPI},
		{"doc/settings.md", rewriteSettings},
		{"doc/commands.md", rewriteCommands},
		{"doc/analyzers.md", rewriteAnalyzers},
		{"doc/inlayHints.md", rewriteInlayHints},
	} {
		file := filepath.Join(goplsDir, f.name)
		old, err := os.ReadFile(file)
		if err != nil {
			return false, err
		}

		new, err := f.rewrite(old, api)
		if err != nil {
			return false, fmt.Errorf("rewriting %q: %v", file, err)
		}

		if write {
			if err := os.WriteFile(file, new, 0); err != nil {
				return false, err
			}
		} else if !bytes.Equal(old, new) {
			return false, nil // files would change
		}
	}
	return true, nil
}

// A rewriter is a function that transforms the content of a file.
type rewriter = func([]byte, *doc.API) ([]byte, error)

// pkgDir returns the directory corresponding to the import path pkgPath.
func pkgDir(pkgPath string) (string, error) {
	cmd := exec.Command("go", "list", "-f", "{{.Dir}}", pkgPath)
	out, err := cmd.Output()
	if err != nil {
		if ee, _ := err.(*exec.ExitError); ee != nil && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%v: %w\n%s", cmd, err, ee.Stderr)
		}
		return "", fmt.Errorf("%v: %w", cmd, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// loadAPI computes the JSON-encodable value that describes gopls'
// interfaces, by a combination of static and dynamic analysis.
func loadAPI() (*doc.API, error) {
	pkgs, err := packages.Load(
		&packages.Config{
			Mode: packages.NeedTypes | packages.NeedTypesInfo | packages.NeedSyntax | packages.NeedDeps,
		},
		"golang.org/x/tools/gopls/internal/settings",
	)
	if err != nil {
		return nil, err
	}
	pkg := pkgs[0]

	defaults := settings.DefaultOptions()
	api := &doc.API{
		Options:   map[string][]*doc.Option{},
		Analyzers: loadAnalyzers(settings.DefaultAnalyzers), // no staticcheck analyzers
	}

	api.Commands, err = loadCommands()
	if err != nil {
		return nil, err
	}
	api.Lenses = loadLenses(api.Commands)

	// Transform the internal command name to the external command name.
	for _, c := range api.Commands {
		c.Command = command.ID(c.Command)
	}
	api.Hints = loadHints(golang.AllInlayHints)
	for _, category := range []reflect.Value{
		reflect.ValueOf(defaults.UserOptions),
	} {
		// Find the type information and ast.File corresponding to the category.
		optsType := pkg.Types.Scope().Lookup(category.Type().Name())
		if optsType == nil {
			return nil, fmt.Errorf("could not find %v in scope %v", category.Type().Name(), pkg.Types.Scope())
		}
		opts, err := loadOptions(category, optsType, pkg, "")
		if err != nil {
			return nil, err
		}
		catName := strings.TrimSuffix(category.Type().Name(), "Options")
		api.Options[catName] = opts

		// Hardcode the expected values for the analyses and code lenses
		// settings, since their keys are not enums.
		for _, opt := range opts {
			switch opt.Name {
			case "analyses":
				for _, a := range api.Analyzers {
					opt.EnumKeys.Keys = append(opt.EnumKeys.Keys, doc.EnumKey{
						Name:    fmt.Sprintf("%q", a.Name),
						Doc:     a.Doc,
						Default: strconv.FormatBool(a.Default),
					})
				}
			case "codelenses":
				// Hack: Lenses don't set default values, and we don't want to
				// pass in the list of expected lenses to loadOptions. Instead,
				// format the defaults using reflection here. The hackiest part
				// is reversing lowercasing of the field name.
				reflectField := category.FieldByName(upperFirst(opt.Name))
				for _, l := range api.Lenses {
					def, err := formatDefaultFromEnumBoolMap(reflectField, l.Lens)
					if err != nil {
						return nil, err
					}
					opt.EnumKeys.Keys = append(opt.EnumKeys.Keys, doc.EnumKey{
						Name:    fmt.Sprintf("%q", l.Lens),
						Doc:     l.Doc,
						Default: def,
					})
				}
			case "hints":
				for _, a := range api.Hints {
					opt.EnumKeys.Keys = append(opt.EnumKeys.Keys, doc.EnumKey{
						Name:    fmt.Sprintf("%q", a.Name),
						Doc:     a.Doc,
						Default: strconv.FormatBool(a.Default),
					})
				}
			}
		}
	}
	return api, nil
}

func loadOptions(category reflect.Value, optsType types.Object, pkg *packages.Package, hierarchy string) ([]*doc.Option, error) {
	file, err := fileForPos(pkg, optsType.Pos())
	if err != nil {
		return nil, err
	}

	enums, err := loadEnums(pkg)
	if err != nil {
		return nil, err
	}

	var opts []*doc.Option
	optsStruct := optsType.Type().Underlying().(*types.Struct)
	for i := 0; i < optsStruct.NumFields(); i++ {
		// The types field gives us the type.
		typesField := optsStruct.Field(i)

		// If the field name ends with "Options", assume it is a struct with
		// additional options and process it recursively.
		if h := strings.TrimSuffix(typesField.Name(), "Options"); h != typesField.Name() {
			// Keep track of the parent structs.
			if hierarchy != "" {
				h = hierarchy + "." + h
			}
			options, err := loadOptions(category, typesField, pkg, strings.ToLower(h))
			if err != nil {
				return nil, err
			}
			opts = append(opts, options...)
			continue
		}
		path, _ := astutil.PathEnclosingInterval(file, typesField.Pos(), typesField.Pos())
		if len(path) < 2 {
			return nil, fmt.Errorf("could not find AST node for field %v", typesField)
		}
		// The AST field gives us the doc.
		astField, ok := path[1].(*ast.Field)
		if !ok {
			return nil, fmt.Errorf("unexpected AST path %v", path)
		}

		// The reflect field gives us the default value.
		reflectField := category.FieldByName(typesField.Name())
		if !reflectField.IsValid() {
			return nil, fmt.Errorf("could not find reflect field for %v", typesField.Name())
		}

		def, err := formatDefault(reflectField)
		if err != nil {
			return nil, err
		}

		typ := typesField.Type().String()
		if _, ok := enums[typesField.Type()]; ok {
			typ = "enum"
		}
		name := lowerFirst(typesField.Name())

		var enumKeys doc.EnumKeys
		if m, ok := typesField.Type().Underlying().(*types.Map); ok {
			e, ok := enums[m.Key()]
			if ok {
				typ = strings.Replace(typ, m.Key().String(), m.Key().Underlying().String(), 1)
			}
			keys, err := collectEnumKeys(name, m, reflectField, e)
			if err != nil {
				return nil, err
			}
			if keys != nil {
				enumKeys = *keys
			}
		}

		// Get the status of the field by checking its struct tags.
		reflectStructField, ok := category.Type().FieldByName(typesField.Name())
		if !ok {
			return nil, fmt.Errorf("no struct field for %s", typesField.Name())
		}
		status := reflectStructField.Tag.Get("status")

		opts = append(opts, &doc.Option{
			Name:       name,
			Type:       typ,
			Doc:        lowerFirst(astField.Doc.Text()),
			Default:    def,
			EnumKeys:   enumKeys,
			EnumValues: enums[typesField.Type()],
			Status:     status,
			Hierarchy:  hierarchy,
		})
	}
	return opts, nil
}

func loadEnums(pkg *packages.Package) (map[types.Type][]doc.EnumValue, error) {
	enums := map[types.Type][]doc.EnumValue{}
	for _, name := range pkg.Types.Scope().Names() {
		obj := pkg.Types.Scope().Lookup(name)
		cnst, ok := obj.(*types.Const)
		if !ok {
			continue
		}
		f, err := fileForPos(pkg, cnst.Pos())
		if err != nil {
			return nil, fmt.Errorf("finding file for %q: %v", cnst.Name(), err)
		}
		path, _ := astutil.PathEnclosingInterval(f, cnst.Pos(), cnst.Pos())
		spec := path[1].(*ast.ValueSpec)
		value := cnst.Val().ExactString()
		docstring := valueDoc(cnst.Name(), value, spec.Doc.Text())
		v := doc.EnumValue{
			Value: value,
			Doc:   docstring,
		}
		enums[obj.Type()] = append(enums[obj.Type()], v)
	}
	return enums, nil
}

func collectEnumKeys(name string, m *types.Map, reflectField reflect.Value, enumValues []doc.EnumValue) (*doc.EnumKeys, error) {
	// Make sure the value type gets set for analyses and codelenses
	// too.
	if len(enumValues) == 0 && !hardcodedEnumKeys(name) {
		return nil, nil
	}
	keys := &doc.EnumKeys{
		ValueType: m.Elem().String(),
	}
	// We can get default values for enum -> bool maps.
	var isEnumBoolMap bool
	if basic, ok := m.Elem().Underlying().(*types.Basic); ok && basic.Kind() == types.Bool {
		isEnumBoolMap = true
	}
	for _, v := range enumValues {
		var def string
		if isEnumBoolMap {
			var err error
			def, err = formatDefaultFromEnumBoolMap(reflectField, v.Value)
			if err != nil {
				return nil, err
			}
		}
		keys.Keys = append(keys.Keys, doc.EnumKey{
			Name:    v.Value,
			Doc:     v.Doc,
			Default: def,
		})
	}
	return keys, nil
}

func formatDefaultFromEnumBoolMap(reflectMap reflect.Value, enumKey string) (string, error) {
	if reflectMap.Kind() != reflect.Map {
		return "", nil
	}
	name := enumKey
	if unquoted, err := strconv.Unquote(name); err == nil {
		name = unquoted
	}
	for _, e := range reflectMap.MapKeys() {
		if e.String() == name {
			value := reflectMap.MapIndex(e)
			if value.Type().Kind() == reflect.Bool {
				return formatDefault(value)
			}
		}
	}
	// Assume that if the value isn't mentioned in the map, it defaults to
	// the default value, false.
	return formatDefault(reflect.ValueOf(false))
}

// formatDefault formats the default value into a JSON-like string.
// VS Code exposes settings as JSON, so showing them as JSON is reasonable.
// TODO(rstambler): Reconsider this approach, as the VS Code Go generator now
// marshals to JSON.
func formatDefault(reflectField reflect.Value) (string, error) {
	def := reflectField.Interface()

	// Durations marshal as nanoseconds, but we want the stringy versions,
	// e.g. "100ms".
	if t, ok := def.(time.Duration); ok {
		def = t.String()
	}
	defBytes, err := json.Marshal(def)
	if err != nil {
		return "", err
	}

	// Nil values format as "null" so print them as hardcoded empty values.
	switch reflectField.Type().Kind() {
	case reflect.Map:
		if reflectField.IsNil() {
			defBytes = []byte("{}")
		}
	case reflect.Slice:
		if reflectField.IsNil() {
			defBytes = []byte("[]")
		}
	}
	return string(defBytes), err
}

// valueDoc transforms a docstring documenting an constant identifier to a
// docstring documenting its value.
//
// If doc is of the form "Foo is a bar", it returns '`"fooValue"` is a bar'. If
// doc is non-standard ("this value is a bar"), it returns '`"fooValue"`: this
// value is a bar'.
func valueDoc(name, value, doc string) string {
	if doc == "" {
		return ""
	}
	if strings.HasPrefix(doc, name) {
		// docstring in standard form. Replace the subject with value.
		return fmt.Sprintf("`%s`%s", value, doc[len(name):])
	}
	return fmt.Sprintf("`%s`: %s", value, doc)
}

func loadCommands() ([]*doc.Command, error) {
	var commands []*doc.Command

	_, cmds, err := commandmeta.Load()
	if err != nil {
		return nil, err
	}
	// Parse the objects it contains.
	for _, cmd := range cmds {
		cmdjson := &doc.Command{
			Command: cmd.Name,
			Title:   cmd.Title,
			Doc:     cmd.Doc,
			ArgDoc:  argsDoc(cmd.Args),
		}
		if cmd.Result != nil {
			cmdjson.ResultDoc = typeDoc(cmd.Result, 0)
		}
		commands = append(commands, cmdjson)
	}
	return commands, nil
}

func argsDoc(args []*commandmeta.Field) string {
	var b strings.Builder
	for i, arg := range args {
		b.WriteString(typeDoc(arg, 0))
		if i != len(args)-1 {
			b.WriteString(",\n")
		}
	}
	return b.String()
}

func typeDoc(arg *commandmeta.Field, level int) string {
	// Max level to expand struct fields.
	const maxLevel = 3
	if len(arg.Fields) > 0 {
		if level < maxLevel {
			return arg.FieldMod + structDoc(arg.Fields, level)
		}
		return "{ ... }"
	}
	under := arg.Type.Underlying()
	switch u := under.(type) {
	case *types.Slice:
		return fmt.Sprintf("[]%s", u.Elem().Underlying().String())
	}
	// TODO(adonovan): use (*types.Package).Name qualifier.
	return types.TypeString(under, nil)
}

func structDoc(fields []*commandmeta.Field, level int) string {
	var b strings.Builder
	b.WriteString("{\n")
	indent := strings.Repeat("\t", level)
	for _, fld := range fields {
		if fld.Doc != "" && level == 0 {
			doclines := strings.Split(fld.Doc, "\n")
			for _, line := range doclines {
				text := ""
				if line != "" {
					text = " " + line
				}
				fmt.Fprintf(&b, "%s\t//%s\n", indent, text)
			}
		}
		tag := strings.Split(fld.JSONTag, ",")[0]
		if tag == "" {
			tag = fld.Name
		}
		fmt.Fprintf(&b, "%s\t%q: %s,\n", indent, tag, typeDoc(fld, level+1))
	}
	fmt.Fprintf(&b, "%s}", indent)
	return b.String()
}

func loadLenses(commands []*doc.Command) []*doc.Lens {
	all := map[command.Command]struct{}{}
	for k := range golang.LensFuncs() {
		all[k] = struct{}{}
	}
	for k := range mod.LensFuncs() {
		if _, ok := all[k]; ok {
			panic(fmt.Sprintf("duplicate lens %q", string(k)))
		}
		all[k] = struct{}{}
	}

	var lenses []*doc.Lens

	for _, cmd := range commands {
		if _, ok := all[command.Command(cmd.Command)]; ok {
			lenses = append(lenses, &doc.Lens{
				Lens:  cmd.Command,
				Title: cmd.Title,
				Doc:   cmd.Doc,
			})
		}
	}
	return lenses
}

func loadAnalyzers(m map[string]*settings.Analyzer) []*doc.Analyzer {
	var sorted []string
	for _, a := range m {
		sorted = append(sorted, a.Analyzer().Name)
	}
	sort.Strings(sorted)
	var json []*doc.Analyzer
	for _, name := range sorted {
		a := m[name]
		json = append(json, &doc.Analyzer{
			Name:    a.Analyzer().Name,
			Doc:     a.Analyzer().Doc,
			URL:     a.Analyzer().URL,
			Default: a.EnabledByDefault(),
		})
	}
	return json
}

func loadHints(m map[string]*golang.Hint) []*doc.Hint {
	var sorted []string
	for _, h := range m {
		sorted = append(sorted, h.Name)
	}
	sort.Strings(sorted)
	var json []*doc.Hint
	for _, name := range sorted {
		h := m[name]
		json = append(json, &doc.Hint{
			Name: h.Name,
			Doc:  h.Doc,
		})
	}
	return json
}

func lowerFirst(x string) string {
	if x == "" {
		return x
	}
	return strings.ToLower(x[:1]) + x[1:]
}

func upperFirst(x string) string {
	if x == "" {
		return x
	}
	return strings.ToUpper(x[:1]) + x[1:]
}

func fileForPos(pkg *packages.Package, pos token.Pos) (*ast.File, error) {
	fset := pkg.Fset
	for _, f := range pkg.Syntax {
		if safetoken.StartPosition(fset, f.Pos()).Filename == safetoken.StartPosition(fset, pos).Filename {
			return f, nil
		}
	}
	return nil, fmt.Errorf("no file for pos %v", pos)
}

func rewriteAPI(_ []byte, api *doc.API) ([]byte, error) {
	return json.MarshalIndent(api, "", "\t")
}

type optionsGroup struct {
	title   string
	final   string
	level   int
	options []*doc.Option
}

func rewriteSettings(content []byte, api *doc.API) ([]byte, error) {
	result := content
	for category, opts := range api.Options {
		groups := collectGroups(opts)

		// First, print a table of contents.
		section := bytes.NewBuffer(nil)
		fmt.Fprintln(section, "")
		for _, h := range groups {
			writeBullet(section, h.final, h.level)
		}
		fmt.Fprintln(section, "")

		// Currently, the settings document has a title and a subtitle, so
		// start at level 3 for a header beginning with "###".
		baseLevel := 3
		for _, h := range groups {
			level := baseLevel + h.level
			writeTitle(section, h.final, level)
			for _, opt := range h.options {
				header := strMultiply("#", level+1)
				fmt.Fprintf(section, "%s ", header)
				writeOption(section, opt)
			}
		}
		var err error
		result, err = replaceSection(result, category, section.Bytes())
		if err != nil {
			return nil, err
		}
	}

	section := bytes.NewBuffer(nil)
	for _, lens := range api.Lenses {
		fmt.Fprintf(section, "### **%v**\n\nIdentifier: `%v`\n\n%v\n", lens.Title, lens.Lens, lens.Doc)
	}
	return replaceSection(result, "Lenses", section.Bytes())
}

func writeOption(w *bytes.Buffer, o *doc.Option) {
	fmt.Fprintf(w, "**%v** *%v*\n\n", o.Name, o.Type)
	writeStatus(w, o.Status)
	enumValues := collectEnums(o)
	fmt.Fprintf(w, "%v%v\nDefault: `%v`.\n\n", o.Doc, enumValues, o.Default)
}

func writeStatus(section *bytes.Buffer, status string) {
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

func collectEnums(opt *doc.Option) string {
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

func collectGroups(opts []*doc.Option) []optionsGroup {
	optsByHierarchy := map[string][]*doc.Option{}
	for _, opt := range opts {
		optsByHierarchy[opt.Hierarchy] = append(optsByHierarchy[opt.Hierarchy], opt)
	}

	// As a hack, assume that uncategorized items are less important to
	// users and force the empty string to the end of the list.
	var containsEmpty bool
	var sorted []string
	for h := range optsByHierarchy {
		if h == "" {
			containsEmpty = true
			continue
		}
		sorted = append(sorted, h)
	}
	sort.Strings(sorted)
	if containsEmpty {
		sorted = append(sorted, "")
	}
	var groups []optionsGroup
	baseLevel := 0
	for _, h := range sorted {
		split := strings.SplitAfter(h, ".")
		last := split[len(split)-1]
		// Hack to capitalize all of UI.
		if last == "ui" {
			last = "UI"
		}
		// A hierarchy may look like "ui.formatting". If "ui" has no
		// options of its own, it may not be added to the map, but it
		// still needs a heading.
		components := strings.Split(h, ".")
		for i := 1; i < len(components); i++ {
			parent := strings.Join(components[0:i], ".")
			if _, ok := optsByHierarchy[parent]; !ok {
				groups = append(groups, optionsGroup{
					title: parent,
					final: last,
					level: baseLevel + i,
				})
			}
		}
		groups = append(groups, optionsGroup{
			title:   h,
			final:   last,
			level:   baseLevel + strings.Count(h, "."),
			options: optsByHierarchy[h],
		})
	}
	return groups
}

func hardcodedEnumKeys(name string) bool {
	return name == "analyses" || name == "codelenses"
}

func writeBullet(w io.Writer, title string, level int) {
	if title == "" {
		return
	}
	// Capitalize the first letter of each title.
	prefix := strMultiply("  ", level)
	fmt.Fprintf(w, "%s* [%s](#%s)\n", prefix, capitalize(title), strings.ToLower(title))
}

func writeTitle(w io.Writer, title string, level int) {
	if title == "" {
		return
	}
	// Capitalize the first letter of each title.
	fmt.Fprintf(w, "%s %s\n\n", strMultiply("#", level), capitalize(title))
}

func capitalize(s string) string {
	return string(unicode.ToUpper(rune(s[0]))) + s[1:]
}

func strMultiply(str string, count int) string {
	var result string
	for i := 0; i < count; i++ {
		result += str
	}
	return result
}

func rewriteCommands(content []byte, api *doc.API) ([]byte, error) {
	section := bytes.NewBuffer(nil)
	for _, command := range api.Commands {
		writeCommand(section, command)
	}
	return replaceSection(content, "Commands", section.Bytes())
}

func writeCommand(w *bytes.Buffer, c *doc.Command) {
	fmt.Fprintf(w, "### **%v**\nIdentifier: `%v`\n\n%v\n\n", c.Title, c.Command, c.Doc)
	if c.ArgDoc != "" {
		fmt.Fprintf(w, "Args:\n\n```\n%s\n```\n\n", c.ArgDoc)
	}
	if c.ResultDoc != "" {
		fmt.Fprintf(w, "Result:\n\n```\n%s\n```\n\n", c.ResultDoc)
	}
}

func rewriteAnalyzers(content []byte, api *doc.API) ([]byte, error) {
	section := bytes.NewBuffer(nil)
	for _, analyzer := range api.Analyzers {
		fmt.Fprintf(section, "## **%v**\n\n", analyzer.Name)
		fmt.Fprintf(section, "%s: %s\n\n", analyzer.Name, analyzer.Doc)
		if analyzer.URL != "" {
			fmt.Fprintf(section, "[Full documentation](%s)\n\n", analyzer.URL)
		}
		switch analyzer.Default {
		case true:
			fmt.Fprintf(section, "**Enabled by default.**\n\n")
		case false:
			fmt.Fprintf(section, "**Disabled by default. Enable it by setting `\"analyses\": {\"%s\": true}`.**\n\n", analyzer.Name)
		}
	}
	return replaceSection(content, "Analyzers", section.Bytes())
}

func rewriteInlayHints(content []byte, api *doc.API) ([]byte, error) {
	section := bytes.NewBuffer(nil)
	for _, hint := range api.Hints {
		fmt.Fprintf(section, "## **%v**\n\n", hint.Name)
		fmt.Fprintf(section, "%s\n\n", hint.Doc)
		switch hint.Default {
		case true:
			fmt.Fprintf(section, "**Enabled by default.**\n\n")
		case false:
			fmt.Fprintf(section, "**Disabled by default. Enable it by setting `\"hints\": {\"%s\": true}`.**\n\n", hint.Name)
		}
	}
	return replaceSection(content, "Hints", section.Bytes())
}

func replaceSection(content []byte, sectionName string, replacement []byte) ([]byte, error) {
	re := regexp.MustCompile(fmt.Sprintf(`(?s)<!-- BEGIN %v.* -->\n(.*?)<!-- END %v.* -->`, sectionName, sectionName))
	idx := re.FindSubmatchIndex(content)
	if idx == nil {
		return nil, fmt.Errorf("could not find section %q", sectionName)
	}
	result := append([]byte(nil), content[:idx[2]]...)
	result = append(result, replacement...)
	result = append(result, content[idx[3]:]...)
	return result, nil
}
