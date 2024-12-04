// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file should be kept in sync with $GOROOT/src/internal/exportdata/exportdata.go.
// This file also additionally implements FindExportData for gcexportdata.NewReader.

package gcimporter

import (
	"bufio"
	"bytes"
	"errors"
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
//
// FindPkg is only used in tests within x/tools.
func FindPkg(path, srcDir string) (filename, id string, err error) {
	// TODO(taking): Move internal/exportdata.FindPkg into its own file,
	// and then this copy into a _test package.
	if path == "" {
		return "", "", errors.New("path is empty")
	}

	var noext string
	switch {
	default:
		// "x" -> "$GOPATH/pkg/$GOOS_$GOARCH/x.ext", "x"
		// Don't require the source files to be present.
		if abs, err := filepath.Abs(srcDir); err == nil { // see issue 14282
			srcDir = abs
		}
		var bp *build.Package
		bp, err = build.Import(path, srcDir, build.FindOnly|build.AllowBinary)
		if bp.PkgObj == "" {
			if bp.Goroot && bp.Dir != "" {
				filename, err = lookupGorootExport(bp.Dir)
				if err == nil {
					_, err = os.Stat(filename)
				}
				if err == nil {
					return filename, bp.ImportPath, nil
				}
			}
			goto notfound
		} else {
			noext = strings.TrimSuffix(bp.PkgObj, ".a")
		}
		id = bp.ImportPath

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

	// try extensions
	for _, ext := range pkgExts {
		filename = noext + ext
		f, statErr := os.Stat(filename)
		if statErr == nil && !f.IsDir() {
			return filename, id, nil
		}
		if err == nil {
			err = statErr
		}
	}

notfound:
	if err == nil {
		return "", path, fmt.Errorf("can't find import: %q", path)
	}
	return "", path, fmt.Errorf("can't find import: %q: %w", path, err)
}

var pkgExts = [...]string{".a", ".o"} // a file from the build cache will have no extension

var exportMap sync.Map // package dir → func() (string, error)

// lookupGorootExport returns the location of the export data
// (normally found in the build cache, but located in GOROOT/pkg
// in prior Go releases) for the package located in pkgDir.
//
// (We use the package's directory instead of its import path
// mainly to simplify handling of the packages in src/vendor
// and cmd/vendor.)
//
// lookupGorootExport is only used in tests within x/tools.
func lookupGorootExport(pkgDir string) (string, error) {
	f, ok := exportMap.Load(pkgDir)
	if !ok {
		var (
			listOnce   sync.Once
			exportPath string
			err        error
		)
		f, _ = exportMap.LoadOrStore(pkgDir, func() (string, error) {
			listOnce.Do(func() {
				cmd := exec.Command(filepath.Join(build.Default.GOROOT, "bin", "go"), "list", "-export", "-f", "{{.Export}}", pkgDir)
				cmd.Dir = build.Default.GOROOT
				cmd.Env = append(os.Environ(), "PWD="+cmd.Dir, "GOROOT="+build.Default.GOROOT)
				var output []byte
				output, err = cmd.Output()
				if err != nil {
					if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
						err = errors.New(string(ee.Stderr))
					}
					return
				}

				exports := strings.Split(string(bytes.TrimSpace(output)), "\n")
				if len(exports) != 1 {
					err = fmt.Errorf("go list reported %d exports; expected 1", len(exports))
					return
				}

				exportPath = exports[0]
			})

			return exportPath, err
		})
	}

	return f.(func() (string, error))()
}
