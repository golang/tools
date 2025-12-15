// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

// The generate command reads all the GOROOT/api/go1.*.txt files and
// generates a single combined manifest.go file containing the Go
// standard library API symbols along with versions.
//
// It also runs "go list -deps std" and records the import graph. This
// information may be used, for example, to ensure that tools don't
// suggest fixes that import package P when analyzing one of P's
// dependencies.
package main

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

func main() {
	log.SetFlags(log.Lshortfile) // to identify the source of the log messages

	dir := apidir()
	manifest(dir)
	deps()
}

// -- generate std manifest --

func manifest(apidir string) {
	// find the signatures
	cfg := packages.Config{
		Mode: packages.LoadTypes,
		Env:  append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64"),
	}
	// find the source. This is not totally reliable: different
	// systems may get different versions of unreleased APIs.
	// The result depends on the toolchain.
	// The x/tools release process regenerates the table
	// with the canonical toolchain.
	stdpkgs, err := packages.Load(&cfg, "std")
	if err != nil {
		log.Fatal(err)
	}
	signatures := make(map[string]map[string]string) // PkgPath->FuncName->signature
	// signatures start with func and may contain type parameters
	// "func[T comparable](value T) unique.Handle[T]"
	for _, pkg := range stdpkgs {
		if strings.HasPrefix(pkg.PkgPath, "vendor/") ||
			strings.HasPrefix(pkg.PkgPath, "internal/") ||
			strings.Contains(pkg.PkgPath, "/internal/") {
			continue
		}
		for _, name := range pkg.Types.Scope().Names() {
			fixer := func(p *types.Package) string {
				// fn.Signature() would have produced
				// "func(fi io/fs.FileInfo, link string) (*archive/tar.Header, error)"},
				// This produces
				// "func FileInfoHeader(fi fs.FileInfo, link string) (*Header, error)""
				// Note that the function name is superfluous, so it is removed below
				if p != pkg.Types {
					return p.Name()
				}
				return ""
			}
			obj := pkg.Types.Scope().Lookup(name)
			if fn, ok := obj.(*types.Func); ok {
				mp, ok := signatures[pkg.PkgPath]
				if !ok {
					mp = make(map[string]string)
					signatures[pkg.PkgPath] = mp
				}
				sig := types.ObjectString(fn, fixer)
				// remove the space and function name introduced by fixer
				sig = strings.Replace(sig, " "+name, "", 1)
				mp[name] = sig
			}
		}
	}

	// read the api data
	pkgs := make(map[string]map[string]symInfo) // package -> symbol -> info
	symRE := regexp.MustCompile(`^pkg (\S+).*?, (var|func|type|const|method \([^)]*\)) ([\pL\p{Nd}_]+)(.*)`)

	// parse parses symbols out of GOROOT/api/*.txt data, with the specified minor version.
	// Errors are reported against filename.
	parse := func(filename string, data []byte, minor int) {
		for linenum, line := range strings.Split(string(data), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			m := symRE.FindStringSubmatch(line)
			if m == nil {
				log.Fatalf("invalid input: %s:%d: %s", filename, linenum+1, line)
			}
			path, kind, sym, rest := m[1], m[2], m[3], m[4]

			if _, recv, ok := strings.Cut(kind, "method "); ok {
				// e.g. "method (*Func) Pos() token.Pos"
				kind = "method" // (concrete)

				recv := removeTypeParam(recv) // (*Foo[T]) -> (*Foo)

				sym = recv + "." + sym // (*T).m

			} else if method, ok := strings.CutPrefix(rest, " interface, "); ok && kind == "type" {
				// e.g. "pkg reflect, type Type interface, Comparable() bool"
				// or   "pkg net, type Error interface, Temporary //deprecated"

				kind = "method" // (abstract)

				if strings.HasPrefix(method, "unexported methods") {
					continue
				}
				if strings.Contains(method, " //deprecated") {
					continue
				}
				name, _, ok := strings.Cut(method, "(")
				if !ok {
					log.Printf("unexpected: %s", line)
					continue
				}
				sym = fmt.Sprintf("(%s).%s", sym, name) // (T).m

			} else if field, ok := strings.CutPrefix(rest, " struct, "); ok && kind == "type" {
				// e.g. "type ParenExpr struct, Lparen token.Pos"
				kind = "field"
				name, typ, _ := strings.Cut(field, " ")

				// The api script uses the name
				// "embedded" (ambiguously) for
				// the name of an anonymous field.
				if name == "embedded" {
					// Strip "*pkg.T" down to "T".
					typ = strings.TrimPrefix(typ, "*")
					if _, after, ok := strings.Cut(typ, "."); ok {
						typ = after
					}
					typ = removeTypeParam(typ) // embedded Foo[T] -> Foo
					name = typ
				}

				sym += "." + name // T.f
			}

			symbols, ok := pkgs[path]
			if !ok {
				symbols = make(map[string]symInfo)
				pkgs[path] = symbols
			}

			// Don't overwrite earlier entries:
			// enums are redeclared in later versions
			// as their encoding changes;
			// deprecations count as updates too.
			// TODO(adonovan): it would be better to mark
			// deprecated as a boolean without changing the
			// version.
			if _, ok := symbols[sym]; !ok {
				var sig string
				if kind == "func" {
					sig = signatures[path][sym]
				}
				symbols[sym] = symInfo{
					kind:      kind,
					minor:     minor,
					signature: sig,
				}
			}
		}
	}

	// Read and parse the GOROOT/api manifests.
	for minor := 0; ; minor++ {
		base := "go1.txt"
		if minor > 0 {
			base = fmt.Sprintf("go1.%d.txt", minor)
		}
		filename := filepath.Join(apidir, base)
		data, err := os.ReadFile(filename)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// All caught up.
				// Synthesize one final file from any api/next/*.txt fragments.
				// (They are consolidated into a go1.%d file some time between
				// the freeze and the first release candidate.)
				filenames, err := filepath.Glob(filepath.Join(apidir, "next", "*.txt"))
				if err != nil {
					log.Fatal(err)
				}
				var next bytes.Buffer
				for _, filename := range filenames {
					data, err := os.ReadFile(filename)
					if err != nil {
						log.Fatal(err)
					}
					next.Write(data)
				}
				parse(filename, next.Bytes(), minor) // (filename is a lie)
				break
			}
			log.Fatal(err)
		}
		parse(filename, data, minor)
	}

	// The APIs of the syscall/js and unsafe packages need to be computed explicitly,
	// because they're not included in the GOROOT/api/go1.*.txt files at this time.
	pkgs["syscall/js"] = loadSymbols("syscall/js", "GOOS=js", "GOARCH=wasm")
	pkgs["unsafe"] = exportedSymbols(types.Unsafe) // TODO(adonovan): set correct versions

	// Write the combined manifest.
	var buf bytes.Buffer
	buf.WriteString(`// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated by generate.go. DO NOT EDIT.

package stdlib

var PackageSymbols = map[string][]Symbol{
`)

	for _, path := range sortedKeys(pkgs) {
		pkg := pkgs[path]
		fmt.Fprintf(&buf, "\t%q: {\n", path)
		for _, name := range sortedKeys(pkg) {
			info := pkg[name]
			fmt.Fprintf(&buf, "\t\t{%q, %s, %d, %q},\n",
				name, strings.Title(info.kind), info.minor, info.signature)
		}
		fmt.Fprintln(&buf, "},")
	}
	fmt.Fprintln(&buf, "}")
	fmtbuf, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("manifest.go", fmtbuf, 0o666); err != nil {
		log.Fatal(err)
	}
}

// find the api directory, In most situations it is in GOROOT/api, but not always.
// TODO(pjw): understand where it might be, and if there could be newer and older versions
func apidir() string {
	stdout := new(bytes.Buffer)
	cmd := exec.Command("go", "env", "GOROOT", "GOPATH")
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
	// Prefer GOROOT/api over GOPATH/api.
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		apidir := filepath.Join(line, "api")
		info, err := os.Stat(apidir)
		if err == nil && info.IsDir() {
			return apidir
		}
	}
	log.Fatal("could not find api dir")
	return ""
}

type symInfo struct {
	kind  string // e.g. "func"
	minor int    // go1.%d
	// for completion snippets
	signature string // for Kind == stdlib.Func
}

// loadSymbols computes the exported symbols in the specified package
// by parsing and type-checking the current source.
func loadSymbols(pkg string, extraEnv ...string) map[string]symInfo {
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedTypes,
		Env:  append(os.Environ(), extraEnv...),
	}, pkg)
	if err != nil {
		log.Fatalln(err)
	} else if len(pkgs) != 1 {
		log.Fatalf("got %d packages, want one package %q", len(pkgs), pkg)
	}
	return exportedSymbols(pkgs[0].Types)
}

func exportedSymbols(pkg *types.Package) map[string]symInfo {
	symbols := make(map[string]symInfo)
	for _, name := range pkg.Scope().Names() {
		if obj := pkg.Scope().Lookup(name); obj.Exported() {
			var kind string
			switch obj.(type) {
			case *types.Func, *types.Builtin:
				kind = "func"
			case *types.Const:
				kind = "const"
			case *types.Var:
				kind = "var"
			case *types.TypeName:
				kind = "type"
				// TODO(adonovan): expand fields and methods of syscall/js.*
			default:
				log.Fatalf("unexpected object type: %v", obj)
			}
			symbols[name] = symInfo{kind: kind, minor: 0} // pretend go1.0
		}
	}
	return symbols
}

func sortedKeys[M ~map[K]V, K cmp.Ordered, V any](m M) []K {
	r := make([]K, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	slices.Sort(r)
	return r
}

func removeTypeParam(s string) string {
	i := strings.IndexByte(s, '[')
	j := strings.LastIndexByte(s, ']')
	if i > 0 && j > i {
		s = s[:i] + s[j+len("["):]
	}
	return s
}

// -- generate dependency graph --

func deps() {
	type Package struct {
		// go list JSON output
		ImportPath string   // import path of package in dir
		Imports    []string // import paths used by this package

		// encoding
		index int
		deps  []int // indices of direct imports, sorted
	}
	pkgs := make(map[string]*Package)
	var keys []string
	for dec := json.NewDecoder(runGo("list", "-deps", "-json", "std")); dec.More(); {
		var pkg Package
		if err := dec.Decode(&pkg); err != nil {
			log.Fatal(err)
		}
		pkgs[pkg.ImportPath] = &pkg
		keys = append(keys, pkg.ImportPath)
	}

	// Sort and number the packages.
	// There are 344 as of Mar 2025.
	slices.Sort(keys)
	for i, name := range keys {
		pkgs[name].index = i
	}

	// Encode the dependencies.
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			if imp == "C" {
				continue
			}
			pkg.deps = append(pkg.deps, pkgs[imp].index)
		}
		slices.Sort(pkg.deps)
	}

	// Emit the table.
	var buf bytes.Buffer
	buf.WriteString(`// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated by generate.go. DO NOT EDIT.

package stdlib

type pkginfo struct {
	name string
	deps string // list of indices of dependencies, as varint-encoded deltas
}
var deps = [...]pkginfo{
`)
	for _, name := range keys {
		prev := 0
		var deps []int
		for _, v := range pkgs[name].deps {
			deps = append(deps, v-prev) // delta
			prev = v
		}
		var data []byte
		for _, v := range deps {
			data = binary.AppendUvarint(data, uint64(v))
		}
		fmt.Fprintf(&buf, "\t{%q, %q},\n", name, data)
	}
	fmt.Fprintln(&buf, "}")

	// Also write the list of bootstrap packages.
	// (We can't use indices because it is not a subset of std.)
	bootstrap, version := bootstrap()
	minor := strings.Split(version, ".")[1] // "go1.2.3" -> "2"
	buf.WriteString(`
// bootstrap is the list of bootstrap packages extracted from cmd/dist.
var bootstrap = map[string]bool{
`)
	for _, pkg := range bootstrap {
		fmt.Fprintf(&buf, "\t%q: true,\n", pkg)
	}
	fmt.Fprintf(&buf, `}

// BootstrapVersion is the minor version of Go used during toolchain
// bootstrapping. Packages for which [IsBootstrapPackage] must not use
// features of Go newer than this version.
const BootstrapVersion = Version(%s) // %s
`, minor, version)

	// Format and update the dependencies file.
	fmtbuf, err := format.Source(buf.Bytes())
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("deps.go", fmtbuf, 0o666); err != nil {
		log.Fatal(err)
	}

	// Also generate the data for the test.
	for _, t := range [...]struct{ flag, filename string }{
		{"-deps=true", "testdata/nethttp.deps"},
		{`-f={{join .Imports "\n"}}`, "testdata/nethttp.imports"},
	} {
		stdout := new(bytes.Buffer)
		cmd := exec.Command("go", "list", t.flag, "net/http")
		cmd.Stdout = stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(t.filename, stdout.Bytes(), 0666); err != nil {
			log.Fatal(err)
		}
	}
}

// bootstrap returns the list of bootstrap packages out of the
// source of the dist command, along with the minimum toolchain
// version.
//
// We assume it is "var bootstrapDirs []string" in buildtool.go, and
// is a list of string literals, either package names or "dir/...".
// TODO(adonovan): find a more robust solution.
func bootstrap() ([]string, string) {
	fset := token.NewFileSet()
	filename := strings.TrimSpace(runGo("list", "-f={{.Dir}}/buildtool.go", "cmd/dist").String())
	f, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		log.Fatalf("can't parse buildtool.go file in cmd/dist package: %v", err)
	}

	const bootstrapVarName = "bootstrapDirs"
	var (
		args    = []string{"list"} // go list command to expand bootstrap packages
		version string
	)
	for _, decl := range f.Decls {
		decl, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range decl.Specs {
			spec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if len(spec.Names) != 1 {
				continue
			}
			switch spec.Names[0].Name {
			case bootstrapVarName:
				// var bootstrapDirs = []string{ ... }
				if len(spec.Values) != 1 {
					log.Fatalf("%s: %s var spec has %d values, want 1",
						fset.Position(spec.Pos()), len(spec.Values))
				}
				value0 := spec.Values[0]
				lit, ok := value0.(*ast.CompositeLit)
				if !ok {
					log.Fatalf("%s: %s assigned from %T, want slice literal",
						fset.Position(value0.Pos()), value0)
				}
				// Construct a go list command from the package patterns.
				for _, elt := range lit.Elts {
					lit, ok := elt.(*ast.BasicLit)
					if !ok {
						log.Fatalf("%s: element is %T, want string literal",
							fset.Position(elt.Pos()), elt)
					}
					pattern, err := strconv.Unquote(lit.Value)
					if err != nil {
						log.Fatalf("%s: %v", fset.Position(elt.Pos()), err)
					}
					args = append(args, pattern)
				}

			case "minBootstrap":
				// const minBootstrap = "go1.2.3"
				lit := spec.Values[0].(*ast.BasicLit)
				version, _ = strconv.Unquote(lit.Value)
			}
		}
	}
	if len(args) < 2 {
		log.Fatalf("can't find var %s in buildtool.go file in cmd/dist package: %v",
			bootstrapVarName, err)
	}
	if version == "" {
		log.Fatalf("can't find const minBootstrap version in buildtool.go file in cmd/dist package: %v",
			err)
	}

	return strings.Split(strings.TrimSpace(runGo(args...).String()), "\n"), version
}

func runGo(args ...string) *bytes.Buffer {
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	stdout, err := cmd.Output()
	if err != nil {
		log.Fatalf("%s: failed: %v", cmd, err)
	}
	return bytes.NewBuffer(stdout)
}
