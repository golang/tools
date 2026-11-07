// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build plan9

package robustio

import (
	"os"
	"strings"
	"syscall"
	"time"
)

var errFileNotFound = syscall.ENOENT

// lockedErrStrings are the errors Plan 9 file servers return for a file that is
// already open for exclusive use, mirroring the set recognized by cmd/go's
// lockedfile.
var lockedErrStrings = [...]string{
	"file is locked",                  // cwfs, kfs
	"exclusive lock",                  // fossil
	"exclusive use file already open", // ramfs
}

// isEphemeralError returns true if err may be resolved by waiting.
func isEphemeralError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, frag := range lockedErrStrings {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

func getErrno(error) (uintptr, bool) {
	return 0, false
}

func getFileID(filename string) (FileID, time.Time, error) {
	fi, err := os.Stat(filename)
	if err != nil {
		return FileID{}, time.Time{}, err
	}
	dir := fi.Sys().(*syscall.Dir)
	return FileID{
		device: uint64(dir.Type)<<32 | uint64(dir.Dev),
		inode:  dir.Qid.Path,
	}, fi.ModTime(), nil
}
