// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The test for validation uses the official test suite, expressed as a set of JSON files.
// Each file is an array of group objects.

// A testGroup consists of a schema and some tests on it.
type testGroup struct {
	Description string
	Schema      *Schema
	Tests       []test
}

// A test consists of a JSON instance to be validated and the expected result.
type test struct {
	Description string
	Data        any
	Valid       bool
}

func TestValidate(t *testing.T) {
	files, err := filepath.Glob(filepath.FromSlash("testdata/draft2020-12/*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files")
	}
	for _, file := range files {
		base := filepath.Base(file)
		t.Run(base, func(t *testing.T) {
			f, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			dec := json.NewDecoder(f)
			var groups []testGroup
			if err := dec.Decode(&groups); err != nil {
				t.Fatal(err)
			}
			for _, g := range groups {
				t.Run(g.Description, func(t *testing.T) {
					rs, err := g.Schema.Resolve("")
					if err != nil {
						t.Fatal(err)
					}
					for s := range g.Schema.all() {
						if s.Defs != nil || s.Ref != "" {
							t.Skip("schema or subschema has unimplemented keywords")
						}
					}
					for _, test := range g.Tests {
						t.Run(test.Description, func(t *testing.T) {
							err = rs.Validate(test.Data)
							if err != nil && test.Valid {
								t.Errorf("wanted success, but failed with: %v", err)
							}
							if err == nil && !test.Valid {
								t.Error("succeeded but wanted failure")
							}
							if t.Failed() {
								t.Errorf("schema: %s", g.Schema.json())
								t.Fatalf("instance: %v", test.Data)
							}
						})
					}
				})
			}
		})
	}
}

func TestStructInstance(t *testing.T) {
	instance := struct {
		I int
		B bool `json:"b"`
		u int
	}{1, true, 0}

	// The instance fails for all of these schemas, demonstrating that it
	// was processed correctly.
	for _, schema := range []*Schema{
		{MinProperties: Ptr(3)},
		{MaxProperties: Ptr(1)},
		{Required: []string{"i"}}, // the name is "I"
		{Required: []string{"B"}}, // the name is "b"
		{PropertyNames: &Schema{MinLength: Ptr(2)}},
		{Properties: map[string]*Schema{"b": {Type: "number"}}},
		{Required: []string{"I"}, AdditionalProperties: falseSchema()},
		{DependentRequired: map[string][]string{"b": {"u"}}},
		{DependentSchemas: map[string]*Schema{"b": falseSchema()}},
		{UnevaluatedProperties: falseSchema()},
	} {
		res, err := schema.Resolve("")
		if err != nil {
			t.Fatal(err)
		}
		err = res.Validate(instance)
		if err == nil {
			t.Errorf("succeeded but wanted failure; schema = %s", schema.json())
		}
	}
}
