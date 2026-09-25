// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWatchGoWorkspace(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"module/subdir", "workspace", "not-a-module/go.mod"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0777); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{
		"module/go.mod":     "module example.com\ngo 1.18\n",
		"workspace/go.work": "go 1.18\nuse ../module\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0666); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		dir  string
		want bool
	}{
		{"module", true},
		{"workspace", true},
		{".", false},             // A parent containing Go projects must not be watched.
		{"module/subdir", false}, // Do not expand a root to an ancestor.
		{"not-a-module", false},  // A directory named go.mod is not a module file.
		{"missing", false},
	} {
		t.Run(test.dir, func(t *testing.T) {
			dir := filepath.Join(root, test.dir)
			called := false
			watchErr := errors.New("watch failed")
			err := watchGoWorkspace(dir, func(got string) error {
				called = true
				if got != dir {
					t.Errorf("watched %q, want %q", got, dir)
				}
				return watchErr
			})
			if called != test.want {
				t.Errorf("watch called = %t, want %t", called, test.want)
			}
			if err == nil || test.want && !errors.Is(err, watchErr) {
				t.Errorf("watch error = %v, want rejection or underlying watcher error", err)
			}
		})
	}
}
