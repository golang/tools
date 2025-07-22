// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.25 && unix

package debug

import (
	"os"
	"syscall"
)

func init() {
	// UNIX: kill the whole process group, since
	// "go tool trace" starts a cmd/trace child.
	kill = killGroup
	sysProcAttr.Setpgid = true
}

func killGroup(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGKILL)
}
