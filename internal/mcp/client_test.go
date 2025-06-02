// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/internal/mcp/jsonschema"
)

type Item struct {
	Name  string
	Value string
}

type ListTestParams struct {
	Cursor string
}

func (p *ListTestParams) cursorPtr() *string {
	return &p.Cursor
}

type ListTestResult struct {
	Items      []*Item
	NextCursor string
}

func (r *ListTestResult) nextCursorPtr() *string {
	return &r.NextCursor
}

var allItems = []*Item{
	{"alpha", "val-A"},
	{"bravo", "val-B"},
	{"charlie", "val-C"},
	{"delta", "val-D"},
	{"echo", "val-E"},
	{"foxtrot", "val-F"},
	{"golf", "val-G"},
	{"hotel", "val-H"},
	{"india", "val-I"},
	{"juliet", "val-J"},
	{"kilo", "val-K"},
}

func toItemValueSlice(ptrSlice []*Item) []Item {
	var valSlice []Item
	for _, ptr := range ptrSlice {
		valSlice = append(valSlice, *ptr)
	}
	return valSlice
}

// generatePaginatedResults is a helper to create a sequence of mock responses for pagination.
// It simulates a server returning items in pages based on a given page size.
func generatePaginatedResults(all []*Item, pageSize int) []*ListTestResult {
	if len(all) == 0 {
		return []*ListTestResult{{Items: []*Item{}, NextCursor: ""}}
	}
	if pageSize <= 0 {
		panic("pageSize must be greater than 0")
	}
	numPages := (len(all) + pageSize - 1) / pageSize // Ceiling division
	var results []*ListTestResult
	for i := range numPages {
		startIndex := i * pageSize
		endIndex := min(startIndex+pageSize, len(all)) // Use min to prevent out of bounds
		nextCursor := ""
		if endIndex < len(all) { // If there are more items after this page
			nextCursor = fmt.Sprintf("cursor_%d", endIndex)
		}
		results = append(results, &ListTestResult{Items: all[startIndex:endIndex], NextCursor: nextCursor})
	}
	return results
}

func TestClientPaginateBasic(t *testing.T) {
	ctx := context.Background()
	testCases := []struct {
		name          string
		results       []*ListTestResult
		mockError     error
		initialParams *ListTestParams
		expected      []Item
		expectError   bool
	}{
		{
			name:     "SinglePageAllItems",
			results:  generatePaginatedResults(allItems, len(allItems)),
			expected: toItemValueSlice(allItems),
		},
		{
			name:     "MultiplePages",
			results:  generatePaginatedResults(allItems, 3),
			expected: toItemValueSlice(allItems),
		},
		{
			name:     "EmptyResults",
			results:  generatePaginatedResults([]*Item{}, 10),
			expected: nil,
		},
		{
			name:        "ListFuncReturnsErrorImmediately",
			results:     []*ListTestResult{{}},
			mockError:   fmt.Errorf("API error on first call"),
			expected:    nil,
			expectError: true,
		},
		{
			name:          "InitialCursorProvided",
			initialParams: &ListTestParams{Cursor: "cursor_2"},
			results:       generatePaginatedResults(allItems[2:], 3),
			expected:      toItemValueSlice(allItems[2:]),
		},
		{
			name:          "CursorBeyondAllItems",
			initialParams: &ListTestParams{Cursor: "cursor_999"},
			results:       []*ListTestResult{{Items: []*Item{}, NextCursor: ""}},
			expected:      nil,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			listFunc := func(ctx context.Context, params *ListTestParams) (*ListTestResult, error) {
				if len(tc.results) == 0 {
					t.Fatalf("listFunc called but no more results defined for test case %q", tc.name)
				}
				res := tc.results[0]
				tc.results = tc.results[1:]
				var err error
				if tc.mockError != nil {
					err = tc.mockError
				}
				return res, err
			}

			params := tc.initialParams
			if tc.initialParams == nil {
				params = &ListTestParams{}
			}

			var gotItems []Item
			var iterationErr error
			seq := paginate(ctx, params, listFunc, func(r *ListTestResult) []*Item { return r.Items })
			for item, err := range seq {
				if err != nil {
					iterationErr = err
					break
				}
				gotItems = append(gotItems, item)
			}
			if tc.expectError {
				if iterationErr == nil {
					t.Errorf("paginate() expected an error during iteration, but got none")
				}
			} else {
				if iterationErr != nil {
					t.Errorf("paginate() got: %v, want: nil", iterationErr)
				}
			}
			if diff := cmp.Diff(tc.expected, gotItems, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Fatalf("paginate() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestClientPaginateVariousPageSizes(t *testing.T) {
	ctx := context.Background()
	for i := 1; i < len(allItems)+1; i++ {
		testname := fmt.Sprintf("PageSize=%d", i)
		t.Run(testname, func(t *testing.T) {
			results := generatePaginatedResults(allItems, i)
			listFunc := func(ctx context.Context, params *ListTestParams) (*ListTestResult, error) {
				res := results[0]
				results = results[1:]
				return res, nil
			}
			var gotItems []Item
			seq := paginate(ctx, &ListTestParams{}, listFunc, func(r *ListTestResult) []*Item { return r.Items })
			for item, err := range seq {
				if err != nil {
					t.Fatalf("paginate() unexpected error during iteration: %v", err)
				}
				gotItems = append(gotItems, item)
			}
			if diff := cmp.Diff(toItemValueSlice(allItems), gotItems, cmpopts.IgnoreUnexported(jsonschema.Schema{})); diff != "" {
				t.Fatalf("paginate() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestToWireParams(t *testing.T) {
	// This test verifies that toWireParams maps all fields.
	// The Meta and Arguments fields are not comparable, so can't be checked by
	// this simple test. However, this test will fail if new fields are added,
	// and not handled by toWireParams.
	params := &CallToolParams[struct{}]{
		Name: "tool",
	}
	wireParams, err := toWireParams(params)
	if err != nil {
		t.Fatal(err)
	}
	v := reflect.ValueOf(wireParams).Elem()
	for i := range v.Type().NumField() {
		f := v.Type().Field(i)
		if f.Name == "Meta" || f.Name == "Arguments" {
			continue // not comparable
		}
		fv := v.Field(i)
		if fv.Interface() == reflect.Zero(f.Type).Interface() {
			t.Fatalf("toWireParams: unmapped field %q", f.Name)
		}
	}
}
