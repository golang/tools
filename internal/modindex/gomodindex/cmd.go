// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// A command for building and maintaing the module cache
// a.out <flags> <command> <args>
// The commands are 'create' which builds a new index,
// 'update', which attempts to update an existing index,
// 'query', which looks up things in the index.
// 'clean', which remove obsolete index files.
// If the command is invoked with no arguments, it defaults to 'create'.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/tools/internal/modindex"
)

var verbose = flag.Int("v", 0, "how much information to print")

type cmd struct {
	name string
	f    func(string)
	doc  string
}

var cmds = []cmd{
	{"create", index, "create a clean index of GOMODCACHE"},
	{"update", update, "if there is an existing index of GOMODCACHE, update it. Otherise create one."},
	{"clean", clean, "removed unreferenced indexes more than an hour old"},
	{"query", query, "not yet implemented"},
}

func goEnv(s string) string {
	out, err := exec.Command("go", "env", s).Output()
	if err != nil {
		return ""
	}
	out = bytes.TrimSpace(out)
	return string(out)
}

func main() {
	flag.Parse()
	log.SetFlags(log.Lshortfile)
	cachedir := goEnv("GOMODCACHE")
	if cachedir == "" {
		log.Fatal("can't find GOMODCACHE")
	}
	if flag.NArg() == 0 {
		index(cachedir)
		return
	}
	for _, c := range cmds {
		if flag.Arg(0) == c.name {
			c.f(cachedir)
			return
		}
	}
	flag.Usage()
}

func init() {
	var sb strings.Builder
	fmt.Fprintf(&sb, "usage:\n")
	for _, c := range cmds {
		fmt.Fprintf(&sb, "'%s': %s\n", c.name, c.doc)
	}
	msg := sb.String()
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, msg)
	}
}

func index(dir string) {
	modindex.Create(dir)
}

func update(dir string) {
	modindex.Update(dir)
}

func query(dir string) {
	panic("implement")
}
func clean(_ string) {
	des := modindex.IndexDir
	// look at the files starting with 'index'
	// the current ones of each version are pointed to by
	// index-name-%d files. Any others more than an hour old
	// are deleted.
	dis, err := os.ReadDir(des)
	if err != nil {
		log.Fatal(err)
	}
	cutoff := time.Now().Add(-time.Hour)
	var inames []string               // older files named index*
	curnames := make(map[string]bool) // current versions of index (different CurrentVersion)
	for _, de := range dis {
		if !strings.HasPrefix(de.Name(), "index") {
			continue
		}
		if strings.HasPrefix(de.Name(), "index-name-") {
			buf, err := os.ReadFile(filepath.Join(des, de.Name()))
			if err != nil {
				log.Print(err)
				continue
			}
			curnames[string(buf)] = true
			if *verbose > 1 {
				log.Printf("latest index is %s", string(buf))
			}
		}
		info, err := de.Info()
		if err != nil {
			log.Print(err)
			continue
		}
		if info.ModTime().Before(cutoff) && !strings.HasPrefix(de.Name(), "index-name-") {
			// add to the list of files to be removed. index-name-%d files are never removed
			inames = append(inames, de.Name())
			if *verbose > 0 {
				log.Printf("%s:%s", de.Name(), cutoff.Sub(info.ModTime()))
			}
		}
	}
	for _, nm := range inames {
		if curnames[nm] {
			continue
		}
		err := os.Remove(filepath.Join(des, nm))
		if err != nil && *verbose > 0 {
			log.Printf("%s not removed (%v)", nm, err)
		}
	}
}
