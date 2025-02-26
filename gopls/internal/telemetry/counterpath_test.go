// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package telemetry

import (
	"testing"
)

// TestCounterPath tests the formatting of various counter paths.
func TestCounterPath(t *testing.T) {
	tests := []struct {
		path CounterPath
		want string
	}{
		{
			path: CounterPath{},
			want: "",
		},
		{
			path: CounterPath{"counter"},
			want: ":counter",
		},
		{
			path: CounterPath{"counter", "bucket"},
			want: "counter:bucket",
		},
		{
			path: CounterPath{"path", "to", "counter"},
			want: "path/to:counter",
		},
		{
			path: CounterPath{"multi", "component", "path", "bucket"},
			want: "multi/component/path:bucket",
		},
		{
			path: CounterPath{"path", ""},
			want: "path",
		},
	}
	for _, tt := range tests {
		if got := tt.path.FullName(); got != tt.want {
			t.Errorf("CounterPath(%v).FullName() = %v, want %v", tt.path, got, tt.want)
		}
	}
}
