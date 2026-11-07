// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"bytes"
	"errors"
	"go/build"
	"go/build/constraint"
	"io"
	"path/filepath"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/util/bug"
)

type port struct{ GOOS, GOARCH string }

var (
	// preferredPorts holds GOOS/GOARCH combinations for which we dynamically
	// create new Views, by setting GOOS=... and GOARCH=... on top of
	// user-provided configuration when we detect that the default build
	// configuration does not match an open file. Ports are matched in the order
	// defined below, so that when multiple ports match a file we use the port
	// occurring at a lower index in the slice. For that reason, we sort first
	// class ports ahead of secondary ports, and (among first class ports) 64-bit
	// ports ahead of the less common 32-bit ports.
	preferredPorts = []port{
		// First class ports, from https://go.dev/wiki/PortingPolicy.
		{"darwin", "amd64"},
		{"darwin", "arm64"},
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"windows", "amd64"},
		{"linux", "arm"},
		{"linux", "386"},
		{"windows", "386"},

		// Secondary ports, from GOROOT/src/internal/platform/zosarch.go.
		// (First class ports are commented out.)
		{"aix", "ppc64"},
		{"dragonfly", "amd64"},
		{"freebsd", "386"},
		{"freebsd", "amd64"},
		{"freebsd", "arm"},
		{"freebsd", "arm64"},
		{"illumos", "amd64"},
		{"linux", "ppc64"},
		{"linux", "ppc64le"},
		{"linux", "mips"},
		{"linux", "mipsle"},
		{"linux", "mips64"},
		{"linux", "mips64le"},
		{"linux", "riscv64"},
		{"linux", "s390x"},
		{"android", "386"},
		{"android", "amd64"},
		{"android", "arm"},
		{"android", "arm64"},
		{"ios", "arm64"},
		{"ios", "amd64"},
		{"js", "wasm"},
		{"netbsd", "386"},
		{"netbsd", "amd64"},
		{"netbsd", "arm"},
		{"netbsd", "arm64"},
		{"openbsd", "386"},
		{"openbsd", "amd64"},
		{"openbsd", "arm"},
		{"openbsd", "arm64"},
		{"openbsd", "mips64"},
		{"plan9", "386"},
		{"plan9", "amd64"},
		{"plan9", "arm"},
		{"solaris", "amd64"},
		{"windows", "arm"},
		{"windows", "arm64"},

		{"aix", "ppc64"},
		{"android", "386"},
		{"android", "amd64"},
		{"android", "arm"},
		{"android", "arm64"},
		// {"darwin", "amd64"},
		// {"darwin", "arm64"},
		{"dragonfly", "amd64"},
		{"freebsd", "386"},
		{"freebsd", "amd64"},
		{"freebsd", "arm"},
		{"freebsd", "arm64"},
		{"freebsd", "riscv64"},
		{"illumos", "amd64"},
		{"ios", "amd64"},
		{"ios", "arm64"},
		{"js", "wasm"},
		// {"linux", "386"},
		// {"linux", "amd64"},
		// {"linux", "arm"},
		// {"linux", "arm64"},
		{"linux", "loong64"},
		{"linux", "mips"},
		{"linux", "mips64"},
		{"linux", "mips64le"},
		{"linux", "mipsle"},
		{"linux", "ppc64"},
		{"linux", "ppc64le"},
		{"linux", "riscv64"},
		{"linux", "s390x"},
		{"linux", "sparc64"},
		{"netbsd", "386"},
		{"netbsd", "amd64"},
		{"netbsd", "arm"},
		{"netbsd", "arm64"},
		{"openbsd", "386"},
		{"openbsd", "amd64"},
		{"openbsd", "arm"},
		{"openbsd", "arm64"},
		{"openbsd", "mips64"},
		{"openbsd", "ppc64"},
		{"openbsd", "riscv64"},
		{"plan9", "386"},
		{"plan9", "amd64"},
		{"plan9", "arm"},
		{"solaris", "amd64"},
		{"wasip1", "wasm"},
		// {"windows", "386"},
		// {"windows", "amd64"},
		{"windows", "arm"},
		{"windows", "arm64"},
	}
)

// matches reports whether the port matches a file with the given absolute path
// and content.
//
// Note that this function accepts content rather than e.g. a file.Handle,
// because we trim content before matching for performance reasons, and
// therefore need to do this outside of matches when considering multiple ports.
func (p port) matches(path string, content []byte) bool {
	ctxt := build.Default // make a copy
	ctxt.UseAllFiles = false
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		bug.Reportf("non-abs file path %q", path)
		return false // fail closed
	}
	dir, name := filepath.Split(path)

	// The only virtualized operation called by MatchFile is OpenFile.
	ctxt.OpenFile = func(p string) (io.ReadCloser, error) {
		if p != path {
			return nil, bug.Errorf("unexpected file %q", p)
		}
		return io.NopCloser(bytes.NewReader(content)), nil
	}

	ctxt.GOOS = p.GOOS
	ctxt.GOARCH = p.GOARCH
	ok, err := ctxt.MatchFile(dir, name)
	return err == nil && ok
}

// buildConstraintFile returns a minimal version of the given file (whose
// kind must be Go or assembly) containing only the same build
// constraints, if any, plus dummy syntax (e.g. package declaration).
//
// This is an unfortunate but necessary optimization, as matching build
// constraints using go/build has significant overhead, and involves parsing
// more than just the build constraint.
//
// It simulates the build constraint extraction performed by
// [go/build.Context.MatchFile] (see parseFileHeader in GOROOT/src/go/build/build.go).
//
// TestMatchingPortsConsistency enforces consistency by comparing results
// without trimming content.
func buildConstraintFile(kind file.Kind, content []byte) []byte {
	trimmed, goBuild, err := parseFileHeader(content)
	if err != nil {
		return nil
	}
	var buf bytes.Buffer
	if goBuild != nil {
		buf.Write(goBuild)
		buf.WriteByte('\n')
	} else {
		for line := range bytes.Lines(trimmed) {
			line = bytes.TrimSpace(line)
			if constraint.IsPlusBuild(string(line)) {
				buf.Write(line)
				buf.WriteByte('\n')
			}
		}
	}
	switch kind {
	case file.Go:
		// The package name does not matter, but go/build requires a
		// package declaration, and +build lines require a blank line
		// before the package declaration.
		buf.WriteString("\npackage p")
	case file.Asm:
		if buf.Len() > 0 && goBuild == nil {
			buf.WriteByte('\n') // legacy build tags require a trailing blank line
		}
	default:
		panic("unsupported file kind: " + kind.String())
	}
	return buf.Bytes()
}

var (
	slashSlash = []byte("//")
	starSlash  = []byte("*/")
	slashStar  = []byte("/*")

	errMultipleGoBuild = errors.New("multiple //go:build comments")
)

// parseFileHeader is copied almost verbatim from GOROOT/src/go/build/build.go.
func parseFileHeader(content []byte) (trimmed, goBuild []byte, err error) {
	end := 0
	p := content
	ended := false       // found non-blank, non-// line, so stopped accepting //go:build lines
	inSlashStar := false // in /* */ comment

Lines:
	for len(p) > 0 {
		line := p
		if i := bytes.IndexByte(line, '\n'); i >= 0 {
			line, p = line[:i], p[i+1:]
		} else {
			p = p[len(p):]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 && !ended { // Blank line
			// Remember position of most recent blank line.
			// When we find the first non-blank, non-// line,
			// this "end" position marks the latest file position
			// where a //go:build line can appear.
			// (It must appear _before_ a blank line before the non-blank, non-// line.
			// Yes, that's confusing, which is part of why we moved to //go:build lines.)
			// Note that ended==false here means that inSlashStar==false,
			// since seeing a /* would have set ended==true.
			end = len(content) - len(p)
			continue Lines
		}
		if !bytes.HasPrefix(line, slashSlash) { // Not comment line
			ended = true
		}

		if !inSlashStar && constraint.IsGoBuild(string(line)) {
			if goBuild != nil {
				return nil, nil, errMultipleGoBuild
			}
			goBuild = line
		}

	Comments:
		for len(line) > 0 {
			if inSlashStar {
				if i := bytes.Index(line, starSlash); i >= 0 {
					inSlashStar = false
					line = bytes.TrimSpace(line[i+len(starSlash):])
					continue Comments
				}
				continue Lines
			}
			if bytes.HasPrefix(line, slashSlash) {
				continue Lines
			}
			if bytes.HasPrefix(line, slashStar) {
				inSlashStar = true
				line = bytes.TrimSpace(line[len(slashStar):])
				continue Comments
			}
			// Found non-comment text.
			break Lines
		}
	}

	return content[:end], goBuild, nil
}
