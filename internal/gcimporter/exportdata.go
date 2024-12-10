// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file is a copy of $GOROOT/src/go/internal/gcimporter/exportdata.go.

// This file implements FindExportData.

package gcimporter

import (
	"bufio"
	"bytes"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

// FindPkg returns the filename and unique package id for an import
// path based on package information provided by build.Import (using
// the build.Default build.Context). A relative srcDir is interpreted
// relative to the current working directory.
// If no file was found, an empty filename is returned.
func FindPkg(path, srcDir string) (filename, id string) {
	if path == "" {
		return
	}

	var noext string
	switch {
	default:
		// "x" -> "$GOPATH/pkg/$GOOS_$GOARCH/x.ext", "x"
		// Don't require the source files to be present.
		if abs, err := filepath.Abs(srcDir); err == nil { // see issue 14282
			srcDir = abs
		}
		bp, _ := build.Import(path, srcDir, build.FindOnly|build.AllowBinary)
		if bp.PkgObj == "" {
			var ok bool
			if bp.Goroot && bp.Dir != "" {
				filename, ok = lookupGorootExport(bp.Dir)
			}
			if !ok {
				id = path // make sure we have an id to print in error message
				return
			}
		} else {
			noext = strings.TrimSuffix(bp.PkgObj, ".a")
			id = bp.ImportPath
		}

	case build.IsLocalImport(path):
		// "./x" -> "/this/directory/x.ext", "/this/directory/x"
		noext = filepath.Join(srcDir, path)
		id = noext

	case filepath.IsAbs(path):
		// for completeness only - go/build.Import
		// does not support absolute imports
		// "/x" -> "/x.ext", "/x"
		noext = path
		id = path
	}

	if false { // for debugging
		if path != id {
			fmt.Printf("%s -> %s\n", path, id)
		}
	}

	if filename != "" {
		if f, err := os.Stat(filename); err == nil && !f.IsDir() {
			return
		}
	}

	// try extensions
	for _, ext := range pkgExts {
		filename = noext + ext
		if f, err := os.Stat(filename); err == nil && !f.IsDir() {
			return
		}
	}

	filename = "" // not found
	return
}

var pkgExts = [...]string{".a", ".o"}

var exportMap sync.Map // package dir → func() (string, bool)

// lookupGorootExport returns the location of the export data
// (normally found in the build cache, but located in GOROOT/pkg
// in prior Go releases) for the package located in pkgDir.
//
// (We use the package's directory instead of its import path
// mainly to simplify handling of the packages in src/vendor
// and cmd/vendor.)
func lookupGorootExport(pkgDir string) (string, bool) {
	f, ok := exportMap.Load(pkgDir)
	if !ok {
		var (
			listOnce   sync.Once
			exportPath string
		)
		f, _ = exportMap.LoadOrStore(pkgDir, func() (string, bool) {
			listOnce.Do(func() {
				cmd := exec.Command("go", "list", "-export", "-f", "{{.Export}}", pkgDir)
				cmd.Dir = build.Default.GOROOT
				var output []byte
				output, err := cmd.Output()
				if err != nil {
					return
				}

				exports := strings.Split(string(bytes.TrimSpace(output)), "\n")
				if len(exports) != 1 {
					return
				}

				exportPath = exports[0]
			})

			return exportPath, exportPath != ""
		})
	}

	return f.(func() (string, bool))()
}
