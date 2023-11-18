// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows
// +build !windows

package protocol_test

import (
	"testing"

	"golang.org/x/tools/gopls/internal/lsp/protocol"
)

// TestURIFromPath tests the conversion between URIs and filenames. The test cases
// include Windows-style URIs and filepaths, but we avoid having OS-specific
// tests by using only forward slashes, assuming that the standard library
// functions filepath.ToSlash and filepath.FromSlash do not need testing.
func TestURIFromPath(t *testing.T) {
	for _, test := range []struct {
		path, wantFile string
		wantURI        protocol.DocumentURI
	}{
		{
			path:     ``,
			wantFile: ``,
			wantURI:  protocol.DocumentURI(""),
		},
		{
			path:     `C:/Windows/System32`,
			wantFile: `C:/Windows/System32`,
			wantURI:  protocol.DocumentURI("file:///C:/Windows/System32"),
		},
		{
			path:     `C:/Go/src/bob.go`,
			wantFile: `C:/Go/src/bob.go`,
			wantURI:  protocol.DocumentURI("file:///C:/Go/src/bob.go"),
		},
		{
			path:     `c:/Go/src/bob.go`,
			wantFile: `C:/Go/src/bob.go`,
			wantURI:  protocol.DocumentURI("file:///C:/Go/src/bob.go"),
		},
		{
			path:     `/path/to/dir`,
			wantFile: `/path/to/dir`,
			wantURI:  protocol.DocumentURI("file:///path/to/dir"),
		},
		{
			path:     `/a/b/c/src/bob.go`,
			wantFile: `/a/b/c/src/bob.go`,
			wantURI:  protocol.DocumentURI("file:///a/b/c/src/bob.go"),
		},
		{
			path:     `c:/Go/src/bob george/george/george.go`,
			wantFile: `C:/Go/src/bob george/george/george.go`,
			wantURI:  protocol.DocumentURI("file:///C:/Go/src/bob%20george/george/george.go"),
		},
	} {
		got := protocol.URIFromPath(test.path)
		if got != test.wantURI {
			t.Errorf("URIFromPath(%q): got %q, expected %q", test.path, got, test.wantURI)
		}
		gotFilename := got.Path()
		if gotFilename != test.wantFile {
			t.Errorf("Filename(%q): got %q, expected %q", got, gotFilename, test.wantFile)
		}
	}
}

func TestParseDocumentURI(t *testing.T) {
	for _, test := range []struct {
		input    string
		want     string // string(DocumentURI) on success or error.Error() on failure
		wantPath string // expected DocumentURI.Path on success
	}{
		{
			input:    `file:///c:/Go/src/bob%20george/george/george.go`,
			want:     "file:///C:/Go/src/bob%20george/george/george.go",
			wantPath: `C:/Go/src/bob george/george/george.go`,
		},
		{
			input:    `file:///C%3A/Go/src/bob%20george/george/george.go`,
			want:     "file:///C:/Go/src/bob%20george/george/george.go",
			wantPath: `C:/Go/src/bob george/george/george.go`,
		},
		{
			input:    `file:///path/to/%25p%25ercent%25/per%25cent.go`,
			want:     `file:///path/to/%25p%25ercent%25/per%25cent.go`,
			wantPath: `/path/to/%p%ercent%/per%cent.go`,
		},
		{
			input:    `file:///C%3A/`,
			want:     `file:///C:/`,
			wantPath: `C:/`,
		},
		{
			input:    `file:///`,
			want:     `file:///`,
			wantPath: `/`,
		},
		{
			input:    `file://wsl%24/Ubuntu/home/wdcui/repo/VMEnclaves/cvm-runtime`,
			want:     `file:///wsl$/Ubuntu/home/wdcui/repo/VMEnclaves/cvm-runtime`,
			wantPath: `/wsl$/Ubuntu/home/wdcui/repo/VMEnclaves/cvm-runtime`,
		},
		{
			input:    "",
			want:     "",
			wantPath: "",
		},
		// Errors:
		{
			input: "https://go.dev/",
			want:  "DocumentURI scheme is not 'file': https://go.dev/",
		},
	} {
		uri, err := protocol.ParseDocumentURI(test.input)
		var got string
		if err != nil {
			got = err.Error()
		} else {
			got = string(uri)
		}
		if got != test.want {
			t.Errorf("ParseDocumentURI(%q): got %q, want %q", test.input, got, test.want)
		}
		if err == nil && uri.Path() != test.wantPath {
			t.Errorf("DocumentURI(%s).Path = %q, want %q", uri,
				uri.Path(), test.wantPath)
		}
	}
}
