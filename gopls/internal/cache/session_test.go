// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
	"golang.org/x/tools/internal/testenv"
)

func TestZeroConfigAlgorithm(t *testing.T) {
	testenv.NeedsExec(t) // executes the Go command
	t.Setenv("GOPACKAGESDRIVER", "off")

	type viewSummary struct {
		// fields exported for cmp.Diff
		Type ViewType
		Root string
		Env  []string
	}

	type folderSummary struct {
		dir     string
		options func(dir string) map[string]any // options may refer to the temp dir
	}

	includeReplaceInWorkspace := func(string) map[string]any {
		return map[string]any{
			"includeReplaceInWorkspace": true,
		}
	}

	type test struct {
		name    string
		files   map[string]string // use a map rather than txtar as file content is tiny
		folders []folderSummary
		open    []string // open files
		want    []viewSummary
	}

	tests := []test{
		// TODO(rfindley): add a test for GOPACKAGESDRIVER.
		// Doing so doesn't yet work using options alone (user env is not honored)

		// TODO(rfindley): add a test for degenerate cases, such as missing
		// workspace folders (once we decide on the correct behavior).
		{
			"basic go.work workspace",
			map[string]string{
				"go.work":  "go 1.18\nuse (\n\t./a\n\t./b\n)\n",
				"a/go.mod": "module golang.org/a\ngo 1.18\n",
				"b/go.mod": "module golang.org/b\ngo 1.18\n",
			},
			[]folderSummary{{dir: "."}},
			nil,
			[]viewSummary{{GoWorkView, ".", nil}},
		},
		{
			"basic go.mod workspace",
			map[string]string{
				"go.mod": "module golang.org/a\ngo 1.18\n",
			},
			[]folderSummary{{dir: "."}},
			nil,
			[]viewSummary{{GoModView, ".", nil}},
		},
		{
			"basic GOPATH workspace",
			map[string]string{
				"src/golang.org/a/a.go": "package a",
				"src/golang.org/b/b.go": "package b",
			},
			[]folderSummary{{
				dir: "src",
				options: func(dir string) map[string]any {
					return map[string]any{
						"env": map[string]any{
							"GO111MODULE": "", // golang/go#70196: must be unset
							"GOPATH":      dir,
						},
					}
				},
			}},
			[]string{"src/golang.org/a//a.go", "src/golang.org/b/b.go"},
			[]viewSummary{{GOPATHView, "src", nil}},
		},
		{
			"basic AdHoc workspace",
			map[string]string{
				"foo.go": "package foo",
			},
			[]folderSummary{{dir: "."}},
			nil,
			[]viewSummary{{AdHocView, ".", nil}},
		},
		{
			"multi-folder workspace",
			map[string]string{
				"a/go.mod": "module golang.org/a\ngo 1.18\n",
				"b/go.mod": "module golang.org/b\ngo 1.18\n",
			},
			[]folderSummary{{dir: "a"}, {dir: "b"}},
			nil,
			[]viewSummary{{GoModView, "a", nil}, {GoModView, "b", nil}},
		},
		{
			"multi-module workspace",
			map[string]string{
				"a/go.mod": "module golang.org/a\ngo 1.18\n",
				"b/go.mod": "module golang.org/b\ngo 1.18\n",
			},
			[]folderSummary{{dir: "."}},
			nil,
			[]viewSummary{{AdHocView, ".", nil}},
		},
		{
			"zero-config open module",
			map[string]string{
				"a/go.mod": "module golang.org/a\ngo 1.18\n",
				"a/a.go":   "package a",
				"b/go.mod": "module golang.org/b\ngo 1.18\n",
				"b/b.go":   "package b",
			},
			[]folderSummary{{dir: "."}},
			[]string{"a/a.go"},
			[]viewSummary{
				{AdHocView, ".", nil},
				{GoModView, "a", nil},
			},
		},
		{
			"zero-config open modules",
			map[string]string{
				"a/go.mod": "module golang.org/a\ngo 1.18\n",
				"a/a.go":   "package a",
				"b/go.mod": "module golang.org/b\ngo 1.18\n",
				"b/b.go":   "package b",
			},
			[]folderSummary{{dir: "."}},
			[]string{"a/a.go", "b/b.go"},
			[]viewSummary{
				{AdHocView, ".", nil},
				{GoModView, "a", nil},
				{GoModView, "b", nil},
			},
		},
		{
			"unified workspace",
			map[string]string{
				"go.work":  "go 1.18\nuse (\n\t./a\n\t./b\n)\n",
				"a/go.mod": "module golang.org/a\ngo 1.18\n",
				"a/a.go":   "package a",
				"b/go.mod": "module golang.org/b\ngo 1.18\n",
				"b/b.go":   "package b",
			},
			[]folderSummary{{dir: "."}},
			[]string{"a/a.go", "b/b.go"},
			[]viewSummary{{GoWorkView, ".", nil}},
		},
		{
			"go.work from env",
			map[string]string{
				"nested/go.work": "go 1.18\nuse (\n\t../a\n\t../b\n)\n",
				"a/go.mod":       "module golang.org/a\ngo 1.18\n",
				"a/a.go":         "package a",
				"b/go.mod":       "module golang.org/b\ngo 1.18\n",
				"b/b.go":         "package b",
			},
			[]folderSummary{{
				dir: ".",
				options: func(dir string) map[string]any {
					return map[string]any{
						"env": map[string]any{
							"GOWORK": filepath.Join(dir, "nested", "go.work"),
						},
					}
				},
			}},
			[]string{"a/a.go", "b/b.go"},
			[]viewSummary{{GoWorkView, ".", nil}},
		},
		{
			"independent module view",
			map[string]string{
				"go.work":  "go 1.18\nuse (\n\t./a\n)\n", // not using b
				"a/go.mod": "module golang.org/a\ngo 1.18\n",
				"a/a.go":   "package a",
				"b/go.mod": "module golang.org/a\ngo 1.18\n",
				"b/b.go":   "package b",
			},
			[]folderSummary{{dir: "."}},
			[]string{"a/a.go", "b/b.go"},
			[]viewSummary{
				{GoWorkView, ".", nil},
				{GoModView, "b", []string{"GOWORK=off"}},
			},
		},
		{
			"multiple go.work",
			map[string]string{
				"go.work":    "go 1.18\nuse (\n\t./a\n\t./b\n)\n",
				"a/go.mod":   "module golang.org/a\ngo 1.18\n",
				"a/a.go":     "package a",
				"b/go.work":  "go 1.18\nuse (\n\t.\n\t./c\n)\n",
				"b/go.mod":   "module golang.org/b\ngo 1.18\n",
				"b/b.go":     "package b",
				"b/c/go.mod": "module golang.org/c\ngo 1.18\n",
			},
			[]folderSummary{{dir: "."}},
			[]string{"a/a.go", "b/b.go", "b/c/c.go"},
			[]viewSummary{{GoWorkView, ".", nil}, {GoWorkView, "b", nil}},
		},
		{
			"multiple go.work, c unused",
			map[string]string{
				"go.work":    "go 1.18\nuse (\n\t./a\n\t./b\n)\n",
				"a/go.mod":   "module golang.org/a\ngo 1.18\n",
				"a/a.go":     "package a",
				"b/go.work":  "go 1.18\nuse (\n\t.\n)\n",
				"b/go.mod":   "module golang.org/b\ngo 1.18\n",
				"b/b.go":     "package b",
				"b/c/go.mod": "module golang.org/c\ngo 1.18\n",
			},
			[]folderSummary{{dir: "."}},
			[]string{"a/a.go", "b/b.go", "b/c/c.go"},
			[]viewSummary{{GoWorkView, ".", nil}, {GoModView, "b/c", []string{"GOWORK=off"}}},
		},
		{
			"go.mod with nested replace",
			map[string]string{
				"go.mod":   "module golang.org/a\n require golang.org/b v1.2.3\nreplace example.com/b => ./b",
				"a.go":     "package a",
				"b/go.mod": "module golang.org/b\ngo 1.18\n",
				"b/b.go":   "package b",
			},
			[]folderSummary{{dir: ".", options: includeReplaceInWorkspace}},
			[]string{"a/a.go", "b/b.go"},
			[]viewSummary{{GoModView, ".", nil}},
		},
		{
			"go.mod with parent replace, parent folder",
			map[string]string{
				"go.mod":   "module golang.org/a",
				"a.go":     "package a",
				"b/go.mod": "module golang.org/b\ngo 1.18\nrequire golang.org/a v1.2.3\nreplace golang.org/a => ../",
				"b/b.go":   "package b",
			},
			[]folderSummary{{dir: ".", options: includeReplaceInWorkspace}},
			[]string{"a/a.go", "b/b.go"},
			[]viewSummary{{GoModView, ".", nil}, {GoModView, "b", nil}},
		},
		{
			"go.mod with multiple replace",
			map[string]string{
				"go.mod": `
module golang.org/root

require (
	golang.org/a v1.2.3
	golang.org/b v1.2.3
	golang.org/c v1.2.3
)

replace (
	golang.org/b => ./b
	golang.org/c => ./c
	// Note: d is not replaced
)
`,
				"a.go":     "package a",
				"b/go.mod": "module golang.org/b\ngo 1.18",
				"b/b.go":   "package b",
				"c/go.mod": "module golang.org/c\ngo 1.18",
				"c/c.go":   "package c",
				"d/go.mod": "module golang.org/d\ngo 1.18",
				"d/d.go":   "package d",
			},
			[]folderSummary{{dir: ".", options: includeReplaceInWorkspace}},
			[]string{"b/b.go", "c/c.go", "d/d.go"},
			[]viewSummary{{GoModView, ".", nil}, {GoModView, "d", nil}},
		},
		{
			"go.mod with replace outside the workspace",
			map[string]string{
				"go.mod":   "module golang.org/a\ngo 1.18",
				"a.go":     "package a",
				"b/go.mod": "module golang.org/b\ngo 1.18\nrequire golang.org/a v1.2.3\nreplace golang.org/a => ../",
				"b/b.go":   "package b",
			},
			[]folderSummary{{dir: "b"}},
			[]string{"a.go", "b/b.go"},
			[]viewSummary{{GoModView, "b", nil}},
		},
		{
			"go.mod with replace directive; workspace replace off",
			map[string]string{
				"go.mod":   "module golang.org/a\n require golang.org/b v1.2.3\nreplace example.com/b => ./b",
				"a.go":     "package a",
				"b/go.mod": "module golang.org/b\ngo 1.18\n",
				"b/b.go":   "package b",
			},
			[]folderSummary{{
				dir: ".",
				options: func(string) map[string]any {
					return map[string]any{
						"includeReplaceInWorkspace": false,
					}
				},
			}},
			[]string{"a/a.go", "b/b.go"},
			[]viewSummary{{GoModView, ".", nil}, {GoModView, "b", nil}},
		},
	}

	for _, test := range tests {
		ctx := context.Background()
		t.Run(test.name, func(t *testing.T) {
			dir := writeFiles(t, test.files)
			rel := fake.RelativeTo(dir)
			fs := newMemoizedFS()

			toURI := func(path string) protocol.DocumentURI {
				return protocol.URIFromPath(rel.AbsPath(path))
			}

			var folders []*Folder
			for _, f := range test.folders {
				opts := settings.DefaultOptions()
				if f.options != nil {
					_, errs := opts.Set(f.options(dir))
					for _, err := range errs {
						t.Fatal(err)
					}
				}
				uri := toURI(f.dir)
				env, err := FetchGoEnv(ctx, uri, opts)
				if err != nil {
					t.Fatalf("FetchGoEnv failed: %v", err)
				}
				t.Logf("FetchGoEnv(%q) = %+v", uri, env)
				folders = append(folders, &Folder{
					Dir:     uri,
					Name:    path.Base(f.dir),
					Options: opts,
					Env:     *env,
				})
			}

			var openFiles []protocol.DocumentURI
			for _, path := range test.open {
				openFiles = append(openFiles, toURI(path))
			}

			defs, err := selectViewDefs(ctx, fs, folders, openFiles)
			if err != nil {
				t.Fatal(err)
			}
			var got []viewSummary
			for _, def := range defs {
				got = append(got, viewSummary{
					Type: def.Type(),
					Root: rel.RelPath(def.root.Path()),
					Env:  def.EnvOverlay(),
				})
			}
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("selectViews() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TODO(rfindley): this function could be meaningfully factored with the
// various other test helpers of this nature.
func writeFiles(t *testing.T, files map[string]string) string {
	root := t.TempDir()

	// This unfortunate step is required because gopls output
	// expands symbolic links in its input file names (arguably it
	// should not), and on macOS the temp dir is in /var -> private/var.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	for name, content := range files {
		filename := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0777); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0666); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestOrphanedFileDiagnostics_Shutdown verifies that session operations,
// particularly OrphanedFileDiagnostics and SnapshotOf, fail cleanly with
// ErrSessionShutdown when the session shuts down, including when views and
// overlays are present. This prevents race conditions where in-flight
// background diagnostics emit false-positive warnings during shutdown.
func TestOrphanedFileDiagnostics_Shutdown(t *testing.T) {
	testenv.NeedsExec(t)
	t.Setenv("GOPACKAGESDRIVER", "off")

	// The module is in "a". b/b.go is not in the module, so it is orphaned.
	files := map[string]string{
		"a/go.mod": "module example.com/a\ngo 1.18\n",
		"a/a.go":   "package a\n",
		"b/b.go":   "package b\n",
	}
	dir := writeFiles(t, files)
	toURI := func(name string) protocol.DocumentURI {
		return protocol.URIFromPath(filepath.Join(dir, name))
	}

	ctx := t.Context()
	session := NewSession(ctx, New(nil))

	opts := settings.DefaultOptions()
	env, err := FetchGoEnv(ctx, toURI("a"), opts)
	if err != nil {
		t.Fatal(err)
	}
	folder := &Folder{Dir: toURI("a"), Name: "a", Options: opts, Env: *env}
	_, _, release, err := session.NewView(ctx, folder)
	if err != nil {
		t.Fatal(err)
	}
	release()

	// Open a file in the module and the orphaned file. For the orphaned file,
	// no view has a real package, so SnapshotOf uses its fallback path.
	var mods []file.Modification
	for _, name := range []string{"a/a.go", "b/b.go"} {
		mods = append(mods, file.Modification{
			URI:        toURI(name),
			Action:     file.Open,
			Version:    1,
			Text:       []byte(files[name]),
			LanguageID: "go",
		})
	}
	if _, err := session.DidModifyFiles(ctx, mods); err != nil {
		t.Fatal(err)
	}

	// Make sure that b/b.go is orphaned before shutdown.
	bURI := toURI("b/b.go")
	diags, err := session.OrphanedFileDiagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(diags[bURI]) == 0 {
		t.Fatalf("OrphanedFileDiagnostics before Shutdown: no diagnostics for %s, want at least one", bURI)
	}

	snapshotOf := func(uri protocol.DocumentURI) error {
		_, release, err := session.SnapshotOf(ctx, uri)
		if err == nil {
			release()
		}
		return err
	}

	// Call OrphanedFileDiagnostics and SnapshotOf repeatedly while
	// Shutdown runs, as background diagnostics do when the client exits.
	// Each call must succeed or return ErrSessionShutdown.
	//
	// This is a smoke test for data races (run with -race). It does not
	// reliably hit the narrow race where SnapshotOf gets a list of views that
	// is not empty, and then each view shuts down before it gives a snapshot.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := session.OrphanedFileDiagnostics(ctx); err != nil && !errors.Is(err, ErrSessionShutdown) {
				t.Errorf("concurrent OrphanedFileDiagnostics returned %v, want nil or %v", err, ErrSessionShutdown)
			}
			if err := snapshotOf(bURI); err != nil && !errors.Is(err, ErrSessionShutdown) {
				t.Errorf("concurrent SnapshotOf returned %v, want nil or %v", err, ErrSessionShutdown)
			}
		}
	})

	session.Shutdown(ctx)
	close(stop)
	wg.Wait()

	// After Shutdown, all session operations must return ErrSessionShutdown.
	for _, check := range []struct {
		name string
		f    func() error
	}{
		{"OrphanedFileDiagnostics", func() error { _, err := session.OrphanedFileDiagnostics(ctx); return err }},
		{"SnapshotOf", func() error { return snapshotOf(bURI) }},
		{"NewView", func() error { _, _, _, err := session.NewView(ctx, folder); return err }},
		{"ResetView", func() error { _, err := session.ResetView(ctx, toURI("a")); return err }},
		{"DidModifyFiles", func() error { _, err := session.DidModifyFiles(ctx, nil); return err }},
	} {
		if err := check.f(); !errors.Is(err, ErrSessionShutdown) {
			t.Errorf("%s after Shutdown returned %v, want %v", check.name, err, ErrSessionShutdown)
		}
	}
}
