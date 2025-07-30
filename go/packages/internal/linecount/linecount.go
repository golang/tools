// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The linecount command shows the number of lines of code in a set of
// Go packages plus their dependencies. It serves as a working
// illustration of the [packages.Load] operation.
//
// Example: show gopls' total source line count, and its breakdown
// between gopls, x/tools, and the std go/* packages. (The balance
// comes from other std packages.)
//
//	$ linecount -mode=total ./gopls
//	752124
//	$ linecount -mode=total -module=golang.org/x/tools/gopls ./gopls
//	103519
//	$ linecount -mode=total -module=golang.org/x/tools ./gopls
//	99504
//	$ linecount -mode=total -prefix=go -module=std ./gopls
//	47502
//
// Example: show the top 5 modules contributing to gopls' source line count:
//
//	$ linecount -mode=module ./gopls | head -n 5
//	440274	std
//	103519	golang.org/x/tools/gopls
//	99504	golang.org/x/tools
//	40220	honnef.co/go/tools
//	17707	golang.org/x/text
//
// Example: show the top 3 largest files in the gopls module:
//
//	$ linecount -mode=file -module=golang.org/x/tools/gopls ./gopls | head -n 3
//	6841	gopls/internal/protocol/tsprotocol.go
//	3769	gopls/internal/golang/completion/completion.go
//	2202	gopls/internal/cache/snapshot.go
package main

import (
	"bytes"
	"cmp"
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
)

// TODO(adonovan): filters:
// - exclude comment and blank lines (-nonblank)
// - exclude generated files (-generated=false)
// - exclude non-CompiledGoFiles
// - include OtherFiles (asm, etc)
// - include tests (needs care to avoid double counting)

func usage() {
	// See https://go.dev/issue/63659.
	fmt.Fprintf(os.Stderr, "Usage: linecount [flags] packages...\n")
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Docs: go doc golang.org/x/tools/go/packages/internal/linecount
https://pkg.go.dev/golang.org/x/tools/go/packages/internal/linecount
`)
}

func main() {
	// Parse command line.
	log.SetPrefix("linecount: ")
	log.SetFlags(0)
	var (
		mode       = flag.String("mode", "file", "group lines by 'module', 'package', or 'file', or show only 'total'")
		prefix     = flag.String("prefix", "", "only count files in packages whose path has the specified prefix")
		onlyModule = flag.String("module", "", "only count files in the specified module")
	)
	flag.Usage = usage
	flag.Parse()
	if len(flag.Args()) == 0 {
		usage()
		os.Exit(1)
	}

	// Load packages.
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedModule,
	}
	pkgs, err := packages.Load(cfg, flag.Args()...)
	if err != nil {
		log.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	// Read files and count lines.
	var (
		mu        sync.Mutex
		byFile    = make(map[string]int)
		byPackage = make(map[string]int)
		byModule  = make(map[string]int)
	)
	var g errgroup.Group
	g.SetLimit(20) // file system parallelism level
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		pkgpath := p.PkgPath
		module := "std"
		if p.Module != nil {
			module = p.Module.Path
		}
		if *prefix != "" && !within(pkgpath, path.Clean(*prefix)) {
			return
		}
		if *onlyModule != "" && module != *onlyModule {
			return
		}
		for _, f := range p.GoFiles {
			g.Go(func() error {
				data, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				n := bytes.Count(data, []byte("\n"))

				mu.Lock()
				byFile[f] = n
				byPackage[pkgpath] += n
				byModule[module] += n
				mu.Unlock()

				return nil
			})
		}
	})
	if err := g.Wait(); err != nil {
		log.Fatal(err)
	}

	// Display the result.
	switch *mode {
	case "file", "package", "module":
		var m map[string]int
		switch *mode {
		case "file":
			m = byFile
		case "package":
			m = byPackage
		case "module":
			m = byModule
		}
		type item struct {
			name  string
			count int
		}
		var items []item
		for name, count := range m {
			items = append(items, item{name, count})
		}
		slices.SortFunc(items, func(x, y item) int {
			return -cmp.Compare(x.count, y.count)
		})
		for _, item := range items {
			fmt.Printf("%d\t%s\n", item.count, item.name)
		}

	case "total":
		total := 0
		for _, n := range byFile {
			total += n
		}
		fmt.Printf("%d\n", total)

	default:
		log.Fatalf("invalid -mode %q (want file, package, module, or total)", *mode)
	}
}

func within(file, dir string) bool {
	return file == dir ||
		strings.HasPrefix(file, dir) && file[len(dir)] == os.PathSeparator
}
