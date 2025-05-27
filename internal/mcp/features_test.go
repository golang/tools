// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

type SayHiParams struct {
	Name string `json:"name"`
}

func SayHi(ctx context.Context, cc *ServerSession, params *CallToolParams[SayHiParams]) (*CallToolResult, error) {
	return &CallToolResult{
		Content: []*Content{
			NewTextContent("Hi " + params.Name),
		},
	}, nil
}

func TestFeatureSetOrder(t *testing.T) {
	toolA := NewTool("apple", "apple tool", SayHi).Tool
	toolB := NewTool("banana", "banana tool", SayHi).Tool
	toolC := NewTool("cherry", "cherry tool", SayHi).Tool

	testCases := []struct {
		tools []*Tool
		want  []*Tool
	}{
		{[]*Tool{toolA, toolB, toolC}, []*Tool{toolA, toolB, toolC}},
		{[]*Tool{toolB, toolC, toolA}, []*Tool{toolA, toolB, toolC}},
		{[]*Tool{toolA, toolC}, []*Tool{toolA, toolC}},
		{[]*Tool{toolA, toolA, toolA}, []*Tool{toolA}},
		{[]*Tool{}, nil},
	}
	for _, tc := range testCases {
		fs := newFeatureSet(func(t *Tool) string { return t.Name })
		fs.add(tc.tools...)
		got := slices.Collect(fs.all())
		if diff := cmp.Diff(got, tc.want, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("expected %v, got %v, (-want +got):\n%s", tc.want, got, diff)
		}
	}
}

func TestFeatureSetAbove(t *testing.T) {
	toolA := NewTool("apple", "apple tool", SayHi).Tool
	toolB := NewTool("banana", "banana tool", SayHi).Tool
	toolC := NewTool("cherry", "cherry tool", SayHi).Tool

	testCases := []struct {
		tools []*Tool
		above string
		want  []*Tool
	}{
		{[]*Tool{toolA, toolB, toolC}, "apple", []*Tool{toolB, toolC}},
		{[]*Tool{toolA, toolB, toolC}, "banana", []*Tool{toolC}},
		{[]*Tool{toolA, toolB, toolC}, "cherry", nil},
	}
	for _, tc := range testCases {
		fs := newFeatureSet(func(t *Tool) string { return t.Name })
		fs.add(tc.tools...)
		got := slices.Collect(fs.above(tc.above))
		if diff := cmp.Diff(got, tc.want, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
			t.Errorf("expected %v, got %v, (-want +got):\n%s", tc.want, got, diff)

		}
	}
}
