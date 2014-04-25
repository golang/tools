// The eg command performs example-based refactoring.
package main

import (
	"flag"
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"

	"code.google.com/p/go.tools/go/loader"
	"code.google.com/p/go.tools/refactor/eg"
)

var (
	helpFlag       = flag.Bool("help", false, "show detailed help message")
	templateFlag   = flag.String("t", "", "template.go file specifying the refactoring")
	transitiveFlag = flag.Bool("transitive", false, "apply refactoring to all dependencies too")
	writeFlag      = flag.Bool("w", false, "rewrite input files in place (by default, the results are printed to standard output)")
	verboseFlag    = flag.Bool("v", false, "show verbose matcher diagnostics")
)

const usage = `eg: an example-based refactoring tool.

Usage: eg -t template.go [-w] [-transitive] <args>...
-t template.go	specifies the template file (use -help to see explanation)
-w          	causes files to be re-written in place.
-transitive 	causes all dependencies to be refactored too.
` + loader.FromArgsUsage

func main() {
	if err := doMain(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s.\n", filepath.Base(os.Args[0]), err)
		os.Exit(1)
	}
}

func doMain() error {
	flag.Parse()
	args := flag.Args()

	if *helpFlag {
		fmt.Fprint(os.Stderr, eg.Help)
		os.Exit(2)
	}

	if *templateFlag == "" {
		return fmt.Errorf("no -t template.go file specified")
	}

	conf := loader.Config{
		Fset:          token.NewFileSet(),
		ParserMode:    parser.ParseComments,
		SourceImports: true,
	}

	// The first Created package is the template.
	if err := conf.CreateFromFilenames("template", *templateFlag); err != nil {
		return err //  e.g. "foo.go:1: syntax error"
	}

	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	if _, err := conf.FromArgs(args, true); err != nil {
		return err
	}

	// Load, parse and type-check the whole program.
	iprog, err := conf.Load()
	if err != nil {
		return err
	}

	// Analyze the template.
	template := iprog.Created[0]
	xform, err := eg.NewTransformer(iprog.Fset, template, *verboseFlag)
	if err != nil {
		return err
	}

	// Apply it to the input packages.
	var pkgs []*loader.PackageInfo
	if *transitiveFlag {
		for _, info := range iprog.AllPackages {
			pkgs = append(pkgs, info)
		}
	} else {
		pkgs = iprog.InitialPackages()
	}
	var hadErrors bool
	for _, pkg := range pkgs {
		if pkg == template {
			continue
		}
		for _, file := range pkg.Files {
			n := xform.Transform(&pkg.Info, pkg.Pkg, file)
			if n == 0 {
				continue
			}
			filename := iprog.Fset.File(file.Pos()).Name()
			fmt.Fprintf(os.Stderr, "=== %s (%d matches):\n", filename, n)
			if *writeFlag {
				if err := eg.WriteAST(iprog.Fset, filename, file); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %s\n", err)
					hadErrors = true
				}
			} else {
				printer.Fprint(os.Stdout, iprog.Fset, file)
			}
		}
	}
	if hadErrors {
		os.Exit(1)
	}
	return nil
}
