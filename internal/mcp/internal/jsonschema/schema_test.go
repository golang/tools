// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/mcp/internal/jsonschema"
)

func forType[T any]() *jsonschema.Schema {
	s, err := jsonschema.ForType[T]()
	if err != nil {
		panic(err)
	}
	return s
}

func TestForType(t *testing.T) {
	type schema = jsonschema.Schema
	tests := []struct {
		name string
		got  *jsonschema.Schema
		want *jsonschema.Schema
	}{
		{"string", forType[string](), &schema{Type: "string"}},
		{"int", forType[int](), &schema{Type: "integer"}},
		{"int16", forType[int16](), &schema{Type: "integer"}},
		{"uint32", forType[int16](), &schema{Type: "integer"}},
		{"float64", forType[float64](), &schema{Type: "number"}},
		{"bool", forType[bool](), &schema{Type: "boolean"}},
		{"intmap", forType[map[string]int](), &schema{
			Type:                 "object",
			AdditionalProperties: &schema{Type: "integer"},
		}},
		{"anymap", forType[map[string]any](), &schema{
			Type:                 "object",
			AdditionalProperties: &schema{},
		}},
		{"struct", forType[struct {
			F          int `json:"f"`
			G          []float64
			Skip       string `json:"-"`
			unexported float64
		}](), &schema{
			Type: "object",
			Properties: map[string]*schema{
				"f": {Type: "integer"},
				"G": {Type: "array", Items: &schema{Type: "number"}},
			},
			AdditionalProperties: false,
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if diff := cmp.Diff(test.want, test.got); diff != "" {
				t.Errorf("ForType mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
