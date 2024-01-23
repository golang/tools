// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

func TestZeroConfigAlgorithm(t *testing.T) {
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
							"GOPATH": dir,
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
					results := settings.SetOptions(opts, f.options(dir))
					for _, r := range results {
						if r.Error != nil {
							t.Fatalf("setting option %v: %v", r.Name, r.Error)
						}
					}
				}
				env, err := FetchGoEnv(ctx, toURI(f.dir), opts)
				if err != nil {
					t.Fatalf("fetching env: %v", env)
				}
				folders = append(folders, &Folder{
					Dir:     toURI(f.dir),
					Name:    path.Base(f.dir),
					Options: opts,
					Env:     env,
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
