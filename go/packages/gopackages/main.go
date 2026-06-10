// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The gopackages command is a diagnostic tool that demonstrates
// how to use golang.org/x/tools/go/packages to load, parse,
// type-check, and print one or more Go packages.
// Its precise output is unspecified and may change.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/types"
	"log"
	"os"
	"runtime/pprof"
	"runtime/trace"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/drivertest"
)

func main() {
	drivertest.RunIfChild()

	var (
		cpuprofile   = flag.String("profile.cpu", "", "write CPU profile to this file")
		memprofile   = flag.String("profile.mem", "", "write memory profile to this file")
		traceprofile = flag.String("profile.trace", "", "write trace log to this file")
	)

	var (
		deps       = flag.Bool("deps", false, "show dependencies too")
		test       = flag.Bool("test", false, "include any tests implied by the patterns")
		mode       = flag.String("mode", "imports", "mode (one of files, imports, types, syntax, allsyntax)")
		tags       = flag.String("tags", "", "comma-separated list of extra build tags (see: go help buildconstraint)")
		private    = flag.Bool("private", false, "show non-exported declarations too (if -mode=syntax)")
		printJSON  = flag.Bool("json", false, "print package in JSON form")
		driver     = flag.Bool("driver", false, "use golist passthrough driver (for debugging driver issues)")
		buildFlags stringListValue
	)
	flag.Var(&buildFlags, "buildflag", "pass argument to underlying build system (may be repeated)")

	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), `gopackages loads, parses, type-checks, and prints one or more Go packages.

Usage:
  gopackages [flags] package...

Packages are specified using the notation of "go list",
or other underlying build system.

The mode flag determines how much information is computed and printed
for the specified packages. In order of increasing computational cost,
the legal values are:

 -mode=files     shows only the names of the packages' files.
 -mode=imports   also shows the imports. (This is the default.)
 -mode=types     loads the compiler's export data and displays the
                 type of each exported declaration.
 -mode=syntax    parses and type checks syntax trees for the initial
                 packages. (With the -private flag, the types of
                 non-exported declarations are shown too.)
                 Type information for dependencies is obtained from
                 compiler export data.
 -mode=allsyntax is like -mode=syntax but applied to all dependencies.

Flags:
`)
		flag.PrintDefaults()
	}

	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if *traceprofile != "" {
		f, err := os.Create(*traceprofile)
		if err != nil {
			log.Fatal(err)
		}
		trace.Start(f)
		defer trace.Stop()
	}

	if *memprofile != "" {
		defer func() {
			f, err := os.Create(*memprofile)
			if err != nil {
				log.Fatal(err)
			}
			pprof.WriteHeapProfile(f)
			f.Close()
		}()
	}

	if err := run(context.Background(), flag.Args(), *deps, *test, *mode, *tags, *private, *printJSON, *driver, buildFlags); err != nil {
		fmt.Fprintf(os.Stderr, "gopackages: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, deps, test bool, mode, tags string, private, printJSON, driver bool, buildFlags []string) error {
	env := os.Environ()
	if driver {
		env = append(env, drivertest.Env(log.Default())...)
	}

	// Load, parse, and type-check the packages named on the command line.
	cfg := &packages.Config{
		Mode:       packages.LoadSyntax,
		Tests:      test,
		BuildFlags: append([]string{"-tags=" + tags}, buildFlags...),
		Env:        env,
		Context:    ctx,
	}

	// -mode flag
	switch strings.ToLower(mode) {
	case "files":
		cfg.Mode = packages.LoadFiles
	case "imports":
		cfg.Mode = packages.LoadImports
	case "types":
		cfg.Mode = packages.LoadTypes
	case "syntax":
		cfg.Mode = packages.LoadSyntax
	case "allsyntax":
		cfg.Mode = packages.LoadAllSyntax
	default:
		return fmt.Errorf("invalid mode: %s", mode)
	}
	cfg.Mode |= packages.NeedModule

	lpkgs, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}

	// -deps: print dependencies too.
	if deps {
		// We can't use packages.All because
		// we need an ordered traversal.
		var all []*packages.Package // postorder
		seen := make(map[*packages.Package]bool)
		var visit func(*packages.Package)
		visit = func(lpkg *packages.Package) {
			if !seen[lpkg] {
				seen[lpkg] = true

				// visit imports
				var importPaths []string
				for path := range lpkg.Imports {
					importPaths = append(importPaths, path)
				}
				sort.Strings(importPaths) // for determinism
				for _, path := range importPaths {
					visit(lpkg.Imports[path])
				}

				all = append(all, lpkg)
			}
		}
		for _, lpkg := range lpkgs {
			visit(lpkg)
		}
		lpkgs = all
	}

	for _, lpkg := range lpkgs {
		printPkg(lpkg, printJSON, private)
	}
	return nil
}

func printPkg(lpkg *packages.Package, printJSON, private bool) {
	if printJSON {
		data, _ := json.MarshalIndent(lpkg, "", "\t")
		os.Stdout.Write(data)
		return
	}
	// title
	var kind string
	// TODO(matloob): If IsTest is added back print "test command" or
	// "test package" for packages with IsTest == true.
	if lpkg.Name == "main" {
		kind += "command"
	} else {
		kind += "package"
	}
	fmt.Printf("Go %s %q:\n", kind, lpkg.ID) // unique ID
	if mod := lpkg.Module; mod != nil {
		fmt.Printf("\tmodule %s@%s\n", mod.Path, mod.Version)
	}
	fmt.Printf("\tpackage %s\n", lpkg.Name)

	// characterize type info
	if lpkg.Types == nil {
		fmt.Printf("\thas no exported type info\n")
	} else if !lpkg.Types.Complete() {
		fmt.Printf("\thas incomplete exported type info\n")
	} else if len(lpkg.Syntax) == 0 {
		fmt.Printf("\thas complete exported type info\n")
	} else {
		fmt.Printf("\thas complete exported type info and typed ASTs\n")
	}
	if lpkg.Types != nil && lpkg.IllTyped && len(lpkg.Errors) == 0 {
		fmt.Printf("\thas an error among its dependencies\n")
	}

	// source files
	for _, src := range lpkg.GoFiles {
		fmt.Printf("\tfile %s\n", src)
	}

	// imports
	var lines []string
	for importPath, imp := range lpkg.Imports {
		var line string
		if imp.ID == importPath {
			line = fmt.Sprintf("\timport %q", importPath)
		} else {
			line = fmt.Sprintf("\timport %q => %q", importPath, imp.ID)
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	for _, line := range lines {
		fmt.Println(line)
	}

	// errors
	for _, err := range lpkg.Errors {
		fmt.Printf("\t%s\n", err)
	}

	// types of package members
	if lpkg.Types != nil {
		qual := types.RelativeTo(lpkg.Types)
		scope := lpkg.Types.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() && !private {
				continue // skip unexported names
			}

			fmt.Printf("\t%s\n", types.ObjectString(obj, qual))
			if _, ok := obj.(*types.TypeName); ok {
				for _, meth := range typeutil.IntuitiveMethodSet(obj.Type(), nil) {
					if !meth.Obj().Exported() && !private {
						continue // skip unexported names
					}
					fmt.Printf("\t%s\n", types.SelectionString(meth, qual))
				}
			}
		}
	}

	fmt.Println()
}

// stringListValue is a flag.Value that accumulates strings.
// e.g. --flag=one --flag=two would produce []string{"one", "two"}.
type stringListValue []string

func (ss *stringListValue) Get() any { return []string(*ss) }

func (ss *stringListValue) String() string { return fmt.Sprintf("%q", *ss) }

func (ss *stringListValue) Set(s string) error { *ss = append(*ss, s); return nil }
