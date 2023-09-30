// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package txtar implements a trivial text-based file archive format.
//
// The goals for the format are:
//
//   - be trivial enough to create and edit by hand.
//   - be able to store trees of text files describing go command test cases.
//   - diff nicely in git history and code reviews.
//
// Non-goals include being a completely general archive format,
// storing binary data, storing file modes, storing special files like
// symbolic links, and so on.
//
// # Txtar format
//
// A txtar archive is zero or more comment lines and then a sequence of file entries.
// Each file entry begins with a file marker line of the form "-- FILENAME --"
// and is followed by zero or more file content lines making up the file data.
// The comment or file content ends at the next file marker line.
// The file marker line must begin with the three-byte sequence "-- "
// and end with the three-byte sequence " --", but the enclosed
// file name can be surrounding by additional white space,
// all of which is stripped.
//
// If the txtar file is missing a trailing newline on the final line,
// parsers should consider a final newline to be present anyway.
//
// There are no possible syntax errors in a txtar archive.
package txtar

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// An Archive is a collection of files.
type Archive struct {
	Comment []byte
	Files   []File
}

// A File is a single file in an archive.
type File struct {
	Name string // name of file ("foo/bar.txt")
	Data []byte // text content of file
}

// Format returns the serialized form of an Archive.
// It is assumed that the Archive data structure is well-formed:
// a.Comment and all a.File[i].Data contain no file marker lines,
// and all a.File[i].Name is non-empty. Format uses line separators
// based on the line separator encountered in the comment section.
func Format(a *Archive) []byte {
	firstSep, ok := lineEnd(a.Comment)
	if !ok {
		for _, f := range a.Files {
			firstSep, ok = lineEnd(f.Data)
			if ok {
				break
			}
		}
	}

	var buf bytes.Buffer
	buf.Write(fixNL(a.Comment, firstSep))
	for _, f := range a.Files {
		fmt.Fprintf(&buf, "-- %s --%s", f.Name, firstSep)
		buf.Write(fixNL(f.Data, firstSep))
	}
	return buf.Bytes()
}

// lineEnd returns the line separator that was used at the end
// of the passed byte slice and a bool flag that says if the line separator
// was really found or it is a default value (lf).
func lineEnd(b []byte) ([]byte, bool) {
	switch {
	case bytes.HasSuffix(b, crlf):
		return crlf, true
	case bytes.HasSuffix(b, lf):
		return lf, true
	default:
		return lf, false
	}
}

// ParseFile parses the named file as an archive.
func ParseFile(file string) (*Archive, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return Parse(data), nil
}

// Parse parses the serialized form of an Archive.
// The returned Archive holds slices of data.
func Parse(data []byte) *Archive {
	a := new(Archive)
	var name string
	var lineSeparator []byte
	a.Comment, name, lineSeparator, data = findFileMarker(data, nil)
	for name != "" {
		f := File{name, nil}
		f.Data, name, lineSeparator, data = findFileMarker(data, lineSeparator)
		a.Files = append(a.Files, f)
	}
	return a
}

var (
	crlf          = []byte("\r\n")
	lf            = []byte("\n")
	newlineMarker = []byte("\n-- ")
	marker        = []byte("-- ")
	markerEnd     = []byte(" --")
)

// findFileMarker finds the next file marker in data,
// extracts the file name, and returns the data before the marker,
// the file name, and the data after the marker.
// lineSep states if \n or \r\n should be appended by fixNL.
// If there is no next marker, findFileMarker returns before = fixNL(data), name = "", after = nil.
func findFileMarker(data, lineSep []byte) (before []byte, name string, lineSeparator []byte, after []byte) {
	var i int
	for {
		if name, lineSeparator, after = isMarker(data[i:]); name != "" {
			return data[:i], name, lineSeparator, after
		}
		j := bytes.Index(data[i:], newlineMarker)
		if j < 0 {
			return fixNL(data, lineSep), "", lineSep, nil
		}
		i += j + 1 // positioned at start of new possible marker
	}
}

// isMarker checks whether data begins with a file marker line.
// If so, it returns the name from the line, used line separator and the data after the line.
// Otherwise it returns name == "" with nil lineSeparator ("\n") and after.
// If the data does not contain a new line the default line separator is returned ("\n").
func isMarker(data []byte) (name string, lineSeparator, after []byte) {
	if !bytes.HasPrefix(data, marker) {
		return "", nil, nil
	}
	lineSeparator = lf
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		if len(data) > 0 && data[i-1] == '\r' {
			data, after = data[:i-1], data[i+1:]
			lineSeparator = crlf
		} else {
			data, after = data[:i], data[i+1:]
			lineSeparator = lf
		}
	}
	if !(bytes.HasSuffix(data, markerEnd) && len(data) >= len(marker)+len(markerEnd)) {
		return "", nil, nil
	}
	return strings.TrimSpace(string(data[len(marker) : len(data)-len(markerEnd)])), lineSeparator, after
}

// If data is empty or ends in lineSeparator, fixNL returns data.
// lineSeparator states if \n or \r\n should be appended as a line separator if it is not present.
// Otherwise fixNL returns a new slice consisting of data with a final lineSeparator added.
func fixNL(data , lineSeparator []byte) []byte {
	if len(data) == 0 || bytes.HasSuffix(data, lf) {
		return data
	}
	d := make([]byte, len(data)+len(lineSeparator))
	copy(d, data)
	copy(d[len(data):], lineSeparator)
	return d
}
