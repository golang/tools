// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filewatcher_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"golang.org/x/tools/gopls/internal/filewatcher"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/txtar"
)

func TestFileWatcher(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("unsupported OS")
	}

	testCases := []struct {
		name           string
		goos           []string // if not empty, only run in these OS.
		initWorkspace  string
		changes        func(t *testing.T, root string)
		expectedEvents []protocol.FileEvent
	}{
		{
			name: "create file in darwin",
			goos: []string{"darwin"},
			initWorkspace: `
-- foo.go --
package foo
`,
			changes: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "bar.go"), []byte("package main"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "bar.go", Type: protocol.Created},
			},
		},
		{
			name: "create file in linux & windows",
			goos: []string{"linux", "windows"},
			initWorkspace: `
-- foo.go --
package foo
`,
			changes: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "bar.go"), []byte("package main"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "bar.go", Type: protocol.Created},
				{URI: "bar.go", Type: protocol.Changed},
			},
		},
		{
			name: "modify file",
			initWorkspace: `
-- foo.go --
package foo
`,
			changes: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main // modified"), 0644); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "foo.go", Type: protocol.Changed},
			},
		},
		{
			name: "delete file",
			initWorkspace: `
-- foo.go --
package foo
-- bar.go --
package bar
`,
			changes: func(t *testing.T, root string) {
				if err := os.Remove(filepath.Join(root, "foo.go")); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "foo.go", Type: protocol.Deleted},
			},
		},
		{
			name: "rename file in linux & windows",
			goos: []string{"linux", "windows"},
			initWorkspace: `
-- foo.go --
package foo
`,
			changes: func(t *testing.T, root string) {
				if err := os.Rename(filepath.Join(root, "foo.go"), filepath.Join(root, "bar.go")); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "foo.go", Type: protocol.Deleted},
				{URI: "bar.go", Type: protocol.Created},
			},
		},
		{
			name: "rename file in darwin",
			goos: []string{"darwin"},
			initWorkspace: `
-- foo.go --
package foo
`,
			changes: func(t *testing.T, root string) {
				if err := os.Rename(filepath.Join(root, "foo.go"), filepath.Join(root, "bar.go")); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "bar.go", Type: protocol.Created},
				{URI: "foo.go", Type: protocol.Deleted},
			},
		},
		{
			name: "create directory",
			initWorkspace: `
-- foo.go --
package foo
`,
			changes: func(t *testing.T, root string) {
				if err := os.Mkdir(filepath.Join(root, "bar"), 0755); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "bar", Type: protocol.Created},
			},
		},
		{
			name: "delete directory",
			initWorkspace: `
-- foo/bar.go --
package foo
`,
			changes: func(t *testing.T, root string) {
				if err := os.RemoveAll(filepath.Join(root, "foo")); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				// We only assert that the directory deletion event exists,
				// because file system event behavior is inconsistent across
				// platforms when deleting a non-empty directory.
				// e.g. windows-amd64 may only emit a single dir removal event,
				// freebsd-amd64 report dir removal before file removal,
				// linux-amd64 report the reverse order.
				// Therefore, the most reliable and cross-platform compatible
				// signal is the deletion event for the directory itself.
				{URI: "foo", Type: protocol.Deleted},
			},
		},
		{
			name: "rename directory in linux & windows",
			goos: []string{"linux", "windows"},
			initWorkspace: `
-- foo/bar.go --
package foo
`,
			changes: func(t *testing.T, root string) {
				if err := os.Rename(filepath.Join(root, "foo"), filepath.Join(root, "baz")); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "foo", Type: protocol.Deleted},
				{URI: "baz", Type: protocol.Created},
			},
		},
		{
			name: "rename directory in darwin",
			goos: []string{"darwin"},
			initWorkspace: `
-- foo/bar.go --
package foo
`,
			changes: func(t *testing.T, root string) {
				if err := os.Rename(filepath.Join(root, "foo"), filepath.Join(root, "baz")); err != nil {
					t.Fatal(err)
				}
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "baz", Type: protocol.Created},
				{URI: "foo", Type: protocol.Deleted},
			},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.goos) > 0 && !slices.Contains(tt.goos, runtime.GOOS) {
				t.Skipf("skipping on %s", runtime.GOOS)
			}
			t.Parallel()

			root := t.TempDir()
			archive := txtar.Parse([]byte(tt.initWorkspace))
			for _, f := range archive.Files {
				path := filepath.Join(root, f.Name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, f.Data, 0644); err != nil {
					t.Fatal(err)
				}
			}

			matched := 0
			foundAll := make(chan struct{})
			var gots []protocol.FileEvent
			handler := func(events []protocol.FileEvent, err error) {
				if err != nil {
					t.Errorf("error from watcher: %v", err)
				}
				gots = append(gots, events...)
				// This verifies that the list of wanted events is a subsequence of
				// the received events. It confirms not only that all wanted events
				// are present, but also that their relative order is preserved.
				for _, got := range events {
					if matched == len(tt.expectedEvents) {
						break
					}
					want := protocol.FileEvent{
						URI:  protocol.URIFromPath(filepath.Join(root, string(tt.expectedEvents[matched].URI))),
						Type: tt.expectedEvents[matched].Type,
					}
					if want == got {
						matched++
					}
				}
				if matched == len(tt.expectedEvents) {
					close(foundAll)
				}
			}
			w, err := filewatcher.New(50*time.Millisecond, nil, handler)
			if err != nil {
				t.Fatal(err)
			}

			if err := w.WatchDir(root); err != nil {
				t.Fatal(err)
			}

			if tt.changes != nil {
				tt.changes(t, root)
			}

			select {
			case <-foundAll:
			case <-time.After(30 * time.Second):
				if matched < len(tt.expectedEvents) {
					t.Errorf("found %v matching events\nall want: %#v\nall got: %#v", matched, tt.expectedEvents, gots)
				}
			}

			if err := w.Close(); err != nil {
				t.Errorf("failed to close the file watcher: %v", err)
			}
		})
	}
}
