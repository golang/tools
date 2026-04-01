// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// The objectpath command is a debugging tool that prints
// the object path of each symbol declared in a package.
//
// An object path is a unique identifier for an exported Go language symbol
// (function, type, variable, etc) relative to its declaring package.
// See [golang.org/x/tools/go/types/objectpath] for details.
//
// Usage: go run ./go/types/objectpath/main.go [packages...]
package main

import (
	"cmp"
	"flag"
	"fmt"
	"go/types"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/internal/typesinternal"
)

func main() {
	log.SetPrefix("objectpath: ")
	log.SetFlags(0)
	flag.Parse()

	exitcode := 0

	cfg := &packages.Config{Mode: packages.LoadSyntax}
	pkgs, err := packages.Load(cfg, flag.Args()...)
	if err != nil {
		log.Fatalf("packages.Load: %v", err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		exitcode = 1
	}

	for _, pkg := range pkgs {
		var (
			fset          = pkg.Fset
			objectpathFor = new(objectpath.Encoder).For
		)

		// objString formats a symbol relative to this package.
		objString := func(obj types.Object) string {
			return types.ObjectString(obj, func(p *types.Package) string {
				if p == pkg.Types {
					return ""
				}
				return p.Name()
			})
		}

		// print prints a symbol's name, declared location, and object path.
		print := func(obj types.Object) {
			var got string
			path, err := objectpathFor(obj)
			if err != nil {
				if mayFail(obj) {
					return
				}
				exitcode = 1
				got = "error: " + err.Error()
			} else {
				got = strconv.Quote(string(path))
			}

			posn := fset.Position(obj.Pos())
			posn.Filename = filepath.Base(posn.Filename)

			fmt.Printf("%20s: %v = %s\n", posn.String(), objString(obj), got)
		}

		// Gather all objects declared in this package, in source order.
		objs := slices.Collect(maps.Values(pkg.TypesInfo.Defs))
		objs = slices.DeleteFunc(objs, func(obj types.Object) bool {
			return obj == nil
		})
		slices.SortFunc(objs, func(x, y types.Object) int {
			return cmp.Compare(x.Pos(), y.Pos())
		})

		// Show each object's path.
		for _, obj := range objs {
			if obj != nil {
				print(obj)
			}
		}
	}

	if exitcode != 0 {
		log.Printf("there were errors")
	}
	os.Exit(exitcode)
}

// mayFail reports whether [objectpath.Encoder.For] is
// permitted to fail for this kind of symbol.
func mayFail(obj types.Object) bool {
	// "The For function guarantees to return a path only for the following objects:
	// - package-level types
	// - exported package-level non-types
	// - methods
	// - parameter and result variables
	// - struct fields"
	if typesinternal.IsPackageLevel(obj) && is[*types.TypeName](obj) {
		return false // package-level types
	}
	if obj.Exported() && typesinternal.IsPackageLevel(obj) && !is[*types.TypeName](obj) {
		return false // exported package-level non-types
	}

	// The remaining rules are inaccurate: clearly,
	// - local interface methods,
	// - params/results of local functions, and
	// - fields of local structs
	// cannot have object paths.
	// So don't enforce them for now.
	// TODO(adonovan): clarify the documentation and update this check.
	//
	// if fn, ok := obj.(*types.Func); ok && fn.Signature().Recv() != nil {
	// 	return false // methods
	// }
	// if v, ok := obj.(*types.Var); ok {
	// 	case types.ParamVar, types.ResultVar, types.RecvVar:
	// 		return false // parameter and result variables
	// 	case types.FieldVar:
	// 		return false // struct fields
	// 	}
	// }

	return true
}

func is[T any](x any) bool {
	_, ok := x.(T)
	return ok
}
