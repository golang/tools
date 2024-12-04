// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file is a copy of $GOROOT/src/go/internal/gcimporter/exportdata.go.

// This file implements FindExportData.

package gcimporter

import (
	"bufio"
	"fmt"
	"strings"
)

// FindExportData positions the reader r at the beginning of the
// export data section of an underlying cmd/compile created archive
// file by reading from it. The reader must be positioned at the
// start of the file before calling this function.
// This returns the length of the export data in bytes.
//
// This function is needed by [gcexportdata.Read], which must
// accept inputs produced by the last two releases of cmd/compile,
// plus tip.
func FindExportData(r *bufio.Reader) (size int64, err error) {
	// Read first line to make sure this is an object file.
	line, err := r.ReadSlice('\n')
	if err != nil {
		err = fmt.Errorf("can't find export data (%v)", err)
		return
	}

	// Is the first line an archive file signature?
	if string(line) != "!<arch>\n" {
		err = fmt.Errorf("not the start of an archive file (%q)", line)
		return
	}

	// Archive file with the first file being __.PKGDEF.
	arsize := readArchiveHeader(r, "__.PKGDEF")
	if arsize <= 0 {
		err = fmt.Errorf("not a package file")
		return
	}
	size = int64(arsize)

	// Read first line of __.PKGDEF data, so that line
	// is once again the first line of the input.
	if line, err = r.ReadSlice('\n'); err != nil {
		err = fmt.Errorf("can't find export data (%v)", err)
		return
	}
	size -= int64(len(line))

	// Now at __.PKGDEF in archive or still at beginning of file.
	// Either way, line should begin with "go object ".
	if !strings.HasPrefix(string(line), "go object ") {
		err = fmt.Errorf("not a Go object file")
		return
	}

	// Skip over object headers to get to the export data section header "$$B\n".
	// Object headers are lines that do not start with '$'.
	for line[0] != '$' {
		if line, err = r.ReadSlice('\n'); err != nil {
			err = fmt.Errorf("can't find export data (%v)", err)
			return
		}
		size -= int64(len(line))
	}

	// Check for the binary export data section header "$$B\n".
	hdr := string(line)
	if hdr != "$$B\n" {
		err = fmt.Errorf("unknown export data header: %q", hdr)
		return
	}

	// For files with a binary export data header "$$B\n",
	// these are always terminated by an end-of-section marker "\n$$\n".
	// So the last bytes must always be this constant.
	//
	// The end-of-section marker is not a part of the export data itself.
	// Do not include these in size.
	//
	// It would be nice to have sanity check that the final bytes after
	// the export data are indeed the end-of-section marker. The split
	// of gcexportdata.NewReader and gcexportdata.Read make checking this
	// ugly so gcimporter gives up enforcing this. The compiler and go/types
	// importer do enforce this, which seems good enough.
	const endofsection = "\n$$\n"
	size -= int64(len(endofsection))

	if size < 0 {
		err = fmt.Errorf("invalid size (%d) in the archive file: %d bytes remain without section headers (recompile package)", arsize, size)
		return
	}

	return
}
