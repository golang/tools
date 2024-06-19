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
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/doc"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/mod"
	"golang.org/x/tools/gopls/internal/protocol/command/commandmeta"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/maps"
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

	// TODO(adonovan): consider using HTML, not Markdown, for the
	// generated reference documents. It's not more difficult, the
	// layout is easier to read, and we can use go/doc-comment
	// rendering logic.

	for _, f := range []struct {
		name    string // relative to gopls
		rewrite rewriter
	}{
		{"internal/doc/api.json", rewriteAPI},
		{"doc/settings.md", rewriteSettings},
		{"doc/codelenses.md", rewriteCodeLenses},
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
	settingsPkg := pkgs[0]

	defaults := settings.DefaultOptions()
	api := &doc.API{
		Options:   map[string][]*doc.Option{},
		Analyzers: loadAnalyzers(settings.DefaultAnalyzers), // no staticcheck analyzers
	}

	api.Commands, err = loadCommands()
	if err != nil {
		return nil, err
	}
	api.Lenses, err = loadLenses(settingsPkg, defaults.Codelenses)
	if err != nil {
		return nil, err
	}

	api.Hints = loadHints(golang.AllInlayHints)
	for _, category := range []reflect.Value{
		reflect.ValueOf(defaults.UserOptions),
	} {
		// Find the type information and ast.File corresponding to the category.
		optsType := settingsPkg.Types.Scope().Lookup(category.Type().Name())
		if optsType == nil {
			return nil, fmt.Errorf("could not find %v in scope %v", category.Type().Name(), settingsPkg.Types.Scope())
		}
		opts, err := loadOptions(category, optsType, settingsPkg, "")
		if err != nil {
			return nil, err
		}
		catName := strings.TrimSuffix(category.Type().Name(), "Options")
		api.Options[catName] = opts

		// Hardcode the expected values for the "analyses" and "hints" settings,
		// since their map keys are strings, not enums.
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
			case "hints":
				// TODO(adonovan): simplify InlayHints to use an enum,
				// following CodeLensSource.
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

// loadOptions computes a single category of settings by a combination
// of static analysis and reflection over gopls internal types.
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

		// Derive the doc-and-api.json type from the Go field type.
		//
		// In principle, we should use JSON nomenclature here
		// (number, array, object, etc; see #68057), but in
		// practice we use the Go type string ([]T, map[K]V,
		// etc) with only one tweak: enumeration types are
		// replaced by "enum", including when they appear as
		// map keys.
		//
		// Notable edge cases:
		// - any (e.g. in linksInHover) is really a sum of false | true | "internal".
		// - time.Duration is really a string with a particular syntax.
		typ := typesField.Type().String()
		if _, ok := enums[typesField.Type()]; ok {
			typ = "enum"
		}
		name := lowerFirst(typesField.Name())

		// enum-keyed maps
		var enumKeys doc.EnumKeys
		if m, ok := typesField.Type().Underlying().(*types.Map); ok {
			values, ok := enums[m.Key()]
			if ok {
				// Update type name: "map[CodeLensSource]T" -> "map[enum]T"
				// hack: assumes key substring is unique!
				typ = strings.Replace(typ, m.Key().String(), "enum", 1)
			}

			// Edge case: "analyses" is a string (not enum) keyed map,
			// but its EnumKeys.ValueType was historically populated.
			// (But not "env"; not sure why.)
			if ok || name == "analyses" {
				enumKeys.ValueType = m.Elem().String()

				// For map[enum]T fields, gather the set of valid
				// EnumKeys (from type information). If T=bool, also
				// record the default value (from reflection).
				keys, err := collectEnumKeys(m, reflectField, values)
				if err != nil {
					return nil, err
				}
				enumKeys.Keys = keys
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

// loadEnums returns a description of gopls' settings enum types based on static analysis.
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

func collectEnumKeys(m *types.Map, reflectField reflect.Value, enumValues []doc.EnumValue) ([]doc.EnumKey, error) {
	// We can get default values for enum -> bool maps.
	var isEnumBoolMap bool
	if basic, ok := m.Elem().Underlying().(*types.Basic); ok && basic.Kind() == types.Bool {
		isEnumBoolMap = true
	}
	var keys []doc.EnumKey
	for _, v := range enumValues {
		var def string
		if isEnumBoolMap {
			var err error
			def, err = formatDefaultFromEnumBoolMap(reflectField, v.Value)
			if err != nil {
				return nil, err
			}
		}
		keys = append(keys, doc.EnumKey{
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

	cmds, err := commandmeta.Load()
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

// TODO(adonovan): this format is strange; it's not Go, nor JSON, nor LSP. Rethink.
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

// loadLenses combines the syntactic comments from the settings
// package with the default values from settings.DefaultOptions(), and
// returns a list of Code Lens descriptors.
func loadLenses(settingsPkg *packages.Package, defaults map[settings.CodeLensSource]bool) ([]*doc.Lens, error) {
	// Find the CodeLensSource enums among the files of the protocol package.
	// Map each enum value to its doc comment.
	enumDoc := make(map[string]string)
	for _, f := range settingsPkg.Syntax {
		for _, decl := range f.Decls {
			if decl, ok := decl.(*ast.GenDecl); ok && decl.Tok == token.CONST {
				for _, spec := range decl.Specs {
					spec := spec.(*ast.ValueSpec)
					posn := safetoken.StartPosition(settingsPkg.Fset, spec.Pos())
					if id, ok := spec.Type.(*ast.Ident); ok && id.Name == "CodeLensSource" {
						if len(spec.Names) != 1 || len(spec.Values) != 1 {
							return nil, fmt.Errorf("%s: declare one CodeLensSource per line", posn)
						}
						lit, ok := spec.Values[0].(*ast.BasicLit)
						if !ok && lit.Kind != token.STRING {
							return nil, fmt.Errorf("%s: CodeLensSource value is not a string literal", posn)
						}
						value, _ := strconv.Unquote(lit.Value) // ignore error: AST is well-formed
						if spec.Doc == nil {
							return nil, fmt.Errorf("%s: %s lacks doc comment", posn, spec.Names[0].Name)
						}
						enumDoc[value] = spec.Doc.Text()
					}
				}
			}
		}
	}
	if len(enumDoc) == 0 {
		return nil, fmt.Errorf("failed to extract any CodeLensSource declarations")
	}

	// Build list of Lens descriptors.
	var lenses []*doc.Lens
	addAll := func(sources map[settings.CodeLensSource]cache.CodeLensSourceFunc, fileType string) error {
		slice := maps.Keys(sources)
		sort.Slice(slice, func(i, j int) bool { return slice[i] < slice[j] })
		for _, source := range slice {
			docText, ok := enumDoc[string(source)]
			if !ok {
				return fmt.Errorf("missing CodeLensSource declaration for %s", source)
			}
			title, docText, _ := strings.Cut(docText, "\n") // first line is title
			lenses = append(lenses, &doc.Lens{
				FileType: fileType,
				Lens:     string(source),
				Title:    title,
				Doc:      docText,
				Default:  defaults[source],
			})
		}
		return nil
	}
	addAll(golang.CodeLensSources(), "Go")
	addAll(mod.CodeLensSources(), "go.mod")
	return lenses, nil
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
	title   string // dotted path (e.g. "ui.documentation")
	final   string // final segment of title (e.g. "documentation")
	level   int
	options []*doc.Option
}

func rewriteSettings(prevContent []byte, api *doc.API) ([]byte, error) {
	content := prevContent
	for category, opts := range api.Options {
		groups := collectGroups(opts)

		var buf bytes.Buffer

		// First, print a table of contents (ToC).
		fmt.Fprintln(&buf)
		for _, h := range groups {
			title := h.final
			if title != "" {
				fmt.Fprintf(&buf, "%s* [%s](#%s)\n",
					strings.Repeat("  ", h.level),
					capitalize(title),
					strings.ToLower(title))
			}
		}

		// Section titles are h2, options are h3.
		// This is independent of the option hierarchy.
		// (Nested options should not be smaller!)
		fmt.Fprintln(&buf)
		for _, h := range groups {
			title := h.final
			if title != "" {
				// Emit HTML anchor as GitHub markdown doesn't support
				// "# Heading {#anchor}" syntax.
				fmt.Fprintf(&buf, "<a id='%s'></a>\n", strings.ToLower(title))

				fmt.Fprintf(&buf, "## %s\n\n", capitalize(title))
			}
			for _, opt := range h.options {
				// Emit HTML anchor as GitHub markdown doesn't support
				// "# Heading {#anchor}" syntax.
				//
				// (Each option name is the camelCased name of a field of
				// settings.UserOptions or one of its FooOptions subfields.)
				fmt.Fprintf(&buf, "<a id='%s'></a>\n", opt.Name)

				// heading
				//
				// TODO(adonovan): We should display not the Go type (e.g.
				// `time.Duration`, `map[Enum]bool`) for each setting,
				// but its JSON type, since that's the actual interface.
				// We need a better way to derive accurate JSON type descriptions
				// from Go types. eg. "a string parsed as if by
				// `time.Duration.Parse`". (`time.Duration` is an integer, not
				// a string!)
				//
				// We do not display the undocumented dotted-path alias
				// (h.title + "." + opt.Name) used by VS Code only.
				fmt.Fprintf(&buf, "### `%s` *%v*\n\n", opt.Name, opt.Type)

				// status
				switch opt.Status {
				case "":
				case "advanced":
					fmt.Fprint(&buf, "**This is an advanced setting and should not be configured by most `gopls` users.**\n\n")
				case "debug":
					fmt.Fprint(&buf, "**This setting is for debugging purposes only.**\n\n")
				case "experimental":
					fmt.Fprint(&buf, "**This setting is experimental and may be deleted.**\n\n")
				default:
					fmt.Fprintf(&buf, "**Status: %s.**\n\n", opt.Status)
				}

				// doc comment
				buf.WriteString(opt.Doc)

				// enums
				write := func(name, doc string) {
					if doc != "" {
						unbroken := parBreakRE.ReplaceAllString(doc, "\\\n")
						fmt.Fprintf(&buf, "* %s\n", strings.TrimSpace(unbroken))
					} else {
						fmt.Fprintf(&buf, "* `%s`\n", name)
					}
				}
				if len(opt.EnumValues) > 0 && opt.Type == "enum" {
					// enum as top-level type constructor
					buf.WriteString("\nMust be one of:\n\n")
					for _, val := range opt.EnumValues {
						write(val.Value, val.Doc)
					}
				} else if len(opt.EnumKeys.Keys) > 0 && shouldShowEnumKeysInSettings(opt.Name) {
					// enum as map key (currently just "annotations")
					buf.WriteString("\nEach enum must be one of:\n\n")
					for _, val := range opt.EnumKeys.Keys {
						write(val.Name, val.Doc)
					}
				}

				// default value
				fmt.Fprintf(&buf, "\nDefault: `%v`.\n\n", opt.Default)
			}
		}
		newContent, err := replaceSection(content, category, buf.Bytes())
		if err != nil {
			return nil, err
		}
		content = newContent
	}
	return content, nil
}

var parBreakRE = regexp.MustCompile("\n{2,}")

func shouldShowEnumKeysInSettings(name string) bool {
	// These fields have too many possible options,
	// or too voluminous documentation, to render as enums.
	// Instead they each get their own page in the manual.
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

func capitalize(s string) string {
	return string(unicode.ToUpper(rune(s[0]))) + s[1:]
}

func rewriteCodeLenses(prevContent []byte, api *doc.API) ([]byte, error) {
	var buf bytes.Buffer
	for _, lens := range api.Lenses {
		fmt.Fprintf(&buf, "## `%s`: %s\n\n", lens.Lens, lens.Title)
		fmt.Fprintf(&buf, "%s\n\n", lens.Doc)
		fmt.Fprintf(&buf, "Default: %v\n\n", onOff(lens.Default))
		fmt.Fprintf(&buf, "File type: %s\n\n", lens.FileType)
	}
	return replaceSection(prevContent, "Lenses", buf.Bytes())
}

func rewriteCommands(prevContent []byte, api *doc.API) ([]byte, error) {
	var buf bytes.Buffer
	for _, command := range api.Commands {
		fmt.Fprintf(&buf, "## `%s`: **%s**\n\n%v\n\n", command.Command, command.Title, command.Doc)
		if command.ArgDoc != "" {
			fmt.Fprintf(&buf, "Args:\n\n```\n%s\n```\n\n", command.ArgDoc)
		}
		if command.ResultDoc != "" {
			fmt.Fprintf(&buf, "Result:\n\n```\n%s\n```\n\n", command.ResultDoc)
		}
	}
	return replaceSection(prevContent, "Commands", buf.Bytes())
}

func rewriteAnalyzers(prevContent []byte, api *doc.API) ([]byte, error) {
	var buf bytes.Buffer
	for _, analyzer := range api.Analyzers {
		fmt.Fprintf(&buf, "<a id='%s'></a>\n", analyzer.Name)
		title, doc, _ := strings.Cut(analyzer.Doc, "\n")
		title = strings.TrimPrefix(title, analyzer.Name+": ")
		fmt.Fprintf(&buf, "## `%s`: %s\n\n", analyzer.Name, title)
		fmt.Fprintf(&buf, "%s\n\n", doc)
		fmt.Fprintf(&buf, "Default: %s.", onOff(analyzer.Default))
		if !analyzer.Default {
			fmt.Fprintf(&buf, " Enable by setting `\"analyses\": {\"%s\": true}`.", analyzer.Name)
		}
		fmt.Fprintf(&buf, "\n\n")
		if analyzer.URL != "" {
			// TODO(adonovan): currently the URL provides the same information
			// as 'doc' above, though that may change due to
			// https://github.com/golang/go/issues/61315#issuecomment-1841350181.
			// In that case, update this to something like "Complete documentation".
			fmt.Fprintf(&buf, "Package documentation: [%s](%s)\n\n",
				analyzer.Name, analyzer.URL)
		}

	}
	return replaceSection(prevContent, "Analyzers", buf.Bytes())
}

func rewriteInlayHints(prevContent []byte, api *doc.API) ([]byte, error) {
	var buf bytes.Buffer
	for _, hint := range api.Hints {
		fmt.Fprintf(&buf, "## **%v**\n\n", hint.Name)
		fmt.Fprintf(&buf, "%s\n\n", hint.Doc)
		switch hint.Default {
		case true:
			fmt.Fprintf(&buf, "**Enabled by default.**\n\n")
		case false:
			fmt.Fprintf(&buf, "**Disabled by default. Enable it by setting `\"hints\": {\"%s\": true}`.**\n\n", hint.Name)
		}
	}
	return replaceSection(prevContent, "Hints", buf.Bytes())
}

// replaceSection replaces the portion of a file delimited by comments of the form:
//
//	<!-- BEGIN sectionName -->
//	<!-- END section Name -->
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

type onOff bool

func (o onOff) String() string {
	if o {
		return "on"
	} else {
		return "off"
	}
}
