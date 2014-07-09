// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"code.google.com/p/go.tools/go/vcs"
)

// builderEnv represents the environment that a Builder will run tests in.
type builderEnv interface {
	// setup sets up the builder environment and returns the directory to run the buildCmd in.
	setup(repo *Repo, workpath, hash string, envv []string) (string, error)
}

// goEnv represents the builderEnv for the main Go repo.
type goEnv struct {
	goos, goarch string
}

func (b *Builder) envv() []string {
	if runtime.GOOS == "windows" {
		return b.envvWindows()
	}

	var e []string
	if *buildTool == "go" {
		e = []string{
			"GOOS=" + b.goos,
			"GOARCH=" + b.goarch,
			"GOROOT_FINAL=/usr/local/go",
		}
		switch b.goos {
		case "android", "nacl":
			// Cross compile.
		default:
			// If we are building, for example, linux/386 on a linux/amd64 machine we want to
			// make sure that the whole build is done as a if this were compiled on a real
			// linux/386 machine. In other words, we want to not do a cross compilation build.
			// To do this we set GOHOSTOS and GOHOSTARCH to override the detection in make.bash.
			//
			// The exception to this rule is when we are doing nacl/android builds. These are by
			// definition always cross compilation, and we have support built into cmd/go to be
			// able to handle this case.
			e = append(e, "GOHOSTOS="+b.goos, "GOHOSTARCH="+b.goarch)
		}
	}

	for _, k := range extraEnv() {
		if s, ok := getenvOk(k); ok {
			e = append(e, k+"="+s)
		}
	}
	return e
}

func (b *Builder) envvWindows() []string {
	var start map[string]string
	if *buildTool == "go" {
		start = map[string]string{
			"GOOS":         b.goos,
			"GOHOSTOS":     b.goos,
			"GOARCH":       b.goarch,
			"GOHOSTARCH":   b.goarch,
			"GOROOT_FINAL": `c:\go`,
			"GOBUILDEXIT":  "1", // exit all.bat with completion status.
		}
	}

	for _, name := range extraEnv() {
		if s, ok := getenvOk(name); ok {
			start[name] = s
		}
	}
	skip := map[string]bool{
		"GOBIN":   true,
		"GOPATH":  true,
		"GOROOT":  true,
		"INCLUDE": true,
		"LIB":     true,
	}
	var e []string
	for name, v := range start {
		e = append(e, name+"="+v)
		skip[name] = true
	}
	for _, kv := range os.Environ() {
		s := strings.SplitN(kv, "=", 2)
		name := strings.ToUpper(s[0])
		switch {
		case name == "":
			// variables, like "=C:=C:\", just copy them
			e = append(e, kv)
		case !skip[name]:
			e = append(e, kv)
			skip[name] = true
		}
	}
	return e
}

// setup for a goEnv clones the main go repo to workpath/go at the provided hash
// and returns the path workpath/go/src, the location of all go build scripts.
func (env *goEnv) setup(repo *Repo, workpath, hash string, envv []string) (string, error) {
	goworkpath := filepath.Join(workpath, "go")
	if err := repo.Export(goworkpath, hash); err != nil {
		return "", fmt.Errorf("error exporting repository: %s", err)
	}
	if err := ioutil.WriteFile(filepath.Join(goworkpath, "VERSION"), []byte(hash), 0644); err != nil {
		return "", fmt.Errorf("error writing VERSION file: %s", err)
	}
	return filepath.Join(goworkpath, "src"), nil
}

// gccgoEnv represents the builderEnv for the gccgo compiler.
type gccgoEnv struct{}

// setup for a gccgoEnv clones the gofrontend repo to workpath/go at the hash
// and clones the latest GCC branch to repo.Path/gcc. The gccgo sources are
// replaced with the updated sources in the gofrontend repo and gcc gets
// gets configured and built in workpath/gcc-objdir. The path to
// workpath/gcc-objdir is returned.
func (env *gccgoEnv) setup(repo *Repo, workpath, hash string, envv []string) (string, error) {
	gccpath := filepath.Join(repo.Path, "gcc")

	// get a handle to Git vcs.Cmd for pulling down GCC from the mirror.
	git := vcs.ByCmd("git")

	// only pull down gcc if we don't have a local copy.
	if _, err := os.Stat(gccpath); err != nil {
		if err := timeout(*cmdTimeout, func() error {
			// pull down a working copy of GCC.
			return git.Create(gccpath, *gccPath)
		}); err != nil {
			return "", err
		}
	}

	if err := git.Download(gccpath); err != nil {
		return "", err
	}

	// get the modified files for this commit.
	statusCmd := []string{
		"hg",
		"status",
		"--no-status",
		"--change",
		hash,
	}

	var buf bytes.Buffer
	if _, err := runOutput(*cmdTimeout, envv, &buf, repo.Path, statusCmd...); err != nil {
		return "", fmt.Errorf("Failed to find the modified files for %s: %s", hash, err)
	}
	modifiedFiles := strings.Split(buf.String(), "\n")
	var isMirrored bool
	for _, f := range modifiedFiles {
		if strings.HasPrefix(f, "go/") || strings.HasPrefix(f, "libgo/") {
			isMirrored = true
			break
		}
	}

	// use git log to find the corresponding commit to sync to in the gcc mirror.
	// If the files modified in the gofrontend are mirrored to gcc, we expect a
	// commit with a similar description in the gcc mirror. If the files modified are
	// not mirrored, e.g. in support/, we can sync to the most recent gcc commit that
	// occurred before those files were modified to verify gccgo's status at that point.
	logCmd := []string{
		"git",
		"log",
		"-1",
		"--format=%H",
	}
	var errMsg string
	if isMirrored {
		commitDesc, err := repo.Master.VCS.LogAtRev(repo.Path, hash, "{desc|escape}")
		if err != nil {
			return "", err
		}

		logCmd = append(logCmd, "--grep", "'"+string(commitDesc)+"'", "--regexp-ignore-case")
		errMsg = fmt.Sprintf("Failed to find a commit with a similar description to '%s'", string(commitDesc))
	} else {
		commitDate, err := repo.Master.VCS.LogAtRev(repo.Path, hash, "{date|rfc3339date}")
		if err != nil {
			return "", err
		}

		logCmd = append(logCmd, "--before", string(commitDate))
		errMsg = fmt.Sprintf("Failed to find a commit before '%s'", string(commitDate))
	}

	buf.Reset()
	if _, err := runOutput(*cmdTimeout, envv, &buf, gccpath, logCmd...); err != nil {
		return "", fmt.Errorf("%s: %s", errMsg, err)
	}
	gccRev := buf.String()
	if gccRev == "" {
		return "", fmt.Errorf(errMsg)
	}

	// checkout gccRev
	// TODO(cmang): Fix this to work in parallel mode.
	checkoutCmd := []string{
		"git",
		"reset",
		"--hard",
		strings.TrimSpace(gccRev),
	}
	if _, err := runOutput(*cmdTimeout, envv, ioutil.Discard, gccpath, checkoutCmd...); err != nil {
		return "", fmt.Errorf("Failed to checkout commit at revision %s: %s", gccRev, err)
	}

	// make objdir to work in
	gccobjdir := filepath.Join(workpath, "gcc-objdir")
	if err := os.Mkdir(gccobjdir, mkdirPerm); err != nil {
		return "", err
	}

	// configure GCC with substituted gofrontend and libgo
	gccConfigCmd := []string{
		filepath.Join(gccpath, "configure"),
		"--enable-languages=c,c++,go",
		"--disable-bootstrap",
		"--disable-multilib",
	}
	if _, err := runOutput(*cmdTimeout, envv, ioutil.Discard, gccobjdir, gccConfigCmd...); err != nil {
		return "", fmt.Errorf("Failed to configure GCC: %s", err)
	}

	// build gcc
	if _, err := runOutput(*buildTimeout, envv, ioutil.Discard, gccobjdir, "make"); err != nil {
		return "", fmt.Errorf("Failed to build GCC: %s", err)
	}

	return gccobjdir, nil
}

func getenvOk(k string) (v string, ok bool) {
	v = os.Getenv(k)
	if v != "" {
		return v, true
	}
	keq := k + "="
	for _, kv := range os.Environ() {
		if kv == keq {
			return "", true
		}
	}
	return "", false
}

// extraEnv returns environment variables that need to be copied from
// the gobuilder's environment to the envv of its subprocesses.
func extraEnv() []string {
	extra := []string{
		"GOARM",
		"GO386",
		"CGO_ENABLED",
		"CC",
		"CC_FOR_TARGET",
		"PATH",
		"TMPDIR",
		"USER",
	}
	if runtime.GOOS == "plan9" {
		extra = append(extra, "objtype", "cputype", "path")
	}
	return extra
}
