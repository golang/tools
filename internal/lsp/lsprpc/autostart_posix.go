// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build darwin dragonfly freebsd linux netbsd openbsd solaris

package lsprpc

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

func init() {
	startRemote = startRemotePosix
	autoNetworkAddress = autoNetworkAddressPosix
	verifyRemoteOwnership = verifyRemoteOwnershipPosix
}

func startRemotePosix(goplsPath string, args ...string) error {
	cmd := exec.Command(goplsPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting remote gopls: %v", err)
	}
	return nil
}

// autoNetworkAddress resolves an id on the 'auto' pseduo-network to a
// real network and address. On unix, this uses unix domain sockets.
func autoNetworkAddressPosix(goplsPath, id string) (network string, address string) {
	// Especially when doing local development or testing, it's important that
	// the remote gopls instance we connect to is running the same binary as our
	// forwarder. So we encode a short hash of the binary path into the daemon
	// socket name. If possible, we also include the buildid in this hash, to
	// account for long-running processes where the binary has been subsequently
	// rebuilt.
	h := sha1.New()
	cmd := exec.Command("go", "tool", "buildid", goplsPath)
	cmd.Stdout = h
	var pathHash []byte
	if err := cmd.Run(); err == nil {
		pathHash = h.Sum(nil)
	} else {
		log.Printf("error getting current buildid: %v", err)
		sum := sha1.Sum([]byte(goplsPath))
		pathHash = sum[:]
	}
	shortHash := fmt.Sprintf("%x", pathHash)[:6]
	user := os.Getenv("USER")
	if user == "" {
		user = "shared"
	}
	basename := filepath.Base(goplsPath)
	idComponent := ""
	if id != "" {
		idComponent = "-" + id
	}
	return "unix", filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s-daemon.%s%s", basename, shortHash, user, idComponent))
}

func verifyRemoteOwnershipPosix(network, address string) (bool, error) {
	if network != "unix" {
		return true, nil
	}
	fi, err := os.Stat(address)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("checking socket owner: %v", err)
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false, errors.New("fi.Sys() is not a Stat_t")
	}
	user, err := user.Current()
	if err != nil {
		return false, fmt.Errorf("checking current user: %v", err)
	}
	uid, err := strconv.ParseUint(user.Uid, 10, 32)
	if err != nil {
		return false, fmt.Errorf("parsing current UID: %v", err)
	}
	return stat.Uid == uint32(uid), nil
}
