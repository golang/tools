// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
)

func TestFileMap(t *testing.T) {
	const (
		set = iota
		del
	)
	type op struct {
		op      int // set or remove
		path    string
		overlay bool
	}
	tests := []struct {
		label        string
		ops          []op
		wantFiles    []string
		wantOverlays []string
		wantDirs     []string
	}{
		{"empty", nil, nil, nil, nil},
		{"singleton", []op{
			{set, "/a/b", false},
		}, []string{"/a/b"}, nil, []string{"/", "/a"}},
		{"overlay", []op{
			{set, "/a/b", true},
		}, []string{"/a/b"}, []string{"/a/b"}, []string{"/", "/a"}},
		{"replace overlay", []op{
			{set, "/a/b", true},
			{set, "/a/b", false},
		}, []string{"/a/b"}, nil, []string{"/", "/a"}},
		{"multi dir", []op{
			{set, "/a/b", false},
			{set, "/c/d", false},
		}, []string{"/a/b", "/c/d"}, nil, []string{"/", "/a", "/c"}},
		{"empty dir", []op{
			{set, "/a/b", false},
			{set, "/c/d", false},
			{del, "/a/b", false},
		}, []string{"/c/d"}, nil, []string{"/", "/c"}},
	}

	// Normalize paths for windows compatibility.
	normalize := func(path string) string {
		y := filepath.ToSlash(path)
		// Windows paths may start with a drive letter
		if len(y) > 2 && y[1] == ':' && y[0] >= 'A' && y[0] <= 'Z' {
			y = y[2:]
		}
		return y
	}

	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			m := newFileMap()
			for _, op := range test.ops {
				uri := protocol.URIFromPath(filepath.FromSlash(op.path))
				switch op.op {
				case set:
					var fh file.Handle
					if op.overlay {
						fh = &overlay{uri: uri}
					} else {
						fh = &diskFile{uri: uri}
					}
					m.set(uri, fh)
				case del:
					m.delete(uri)
				}
			}

			var gotFiles []string
			for uri := range m.all() {
				gotFiles = append(gotFiles, normalize(uri.Path()))
			}
			sort.Strings(gotFiles)
			if diff := cmp.Diff(test.wantFiles, gotFiles); diff != "" {
				t.Errorf("Files mismatch (-want +got):\n%s", diff)
			}

			var gotOverlays []string
			for _, o := range m.getOverlays() {
				gotOverlays = append(gotOverlays, normalize(o.URI().Path()))
			}
			if diff := cmp.Diff(test.wantOverlays, gotOverlays); diff != "" {
				t.Errorf("Overlays mismatch (-want +got):\n%s", diff)
			}

			var gotDirs []string
			for dir := range m.getDirs().All() {
				gotDirs = append(gotDirs, normalize(dir))
			}
			sort.Strings(gotDirs)
			if diff := cmp.Diff(test.wantDirs, gotDirs); diff != "" {
				t.Errorf("Dirs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
