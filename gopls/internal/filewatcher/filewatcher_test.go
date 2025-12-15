// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filewatcher_test

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/gopls/internal/filewatcher"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/moremaps"
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
		changes        func(root string) error
		expectedEvents []protocol.FileEvent
	}{
		{
			name: "create file in darwin",
			goos: []string{"darwin"},
			initWorkspace: `
-- foo.go --
package foo
`,
			changes: func(root string) error {
				return os.WriteFile(filepath.Join(root, "bar.go"), []byte("package main"), 0644)
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
			changes: func(root string) error {
				return os.WriteFile(filepath.Join(root, "bar.go"), []byte("package main"), 0644)
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
			changes: func(root string) error {
				return os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main // modified"), 0644)
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
			changes: func(root string) error {
				return os.Remove(filepath.Join(root, "foo.go"))
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
			changes: func(root string) error {
				return os.Rename(filepath.Join(root, "foo.go"), filepath.Join(root, "bar.go"))
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
			changes: func(root string) error {
				return os.Rename(filepath.Join(root, "foo.go"), filepath.Join(root, "bar.go"))
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
			changes: func(root string) error {
				return os.Mkdir(filepath.Join(root, "bar"), 0755)
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
			changes: func(root string) error {
				return os.RemoveAll(filepath.Join(root, "foo"))
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
			changes: func(root string) error {
				return os.Rename(filepath.Join(root, "foo"), filepath.Join(root, "baz"))
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "foo", Type: protocol.Deleted},
				{URI: "baz", Type: protocol.Created},
				{URI: "baz/bar.go", Type: protocol.Created},
			},
		},
		{
			name: "rename directory in darwin",
			goos: []string{"darwin"},
			initWorkspace: `
-- foo/bar.go --
package foo
`,
			changes: func(root string) error {
				return os.Rename(filepath.Join(root, "foo"), filepath.Join(root, "baz"))
			},
			expectedEvents: []protocol.FileEvent{
				{URI: "baz", Type: protocol.Created},
				{URI: "baz/bar.go", Type: protocol.Created},
				{URI: "foo", Type: protocol.Deleted},
			},
		},
		// TOOD(hxjiang): test for symlink to a dir.
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.goos) > 0 && !slices.Contains(tt.goos, runtime.GOOS) {
				t.Skipf("skipping on %s", runtime.GOOS)
			}

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

			foundAll := make(chan struct{})
			var gots []protocol.FileEvent

			matched := 0
			eventsHandler := func(events []protocol.FileEvent) {
				gots = append(gots, events...)

				if matched == len(tt.expectedEvents) {
					return
				}

				// This verifies that the list of wanted events is a subsequence of
				// the received events. It confirms not only that all wanted events
				// are present, but also that their relative order is preserved.
				for _, got := range events {
					want := protocol.FileEvent{
						URI:  protocol.URIFromPath(filepath.Join(root, string(tt.expectedEvents[matched].URI))),
						Type: tt.expectedEvents[matched].Type,
					}
					if want == got {
						matched++
					}
					if matched == len(tt.expectedEvents) {
						close(foundAll)
						return
					}
				}

			}
			errHandler := func(err error) {
				t.Errorf("error from watcher: %v", err)
			}
			w, err := filewatcher.New(50*time.Millisecond, nil, eventsHandler, errHandler)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := w.Close(); err != nil {
					t.Errorf("failed to close the file watcher: %v", err)
				}
			}()

			if err := w.WatchDir(root); err != nil {
				t.Fatal(err)
			}

			if tt.changes != nil {
				if err := tt.changes(root); err != nil {
					t.Fatal(err)
				}
			}

			select {
			case <-foundAll:
			case <-time.After(30 * time.Second):
				if matched != len(tt.expectedEvents) {
					var want strings.Builder
					for _, e := range tt.expectedEvents {
						want.WriteString(fmt.Sprintf("URI: %s type: %v\n", e.URI, e.Type))
					}
					var got strings.Builder
					for _, e := range gots {
						got.WriteString(fmt.Sprintf("URI: %s type: %v\n", strings.TrimPrefix(e.URI.Path(), root+"/"), e.Type))
					}
					t.Errorf("found %v matching events slice\nwant sequences:\n%s\nall got:\n%s", matched, want.String(), got.String())
				}

			}
		})
	}
}

func TestBrokenSymlink(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("unsupported OS")
	}

	root := t.TempDir()

	// watchErrs is used to capture watch errors during directory monitoring.
	// This mechanism allows the test to assert that specific directory watches
	// initially fail and subsequently recover upon fixing the broken symlink.
	watchErrs := make(chan error, 10)
	filewatcher.SetAfterAddHook(func(path string, watchErr error) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return
		}
		if rel == "foo" {
			if watchErr == nil {
				close(watchErrs)
			} else {
				watchErrs <- watchErr
			}
		}
	})
	defer filewatcher.SetAfterAddHook(nil)

	var (
		gots     []protocol.FileEvent
		matched  int
		foundAll = make(chan struct{})
	)
	wants := []protocol.FileEvent{
		// "foo" create event from fsnotify and synthesized create event
		// for all entries under foo.
		{URI: "foo", Type: protocol.Created},
		{URI: "foo/a.go", Type: protocol.Created},
		{URI: "foo/b.go", Type: protocol.Created},
		{URI: "foo/from.go", Type: protocol.Created},
		// "to.go" creation from fsnotify.
		{URI: "to.go", Type: protocol.Created},
		// file creation event after watch retry succeeded.
		{URI: "foo/new.go", Type: protocol.Created},
	}
	eventsHandler := func(events []protocol.FileEvent) {
		gots = append(gots, events...)

		if matched == len(wants) {
			return
		}

		for _, got := range events {
			want := protocol.FileEvent{
				URI:  protocol.URIFromPath(filepath.Join(root, string(wants[matched].URI))),
				Type: wants[matched].Type,
			}
			if want == got {
				matched++
			}
			if matched == len(wants) {
				close(foundAll)
				return
			}
		}

	}
	errHandler := func(err error) {
		t.Errorf("error from watcher: %v", err)
	}
	w, err := filewatcher.New(50*time.Millisecond, nil, eventsHandler, errHandler)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("failed to close the file watcher: %v", err)
		}
	}()

	if err := w.WatchDir(root); err != nil {
		t.Fatal(err)
	}

	{
		// Prepare a dir with with broken symbolic link.
		// foo                       <- 1st
		// ├── from.go -> root/to.go <- 1st
		// ├── a.go                  <- 1st
		// └── b.go                  <- 1st

		to := filepath.Join(root, "to.go")

		archive := txtar.Parse([]byte(`
-- a.go --
package a
-- b.go --
package b
`))
		tmp := filepath.Join(t.TempDir(), "foo")
		for _, f := range archive.Files {
			path := filepath.Join(tmp, f.Name)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatalf("fail to create dir %v", err)
			}
			if err := os.WriteFile(path, f.Data, 0644); err != nil {
				t.Fatalf("fail to write file %v", err)
			}
		}

		// Create the symbolic link to a non-existing file. This would
		// cause the watch registration for dir "foo" to fail.
		if err := os.Symlink(to, filepath.Join(tmp, "from.go")); err != nil {
			t.Fatalf("fail to create symlink file %v", err)
		}

		// Move the directory containing the broken symlink into place
		// to avoids a flaky test where the directory could be watched
		// before the symlink is created. See golang/go#74782.
		if err := os.Rename(tmp, filepath.Join(root, "foo")); err != nil {
			t.Fatalf("fail to rename file %v", err)
		}

		// root
		// ├── foo                          <- 2nd (Move)
		// │   ├── a.go                     <- 2nd (Move)
		// │   ├── b.go                     <- 2nd (Move)
		// │   ├── from.go -> ../../to.go   <- 2nd (Move)
		// │   └── new.go                   <- 4th (Create)
		// └── to.go                        <- 3rd (Create)

		// Should be able to capture watch error while trying to watch dir "foo".
		if err := <-watchErrs; err == nil {
			t.Errorf("did not capture watch registration failure for dir foo")
		}

		// The file watcher should retry watch registration and eventually succeed
		// watching for all dir under 'foo' after the file got created.
		{
			if err := os.WriteFile(to, []byte("package main"), 0644); err != nil {
				t.Errorf("fail to write file %v", err)
			}

			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()

		outer:
			for {
				select {
				case _, ok := <-watchErrs:
					if !ok {
						break outer
					}
				case <-timer.C:
					t.Errorf("timed out after 30s waiting for watches on foo to be established")
				}
			}
		}

		// Once the watch registration is done, file events under the
		// dir "foo" should be captured
		if err := os.WriteFile(filepath.Join(root, "foo", "new.go"), []byte("package main"), 0644); err != nil {
			t.Fatalf("fail to write file %v", err)
		}
	}

	select {
	case <-foundAll:
	case <-time.After(30 * time.Second):
		if matched != len(wants) {
			var want strings.Builder
			for _, e := range wants {
				want.WriteString(fmt.Sprintf("URI: %s type: %v\n", e.URI, e.Type))
			}
			var got strings.Builder
			for _, e := range gots {
				got.WriteString(fmt.Sprintf("URI: %s type: %v\n", strings.TrimPrefix(e.URI.Path(), root+"/"), e.Type))
			}
			t.Errorf("found %v matching events slice\nwant sequences:\n%s\nall got:\n%s", matched, want.String(), got.String())
		}

	}
}

func TestStress(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("unsupported OS")
	}

	const (
		delay       = 50 * time.Millisecond
		parallelism = 100 // number of parallel instances of each kind of operation
	)

	root := t.TempDir()

	mkdir := func(base string) func() error {
		return func() error {
			return os.Mkdir(filepath.Join(root, base), 0755)
		}
	}
	write := func(base string) func() error {
		return func() error {
			return os.WriteFile(filepath.Join(root, base), []byte("package main"), 0644)
		}
	}
	remove := func(base string) func() error {
		return func() error {
			return os.Remove(filepath.Join(root, base))
		}
	}
	rename := func(old, new string) func() error {
		return func() error {
			return os.Rename(filepath.Join(root, old), filepath.Join(root, new))
		}
	}

	wants := make(map[protocol.FileEvent]bool)
	want := func(base string, t protocol.FileChangeType) {
		wants[protocol.FileEvent{URI: protocol.URIFromPath(filepath.Join(root, base)), Type: t}] = true
	}

	for i := range parallelism {
		// Create files and dirs that will be deleted or renamed later.
		if err := cmp.Or(
			mkdir(fmt.Sprintf("delete-dir-%d", i))(),
			mkdir(fmt.Sprintf("old-dir-%d", i))(),
			write(fmt.Sprintf("delete-file-%d.go", i))(),
			write(fmt.Sprintf("old-file-%d.go", i))(),
		); err != nil {
			t.Fatal(err)
		}

		// Add expected notification events to the "wants" set.
		want(fmt.Sprintf("file-%d.go", i), protocol.Created)
		want(fmt.Sprintf("delete-file-%d.go", i), protocol.Deleted)
		want(fmt.Sprintf("old-file-%d.go", i), protocol.Deleted)
		want(fmt.Sprintf("new-file-%d.go", i), protocol.Created)
		want(fmt.Sprintf("dir-%d", i), protocol.Created)
		want(fmt.Sprintf("delete-dir-%d", i), protocol.Deleted)
		want(fmt.Sprintf("old-dir-%d", i), protocol.Deleted)
		want(fmt.Sprintf("new-dir-%d", i), protocol.Created)
	}

	foundAll := make(chan struct{})

	eventsHandler := func(events []protocol.FileEvent) {
		if len(wants) == 0 { // avoid closing twice
			return
		}
		for _, e := range events {
			delete(wants, e)
		}
		if len(wants) == 0 {
			close(foundAll)
		}
	}
	errHandler := func(err error) {
		t.Errorf("error from watcher: %v", err)
	}
	w, err := filewatcher.New(delay, nil, eventsHandler, errHandler)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("failed to close the file watcher: %v", err)
		}
	}()

	if err := w.WatchDir(root); err != nil {
		t.Fatal(err)
	}

	// Spin up multiple goroutines, to perform 6 file system operations i.e.
	// create, delete, rename of file or directory. For deletion and rename,
	// the goroutine deletes / renames files or directories created before the
	// watcher starts.
	var g errgroup.Group
	for id := range parallelism {
		ops := []func() error{
			write(fmt.Sprintf("file-%d.go", id)),
			remove(fmt.Sprintf("delete-file-%d.go", id)),
			rename(fmt.Sprintf("old-file-%d.go", id), fmt.Sprintf("new-file-%d.go", id)),
			mkdir(fmt.Sprintf("dir-%d", id)),
			remove(fmt.Sprintf("delete-dir-%d", id)),
			rename(fmt.Sprintf("old-dir-%d", id), fmt.Sprintf("new-dir-%d", id)),
		}
		for _, f := range ops {
			g.Go(f)
		}
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-foundAll:
	case <-time.After(30 * time.Second):
		if len(wants) > 0 {
			t.Errorf("missing expected events: %#v", moremaps.KeySlice(wants))
		}
	}
}
