// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package txtar

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	var tests = []struct {
		name   string
		text   string
		parsed *Archive
	}{
		{
			name: "basic LF",
			text: `comment1
comment2
-- file1 --
File 1 text.
-- foo ---
More file 1 text.
-- file 2 --
File 2 text.
-- empty --
-- noNL --
hello world
-- empty filename line --
some content
-- --`,
			parsed: &Archive{
				Comment: []byte("comment1\ncomment2\n"),
				Files: []File{
					{"file1", []byte("File 1 text.\n-- foo ---\nMore file 1 text.\n")},
					{"file 2", []byte("File 2 text.\n")},
					{"empty", []byte{}},
					{"noNL", []byte("hello world\n")},
					{"empty filename line", []byte("some content\n-- --\n")},
				},
				UseCRLF: false,
			},
		},
		{
			name: "basic CRLF",
			text: "comment1\r\n" +
				"comment2\r\n" +
				"-- file1 --\r\n" +
				"File 1 text.\r\n" +
				"-- foo ---\r\n" +
				"More file 1 text.\r\n" +
				"-- file 2 --\r\n" +
				"File 2 text.\r\n" +
				"-- empty --\r\n" +
				"-- noNL --\r\n" +
				"hello world\r\n" +
				"-- empty filename line --\r\n" +
				"some content\r\n" +
				"-- --\r\n",
			parsed: &Archive{
				Comment: []byte("comment1\r\ncomment2\r\n"),
				Files: []File{
					{"file1", []byte("File 1 text.\r\n-- foo ---\r\nMore file 1 text.\r\n")},
					{"file 2", []byte("File 2 text.\r\n")},
					{"empty", []byte{}},
					{"noNL", []byte("hello world\r\n")},
					{"empty filename line", []byte("some content\r\n-- --\r\n")},
				},
				UseCRLF: true,
			},
		},
		{
			name: "mixed",
			text: "comment1\n" +
				"comment2\r\n" +
				"-- file1 --\r\n" +
				"File 1 text.\n" +
				"-- foo ---\r\n" +
				"More file 1 text.\r\n" +
				"-- file 2 --\r\n" +
				"File 2 text.\r\n" +
				"-- file 3 --\r\n" +
				"File 3 text.\r\n" +
				"-- foo ---\r\n" +
				"More file 3 text.\r\n" +
				"-- empty --\r\n" +
				"-- noNL --\r\n" +
				"hello world\r\n" +
				"-- empty filename line --\r\n" +
				"some content\n" +
				"-- --\n",
			parsed: &Archive{
				Comment: []byte("comment1\ncomment2\r\n"),
				Files: []File{
					{"file1", []byte("File 1 text.\n-- foo ---\r\nMore file 1 text.\r\n")},
					{"file 2", []byte("File 2 text.\r\n")},
					{"file 3", []byte("File 3 text.\r\n-- foo ---\r\nMore file 3 text.\r\n")},
					{"empty", []byte{}},
					{"noNL", []byte("hello world\r\n")},
					{"empty filename line", []byte("some content\n-- --\n\r\n")},
				},
				UseCRLF: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := Parse([]byte(tt.text))
			if !reflect.DeepEqual(a, tt.parsed) {
				t.Fatalf("Parse: wrong output:\nhave:\n%s\nwant:\n%s", shortArchive(a), shortArchive(tt.parsed))
			}
			text := Format(a)
			a = Parse(text)
			if !reflect.DeepEqual(a, tt.parsed) {
				t.Fatalf("Parse after Format: wrong output:\nhave:\n%s\nwant:\n%s", shortArchive(a), shortArchive(tt.parsed))
			}
		})
	}
}

func TestFormat(t *testing.T) {
	var tests = []struct {
		name   string
		input  *Archive
		wanted string
	}{
		{
			name: "basic LF",
			input: &Archive{
				Comment: []byte("comment1\ncomment2\n"),
				Files: []File{
					{"file1", []byte("File 1 text.\n-- foo ---\nMore file 1 text.\n")},
					{"file 2", []byte("File 2 text.\n")},
					{"empty", []byte{}},
					{"noNL", []byte("hello world")},
				},
				UseCRLF: false,
			},
			wanted: `comment1
comment2
-- file1 --
File 1 text.
-- foo ---
More file 1 text.
-- file 2 --
File 2 text.
-- empty --
-- noNL --
hello world
`,
		},
		{
			name: "basic CRLF",
			input: &Archive{
				Comment: []byte("comment1\r\ncomment2\r\n"),
				Files: []File{
					{"file1", []byte("File 1 text.\r\n-- foo ---\r\nMore file 1 text.\r\n")},
					{"file 2", []byte("File 2 text.\r\n")},
					{"empty", []byte{}},
					{"noNL", []byte("hello world")},
				},
				UseCRLF: true,
			},
			wanted: "comment1\r\n" +
				"comment2\r\n" +
				"-- file1 --\r\n" +
				"File 1 text.\r\n" +
				"-- foo ---\r\n" +
				"More file 1 text.\r\n" +
				"-- file 2 --\r\n" +
				"File 2 text.\r\n" +
				"-- empty --\r\n" +
				"-- noNL --\r\n" +
				"hello world\r\n",
		},
		{
			name: "mixed",
			input: &Archive{
				Comment: []byte("comment1\ncomment2\r\n"),
				Files: []File{
					{"file1", []byte("File 1 text.\n-- foo ---\r\nMore file 1 text.\r\n")},
					{"file 2", []byte("File 2 text.\r\n")},
					{"file 3", []byte("File 3 text.\r\n-- foo ---\r\nMore file 3 text.\r\n")},
					{"empty", []byte{}},
					{"noNL", []byte("hello world\r\n")},
					{"empty filename line", []byte("some content\r\n-- --\n")},
				},
				UseCRLF: true,
			},
			wanted: "comment1\n" +
				"comment2\r\n" +
				"-- file1 --\r\n" +
				"File 1 text.\n" +
				"-- foo ---\r\n" +
				"More file 1 text.\r\n" +
				"-- file 2 --\r\n" +
				"File 2 text.\r\n" +
				"-- file 3 --\r\n" +
				"File 3 text.\r\n" +
				"-- foo ---\r\n" +
				"More file 3 text.\r\n" +
				"-- empty --\r\n" +
				"-- noNL --\r\n" +
				"hello world\r\n" +
				"-- empty filename line --\r\n" +
				"some content\r\n" +
				"-- --\n\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Format(tt.input)
			if string(result) != tt.wanted {
				t.Errorf("Wrong output. \nGot:\n%s\nWant:\n%s\n", string(result), tt.wanted)
			}
		})
	}
}

func shortArchive(a *Archive) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "comment: %q\n", a.Comment)
	for _, f := range a.Files {
		fmt.Fprintf(&buf, "file %q: %q\n", f.Name, f.Data)
	}
	return buf.String()
}
