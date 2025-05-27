// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package checker

import (
	"reflect"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/packages"
)

func TestAnalysisModuleFromPackageModule(t *testing.T) {
	tests := []struct {
		name string
		mod  *packages.Module
		want *analysis.Module
	}{
		{
			name: "nil module",
			mod:  nil,
			want: nil,
		},
		{
			name: "module with replace",
			mod: &packages.Module{
				Path:    "example.com/foo",
				Version: "v1.0.0",
				GoMod:   "/path/to/go.mod",
				Replace: &packages.Module{
					Path: "example.com/bar",
				},
			},
			want: &analysis.Module{
				Path:    "example.com/foo",
				Version: "v1.0.0",
				GoMod:   "/path/to/go.mod",
				Replace: &analysis.Module{
					Path: "example.com/bar",
				},
			},
		},
		{
			name: "module with error",
			mod: &packages.Module{
				Path:  "example.com/foo",
				Error: &packages.ModuleError{Err: "something is wrong"},
			},
			want: &analysis.Module{
				Path:  "example.com/foo",
				Error: &analysis.ModuleError{Err: "something is wrong"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analysisModuleFromPackageModule(tt.mod)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("analysisModuleFromPackageModule() = %v, want %v", got, tt.want)
			}
		})
	}
}
