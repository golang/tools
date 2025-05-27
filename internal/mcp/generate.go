// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// This script generates protocol definitions in protocol.go from the MCP spec.
//
// Only the set of declarations configured by the [declarations] value are
// generated.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/internal/mcp/internal/util"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

var schemaFile = flag.String("schema_file", "", "if set, use this file as the persistent schema file")

// A typeConfig defines a rewrite to perform to a (possibly nested) struct
// field. In some cases, we may want to use an external type for the nested
// struct field. In others, we may want to extract the type definition to a
// name.
type typeConfig struct {
	Name       string      // declaration name for the type
	TypeParams [][2]string // formatted type parameter list ({name, constraint}), if any
	Substitute string      // type definition to substitute
	Fields     config      // individual field configuration, or nil
}

type config map[string]*typeConfig

// declarations configures the set of declarations to write.
//
// Top level declarations are created unless configured with Name=="-",
// in which case they are discarded, though their fields may be
// extracted to types if they have a nested field configuration.
// If Name == "", the map key is used as the type name.
var declarations = config{
	"Annotations": {},
	"CallToolRequest": {
		Name: "-",
		Fields: config{
			"Params": {
				Name:       "CallToolParams",
				TypeParams: [][2]string{{"TArgs", "any"}},
				Fields: config{
					"Arguments": {Substitute: "TArgs"},
				},
			},
		},
	},
	"CallToolResult": {},
	"CancelledNotification": {
		Name:   "-",
		Fields: config{"Params": {Name: "CancelledParams"}},
	},
	"ClientCapabilities": {
		Fields: config{"Sampling": {Name: "SamplingCapabilities"}},
	},
	"CreateMessageRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "CreateMessageParams"}},
	},
	"CreateMessageResult": {},
	"GetPromptRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "GetPromptParams"}},
	},
	"GetPromptResult": {},
	"Implementation":  {Name: "implementation"},
	"InitializeRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "InitializeParams"}},
	},
	"InitializeResult": {Name: "InitializeResult"},
	"InitializedNotification": {
		Name:   "-",
		Fields: config{"Params": {Name: "InitializedParams"}},
	},
	"ListPromptsRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "ListPromptsParams"}},
	},
	"ListPromptsResult": {},
	"ListResourcesRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "ListResourcesParams"}},
	},
	"ListResourcesResult": {},
	"ListRootsRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "ListRootsParams"}},
	},
	"ListRootsResult": {},
	"ListToolsRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "ListToolsParams"}},
	},
	"ListToolsResult":     {},
	"loggingCapabilities": {Substitute: "struct{}"},
	"LoggingLevel":        {},
	"LoggingMessageNotification": {
		Name: "-",
		Fields: config{
			"Params": {
				Name:   "LoggingMessageParams",
				Fields: config{"Data": {Substitute: "any"}},
			},
		},
	},
	"ModelHint":        {},
	"ModelPreferences": {},
	"PingRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "PingParams"}},
	},
	"Prompt":         {},
	"PromptMessage":  {},
	"PromptArgument": {},
	"PromptListChangedNotification": {
		Name:   "-",
		Fields: config{"Params": {Name: "PromptListChangedParams"}},
	},
	"ProgressToken": {Name: "-", Substitute: "any"}, // null|number|string
	"RequestId":     {Name: "-", Substitute: "any"}, // null|number|string
	"ReadResourceRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "ReadResourceParams"}},
	},
	"ReadResourceResult": {
		Fields: config{"Contents": {Substitute: "[]*ResourceContents"}},
	},
	"Resource": {},
	"ResourceListChangedNotification": {
		Name:   "-",
		Fields: config{"Params": {Name: "ResourceListChangedParams"}},
	},
	"Role": {},
	"Root": {},
	"RootsListChangedNotification": {
		Name:   "-",
		Fields: config{"Params": {Name: "RootsListChangedParams"}},
	},

	"SamplingCapabilities": {Substitute: "struct{}"},
	"SamplingMessage":      {},
	"ServerCapabilities": {
		Name: "serverCapabilities",
		Fields: config{
			"Prompts":   {Name: "promptCapabilities"},
			"Resources": {Name: "resourceCapabilities"},
			"Tools":     {Name: "toolCapabilities"},
			"Logging":   {Name: "loggingCapabilities"},
		},
	},
	"SetLevelRequest": {
		Name:   "-",
		Fields: config{"Params": {Name: "SetLevelParams"}},
	},
	"Tool": {
		Fields: config{"InputSchema": {Substitute: "*jsonschema.Schema"}},
	},
	"ToolAnnotations": {},
	"ToolListChangedNotification": {
		Name:   "-",
		Fields: config{"Params": {Name: "ToolListChangedParams"}},
	},
}

func main() {
	flag.Parse()

	// Load and unmarshal the schema.
	data, err := loadSchema(*schemaFile)
	if err != nil {
		log.Fatal(err)
	}
	schema := new(jsonschema.Schema)
	if err := json.Unmarshal(data, &schema); err != nil {
		log.Fatal(err)
	}
	// Resolve the schema so we have the referents of all the Refs.
	if _, err := schema.Resolve(nil); err != nil {
		log.Fatal(err)
	}

	// Collect named types. Since we may create new type definitions while
	// writing types, we collect definitions and concatenate them later. This
	// also allows us to sort.
	named := make(map[string]*bytes.Buffer)
	for name, def := range util.Sorted(schema.Definitions) {
		config := declarations[name]
		if config == nil {
			continue
		}
		if err := writeDecl(name, *config, def, named); err != nil {
			log.Fatal(err)
		}
	}

	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, `
// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated by generate.go. DO NOT EDIT.

package mcp

import (
	"golang.org/x/tools/internal/mcp/jsonschema"
)
`)

	// Write out types.
	for _, b := range util.Sorted(named) {
		fmt.Fprintln(buf)
		fmt.Fprint(buf, b.String())
	}
	// Write out method names.
	fmt.Fprintln(buf, `const (`)
	for _, name := range slices.Sorted(maps.Keys(schema.Definitions)) {
		prefix := "method"
		method, found := strings.CutSuffix(name, "Request")
		if !found {
			prefix = "notification"
			method, found = strings.CutSuffix(name, "Notification")
		}
		if found {
			if ms, ok := schema.Definitions[name].Properties["method"]; ok {
				if c := ms.Const; c != nil {
					fmt.Fprintf(buf, "%s%s = %q\n", prefix, method, *c)
				}
			}
		}
	}
	fmt.Fprintln(buf, `)`)

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		log.Println(buf.String())
		log.Fatalf("failed to format: %v", err)
	}
	if err := os.WriteFile("protocol.go", formatted, 0666); err != nil {
		log.Fatalf("failed to write protocol.go: %v", err)
	}
}

func loadSchema(schemaFile string) (data []byte, err error) {
	const schemaURL = "https://raw.githubusercontent.com/modelcontextprotocol/modelcontextprotocol/refs/heads/main/schema/2025-03-26/schema.json"

	if schemaFile != "" {
		data, err = os.ReadFile(schemaFile)
		if os.IsNotExist(err) {
			data = nil
		} else if err != nil {
			return nil, fmt.Errorf("reading schema file %q: %v", schemaFile, err)
		}
	}
	if data == nil {
		resp, err := http.Get(schemaURL)
		if err != nil {
			return nil, fmt.Errorf("downloading schema: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("downloading schema: %v", resp.Status)
		}
		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("reading schema body: %v", err)
		}
		if schemaFile != "" {
			if err := os.WriteFile(schemaFile, data, 0666); err != nil {
				return nil, fmt.Errorf("persisting schema: %v", err)
			}
		}
	}
	return data, nil
}

func writeDecl(configName string, config typeConfig, def *jsonschema.Schema, named map[string]*bytes.Buffer) error {
	var w io.Writer = io.Discard
	var typeName string
	if typeName = config.Name; typeName != "-" {
		if typeName == "" {
			typeName = configName
		}
		if _, ok := named[typeName]; ok {
			return nil
		}
		// The JSON schema does not accurately represent the source of truth, which is typescript.
		// Every Params and Result type should have a _meta property.
		// Also, those with a progress token will turn into a struct; we want the progress token to
		// be a map item. So replace all metas.
		if strings.HasSuffix(typeName, "Params") || strings.HasSuffix(typeName, "Result") {
			def.Properties["_meta"] = metaSchema
		}
		buf := new(bytes.Buffer)
		w = buf
		named[typeName] = buf
		if def.Description != "" {
			fmt.Fprintf(buf, "%s\n", toComment(def.Description))
		}
		typeParams := new(strings.Builder)
		if len(config.TypeParams) > 0 {
			typeParams.WriteByte('[')
			for i, p := range config.TypeParams {
				if i > 0 {
					typeParams.WriteString(", ")
				}
				fmt.Fprintf(typeParams, "%s %s", p[0], p[1])
			}
			typeParams.WriteByte(']')
		}
		fmt.Fprintf(buf, "type %s%s ", typeName, typeParams)
	}
	if err := writeType(w, &config, def, named); err != nil {
		return err // Better error here?
	}
	fmt.Fprintf(w, "\n")

	// Any decl with a _meta field gets a GetMeta method.
	if _, ok := def.Properties["_meta"]; ok {
		targs := new(strings.Builder)
		if len(config.TypeParams) > 0 {
			targs.WriteByte('[')
			for i, p := range config.TypeParams {
				if i > 0 {
					targs.WriteString(", ")
				}
				fmt.Fprintf(targs, "%s", p[0])
			}
			targs.WriteByte(']')
		}
		fmt.Fprintf(w, "\nfunc (x *%s%s) GetMeta() *Meta { return &x.Meta }", typeName, targs)
	}

	if _, ok := def.Properties["cursor"]; ok {
		fmt.Fprintf(w, "\nfunc (x *%s) cursorPtr() *string { return &x.Cursor }", typeName)
	}
	if _, ok := def.Properties["nextCursor"]; ok {
		fmt.Fprintf(w, "\nfunc (x *%s) nextCursorPtr() *string { return &x.NextCursor }", typeName)
	}

	return nil
}

// writeType writes the type definition to the given writer.
//
// If path is non-empty, it is the path to the field using this type, for the
// purpose of detecting field rewrites (see [fieldRewrite]).
//
// named is the in-progress collection of type definitions. New named types may
// be added during writeType, if they are extracted from inner fields.
func writeType(w io.Writer, config *typeConfig, def *jsonschema.Schema, named map[string]*bytes.Buffer) error {
	// Use type names for Named types.
	name, resolved := deref(def)
	if name != "" {
		// TODO: this check is not quite right: we should really panic if the
		// definition is missing, *but only if w is not io.Discard*. That's not a
		// great API: see if we can do something more explicit than io.Discard.
		if cfg, ok := declarations[name]; ok {
			if cfg.Name == "-" && cfg.Substitute == "" {
				panic(fmt.Sprintf("referenced type %q cannot be referred to (no name or substitution)", name))
			}
			if cfg.Substitute != "" {
				name = cfg.Substitute
			} else if cfg.Name != "" {
				name = cfg.Name
			}
			if isStruct(resolved) {
				w.Write([]byte{'*'})
			}
		}
		w.Write([]byte(name))
		return nil
	}

	// For types that explicitly allow additional properties, we can either
	// unmarshal them into a map[string]any, or delay unmarshalling with
	// json.RawMessage. We use any.
	if def.Type == "object" && canHaveAdditionalProperties(def) && def.Properties == nil {
		w.Write([]byte("map[string]"))
		return writeType(w, nil, def.AdditionalProperties, named)
	}

	if def.Type == "" {
		// special case: recognize Content
		if slices.ContainsFunc(def.AnyOf, func(s *jsonschema.Schema) bool {
			return s.Ref == "#/definitions/TextContent"
		}) {
			fmt.Fprintf(w, "*Content")
		} else {
			// E.g. union types.
			fmt.Fprintf(w, "any")
		}
	} else {
		switch def.Type {
		case "array":
			fmt.Fprintf(w, "[]")
			return writeType(w, nil, def.Items, named)

		case "boolean":
			fmt.Fprintf(w, "bool")

		case "integer":
			fmt.Fprintf(w, "int64")

		// not handled: "null"

		case "number":
			// We could use json.Number here; use float64 for simplicity.
			fmt.Fprintf(w, "float64")

		case "object":
			fmt.Fprintf(w, "struct {\n")
			for name, fieldDef := range util.Sorted(def.Properties) {
				if fieldDef.Description != "" {
					fmt.Fprintf(w, "%s\n", toComment(fieldDef.Description))
				}
				if name == "_meta" {
					fmt.Fprintln(w, "\tMeta Meta `json:\"_meta,omitempty\"`")
					continue
				}

				export := exportName(name)
				fmt.Fprintf(w, "\t%s ", export)

				required := slices.Contains(def.Required, name)

				// If the field is a struct type, indirect with a
				// pointer so that it can be empty as defined by encoding/json.
				// This also future-proofs against the struct getting large.
				fieldTypeSchema := fieldDef
				// If the schema is a reference, dereference it.
				if _, rs := deref(fieldDef); rs != nil {
					fieldTypeSchema = rs
				}
				needPointer := isStruct(fieldTypeSchema)
				// Special case: there are no sampling or logging capabilities defined,
				// but we want them to be structs for future expansion.
				if !needPointer && (name == "sampling" || name == "logging") {
					needPointer = true
				}
				if config != nil && config.Fields[export] != nil {
					r := config.Fields[export]
					if r.Substitute != "" {
						fmt.Fprintf(w, r.Substitute)
					} else {
						assert(r.Name != "-", "missing ExtractTo")
						typename := export
						if r.Name != "" {
							typename = r.Name
						}
						if err := writeDecl(typename, *r, fieldDef, named); err != nil {
							return err
						}
						if needPointer {
							fmt.Fprintf(w, "*")
						}
						fmt.Fprintf(w, typename)
					}
				} else if err := writeType(w, nil, fieldDef, named); err != nil {
					return fmt.Errorf("failed to write type for field %s: %v", export, err)
				}
				fmt.Fprintf(w, " `json:\"%s", name)
				if !required {
					fmt.Fprint(w, ",omitempty")
				}
				fmt.Fprint(w, "\"`\n")
			}
			fmt.Fprintf(w, "}")

		case "string":
			fmt.Fprintf(w, "string")

		default:
			fmt.Fprintf(w, "any")
		}
	}
	return nil
}

// toComment converts a JSON schema description to a Go comment.
func toComment(description string) string {
	var (
		buf     strings.Builder
		lineBuf strings.Builder
	)
	const wrapAt = 80
	for line := range strings.SplitSeq(description, "\n") {
		// Start a new paragraph, if the current is nonempty.
		if len(line) == 0 && lineBuf.Len() > 0 {
			buf.WriteString(lineBuf.String())
			lineBuf.Reset()
			buf.WriteString("\n//\n")
			continue
		}
		// Otherwise, fill in the current paragraph.
		for field := range strings.FieldsSeq(line) {
			if lineBuf.Len() > 0 && lineBuf.Len()+len(" ")+len(field) > wrapAt {
				buf.WriteString(lineBuf.String())
				buf.WriteRune('\n')
				lineBuf.Reset()
			}
			if lineBuf.Len() == 0 {
				lineBuf.WriteString("//")
			}
			lineBuf.WriteString(" ")
			lineBuf.WriteString(field)
		}
	}
	if lineBuf.Len() > 0 {
		buf.WriteString(lineBuf.String())
	}
	return strings.TrimRight(buf.String(), "\n")
}

// The MCP spec improperly uses the absence of the additionalProperties keyword to
// mean that additional properties are not allowed. In fact, it means just the opposite
// (https://json-schema.org/draft-07/draft-handrews-json-schema-validation-01#rfc.section.6.5.6).
// If the MCP spec wants to allow additional properties, it will write "true" or
// an object explicitly.
func canHaveAdditionalProperties(s *jsonschema.Schema) bool {
	ap := s.AdditionalProperties
	return ap != nil && !reflect.DeepEqual(ap, &jsonschema.Schema{Not: &jsonschema.Schema{}})
}

// exportName returns an exported name for a Go symbol, based on the given name
// in the JSON schema, removing leading underscores and capitalizing.
// It also rewrites initialisms.
func exportName(s string) string {
	if strings.HasPrefix(s, "_") {
		s = s[1:]
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	// Replace an initialism if it is its own "word": see the init function below for
	// a definition.
	// There is probably a clever way to write this whole thing with one regexp and
	// a Replace method, but it would be quite obscure.
	// This doesn't have to be fast, because the first match will rarely succeed.
	for ism, re := range initialisms {
		replacement := strings.ToUpper(ism)
		// Find the index of one match at a time, and replace. (We can't find all
		// at once, because the replacement will change the indices.)
		for {
			if loc := re.FindStringIndex(s); loc != nil {
				// Don't replace the rune after the initialism, if any.
				end := loc[1]
				if end < len(s) {
					end--
				}
				s = s[:loc[0]] + replacement + s[end:]
			} else {
				break
			}
		}
	}
	return s
}

// deref dereferences s.Ref.
// If s.Ref refers to a schema in the Definitions section, deref
// returns the definition name and the associated schema.
// Otherwise, deref returns "", nil.
func deref(s *jsonschema.Schema) (name string, _ *jsonschema.Schema) {
	name, ok := strings.CutPrefix(s.Ref, "#/definitions/")
	if !ok {
		return "", nil
	}
	return name, s.ResolvedRef()
}

// isStruct reports whether s should be translated to a struct.
func isStruct(s *jsonschema.Schema) bool {
	return s.Type == "object" && s.Properties != nil && !canHaveAdditionalProperties(s)
}

// The schema for "_meta".
// We only need the description: the rest is a special case.
var metaSchema = &jsonschema.Schema{
	Description: "This property is reserved by the protocol to allow clients and servers to attach additional metadata to their responses.",
}

// schemaJSON returns the JSON for s.
// For debugging.
func schemaJSON(s *jsonschema.Schema) string {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Sprintf("<jsonschema.Schema:%v>", err)
	}
	return string(data)
}

// Map from initialism to the regexp that matches it.
var initialisms = map[string]*regexp.Regexp{
	"Id":   nil,
	"Url":  nil,
	"Uri":  nil,
	"Mime": nil,
}

func init() {
	for ism := range initialisms {
		// Match ism if it is at the end, or followed by an uppercase letter or a number.
		initialisms[ism] = regexp.MustCompile(ism + `($|[A-Z0-9])`)
	}
}

func assert(cond bool, msg string) {
	if !cond {
		panic(msg)
	}
}
