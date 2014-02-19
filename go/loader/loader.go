// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package loader loads, parses and type-checks packages of Go code
// plus their transitive closure, and retains both the ASTs and the
// derived facts.
//
// THIS INTERFACE IS EXPERIMENTAL AND IS LIKELY TO CHANGE.
//
// The package defines two primary types: Config, which specifies a
// set of initial packages to load and various other options; and
// Program, which is the result of successfully loading the packages
// specified by a configuration.
//
// The configuration can be set directly, but *Config provides various
// convenience methods to simplify the common cases, each of which can
// be called any number of times.  Finally, these are followed by a
// call to Load() to actually load and type-check the program.
//
//      var conf loader.Config
//
//      // Use the command-line arguments to specify
//      // a set of initial packages to load from source.
//      // See FromArgsUsage for help.
//      rest, err := conf.FromArgs(os.Args[1:], wantTests)
//
//      // Parse the specified files and create an ad-hoc package with path "foo".
//      // All files must have the same 'package' declaration.
//      err := conf.CreateFromFilenames("foo", "foo.go", "bar.go")
//
//      // Create an ad-hoc package with path "foo" from
//      // the specified already-parsed files.
//      // All ASTs must have the same 'package' declaration.
//      err := conf.CreateFromFiles("foo", parsedFiles)
//
//      // Add "runtime" to the set of packages to be loaded.
//      conf.Import("runtime")
//
//      // Adds "fmt" and "fmt_test" to the set of packages
//      // to be loaded.  "fmt" will include *_test.go files.
//      err := conf.ImportWithTests("fmt")
//
//      // Finally, load all the packages specified by the configuration.
//      prog, err := conf.Load()
//
//
// CONCEPTS AND TERMINOLOGY
//
// An AD-HOC package is one specified as a set of source files on the
// command line.  In the simplest case, it may consist of a single file
// such as src/pkg/net/http/triv.go.
//
// EXTERNAL TEST packages are those comprised of a set of *_test.go
// files all with the same 'package foo_test' declaration, all in the
// same directory.  (go/build.Package calls these files XTestFiles.)
//
// An IMPORTABLE package is one that can be referred to by some import
// spec.  The Path() of each importable package is unique within a
// Program.
//
// Ad-hoc packages and external test packages are NON-IMPORTABLE.  The
// Path() of an ad-hoc package is inferred from the package
// declarations of its files and is therefore not a unique package key.
// For example, Config.CreatePkgs may specify two initial ad-hoc
// packages both called "main".
//
// An AUGMENTED package is an importable package P plus all the
// *_test.go files with same 'package foo' declaration as P.
// (go/build.Package calls these files TestFiles.)
// An external test package may depend upon members of the augmented
// package that are not in the unaugmented package, such as functions
// that expose internals.  (See bufio/export_test.go for an example.)
// So, the loader must ensure that for each external test package
// it loads, it also augments the corresponding non-test package.
//
// The import graph over n unaugmented packages must be acyclic; the
// import graph over n-1 unaugmented packages plus one augmented
// package must also be acyclic.  ('go test' relies on this.)  But the
// import graph over n augmented packages may contain cycles, and
// currently, go/types is incapable of handling such inputs, so the
// loader will only augment (and create an external test package
// for) the first import path specified on the command-line.
//
// The INITIAL packages are those specified in the configuration.  A
// DEPENDENCY is a package loaded to satisfy an import in an initial
// package or another dependency.
//
package loader

// TODO(adonovan):
// - (*Config).ParseFile is very handy, but feels like feature creep.
//   (*Config).CreateFromFiles has a nasty precondition.
// - Ideally some of this logic would move under the umbrella of
//   go/types; see bug 7114.
// - s/path/importPath/g to avoid ambiguity with other meanings of
//   "path": a file name, a colon-separated directory list.
// - Thorough overhaul of package documentation.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"strings"

	"code.google.com/p/go.tools/astutil"
	"code.google.com/p/go.tools/go/gcimporter"
	"code.google.com/p/go.tools/go/types"
)

// Config specifies the configuration for a program to load.
// The zero value for Config is a ready-to-use default configuration.
type Config struct {
	// Fset is the file set for the parser to use when loading the
	// program.  If nil, it will be lazily initialized by any
	// method of Config.
	Fset *token.FileSet

	// TypeChecker contains options relating to the type checker.
	//
	// The supplied IgnoreFuncBodies is not used; the effective
	// value comes from the TypeCheckFuncBodies func below.
	//
	// TypeChecker.Packages is lazily initialized during Load.
	TypeChecker types.Config

	// TypeCheckFuncBodies is a predicate over package import
	// paths.  A package for which the predicate is false will
	// have its package-level declarations type checked, but not
	// its function bodies; this can be used to quickly load
	// dependencies from source.  If nil, all func bodies are type
	// checked.
	TypeCheckFuncBodies func(string) bool

	// SourceImports determines whether to satisfy dependencies by
	// loading Go source code.
	//
	// If true, the entire program---the initial packages and
	// their transitive closure of dependencies---will be loaded,
	// parsed and type-checked.  This is required for
	// whole-program analyses such as pointer analysis.
	//
	// If false, the TypeChecker.Import mechanism will be used
	// instead.  Since that typically supplies only the types of
	// package-level declarations and values of constants, but no
	// code, it will not yield a whole program.  It is intended
	// for analyses that perform intraprocedural analysis of a
	// single package, e.g. traditional compilation.
	//
	// The initial packages (CreatePkgs and ImportPkgs) are always
	// loaded from Go source, regardless of this flag's setting.
	SourceImports bool

	// If Build is non-nil, it is used to locate source packages.
	// Otherwise &build.Default is used.
	Build *build.Context

	// If AllowTypeErrors is true, Load will return a Program even
	// if some of the its packages contained type errors; such
	// errors are accessible via PackageInfo.TypeError.
	// If false, Load will fail if any package had a type error.
	AllowTypeErrors bool

	// CreatePkgs specifies a list of non-importable initial
	// packages to create.  Each element specifies a list of
	// parsed files to be type-checked into a new package, and a
	// path for that package.  If the path is "", the package's
	// name will be used instead.  The path needn't be globally
	// unique.
	//
	// The resulting packages will appear in the corresponding
	// elements of the Program.Created slice.
	CreatePkgs []CreatePkg

	// ImportPkgs specifies a set of initial packages to load from
	// source.  The map keys are package import paths, used to
	// locate the package relative to $GOROOT.  The corresponding
	// values indicate whether to augment the package by *_test.go
	// files.
	//
	// Due to current type-checker limitations, at most one entry
	// may be augmented (true).
	ImportPkgs map[string]bool
}

type CreatePkg struct {
	Path  string
	Files []*ast.File
}

// A Program is a Go program loaded from source or binary
// as specified by a Config.
type Program struct {
	Fset *token.FileSet // the file set for this program

	// Created[i] contains the initial package whose ASTs were
	// supplied by Config.CreatePkgs[i].
	Created []*PackageInfo

	// Imported contains the initially imported packages,
	// as specified by Config.ImportPkgs.
	Imported map[string]*PackageInfo

	// ImportMap is the canonical mapping of import paths to
	// packages used by the type-checker (Config.TypeChecker.Packages).
	// It contains all Imported initial packages, but not Created
	// ones, and all imported dependencies.
	ImportMap map[string]*types.Package

	// AllPackages contains the PackageInfo of every package
	// encountered by Load: all initial packages and all
	// dependencies, including incomplete ones.
	AllPackages map[*types.Package]*PackageInfo
}

func (conf *Config) fset() *token.FileSet {
	if conf.Fset == nil {
		conf.Fset = token.NewFileSet()
	}
	return conf.Fset
}

// ParseFile is a convenience function that invokes the parser using
// the Config's FileSet, which is initialized if nil.
//
func (conf *Config) ParseFile(filename string, src interface{}, mode parser.Mode) (*ast.File, error) {
	return parser.ParseFile(conf.fset(), filename, src, mode)
}

// FromArgsUsage is a partial usage message that applications calling
// FromArgs may wish to include in their -help output.
const FromArgsUsage = `
<args> is a list of arguments denoting a set of initial packages.
It may take one of two forms:

1. A list of *.go source files.

   All of the specified files are loaded, parsed and type-checked
   as a single package.  All the files must belong to the same directory.

2. A list of import paths, each denoting a package.

   The package's directory is found relative to the $GOROOT and
   $GOPATH using similar logic to 'go build', and the *.go files in
   that directory are loaded, parsed and type-checked as a single
   package.

   In addition, all *_test.go files in the directory are then loaded
   and parsed.  Those files whose package declaration equals that of
   the non-*_test.go files are included in the primary package.  Test
   files whose package declaration ends with "_test" are type-checked
   as another package, the 'external' test package, so that a single
   import path may denote two packages.  (Whether this behaviour is
   enabled is tool-specific, and may depend on additional flags.)

   Due to current limitations in the type-checker, only the first
   import path of the command line will contribute any tests.

A '--' argument terminates the list of packages.
`

// FromArgs interprets args as a set of initial packages to load from
// source and updates the configuration.  It returns the list of
// unconsumed arguments.
//
// It is intended for use in command-line interfaces that require a
// set of initial packages to be specified; see FromArgsUsage message
// for details.
//
func (conf *Config) FromArgs(args []string, xtest bool) (rest []string, err error) {
	for i, arg := range args {
		if arg == "--" {
			rest = args[i+1:]
			args = args[:i]
			break // consume "--" and return the remaining args
		}
	}

	if len(args) > 0 && strings.HasSuffix(args[0], ".go") {
		// Assume args is a list of a *.go files
		// denoting a single ad-hoc package.
		for _, arg := range args {
			if !strings.HasSuffix(arg, ".go") {
				return nil, fmt.Errorf("named files must be .go files: %s", arg)
			}
		}
		err = conf.CreateFromFilenames("", args...)
	} else {
		// Assume args are directories each denoting a
		// package and (perhaps) an external test, iff xtest.
		for _, arg := range args {
			if xtest {
				err = conf.ImportWithTests(arg)
				if err != nil {
					break
				}
			} else {
				conf.Import(arg)
			}
		}
	}

	return
}

// CreateFromFilenames is a convenience function that parses the
// specified *.go files and adds a package entry for them to
// conf.CreatePkgs.
//
func (conf *Config) CreateFromFilenames(path string, filenames ...string) error {
	files, err := parseFiles(conf.fset(), ".", filenames...)
	if err != nil {
		return err
	}

	conf.CreateFromFiles(path, files...)
	return nil
}

// CreateFromFiles is a convenience function that adds a CreatePkgs
// entry to create package of the specified path and parsed files.
//
// Precondition: conf.Fset is non-nil and was the fileset used to parse
// the files.  (e.g. the files came from conf.ParseFile().)
//
func (conf *Config) CreateFromFiles(path string, files ...*ast.File) {
	if conf.Fset == nil {
		panic("nil Fset")
	}
	conf.CreatePkgs = append(conf.CreatePkgs, CreatePkg{path, files})
}

// ImportWithTests is a convenience function that adds path to
// ImportPkgs, the set of initial source packages located relative to
// $GOPATH.  The package will be augmented by any *_test.go files in
// its directory that contain a "package x" (not "package x_test")
// declaration.
//
// In addition, if any *_test.go files contain a "package x_test"
// declaration, an additional package comprising just those files will
// be added to CreatePkgs.
//
func (conf *Config) ImportWithTests(path string) error {
	if path == "unsafe" {
		return nil // ignore; not a real package
	}
	conf.Import(path)

	// TODO(adonovan): due to limitations of the current type
	// checker design, we can augment at most one package.
	for _, augmented := range conf.ImportPkgs {
		if augmented {
			return nil // don't attempt a second
		}
	}

	// Load the external test package.
	xtestFiles, err := parsePackageFiles(conf.build(), conf.fset(), path, "x")
	if err != nil {
		return err
	}
	if len(xtestFiles) > 0 {
		conf.CreateFromFiles(path+"_test", xtestFiles...)
	}

	// Mark the non-xtest package for augmentation with
	// in-package *_test.go files when we import it below.
	conf.ImportPkgs[path] = true
	return nil
}

// Import is a convenience function that adds path to ImportPkgs, the
// set of initial packages that will be imported from source.
//
func (conf *Config) Import(path string) {
	if path == "unsafe" {
		return // ignore; not a real package
	}
	if conf.ImportPkgs == nil {
		conf.ImportPkgs = make(map[string]bool)
	}
	// Subtle: adds value 'false' unless value is already true.
	conf.ImportPkgs[path] = conf.ImportPkgs[path] // unaugmented source package
}

// PathEnclosingInterval returns the PackageInfo and ast.Node that
// contain source interval [start, end), and all the node's ancestors
// up to the AST root.  It searches all ast.Files of all packages in prog.
// exact is defined as for astutil.PathEnclosingInterval.
//
// The zero value is returned if not found.
//
func (prog *Program) PathEnclosingInterval(start, end token.Pos) (pkg *PackageInfo, path []ast.Node, exact bool) {
	for _, info := range prog.AllPackages {
		for _, f := range info.Files {
			if !tokenFileContainsPos(prog.Fset.File(f.Pos()), start) {
				continue
			}
			if path, exact := astutil.PathEnclosingInterval(f, start, end); path != nil {
				return info, path, exact
			}
		}
	}
	return nil, nil, false
}

// InitialPackages returns a new slice containing the set of initial
// packages (Created + Imported) in unspecified order.
//
func (prog *Program) InitialPackages() []*PackageInfo {
	infos := make([]*PackageInfo, 0, len(prog.Created)+len(prog.Imported))
	infos = append(infos, prog.Created...)
	for _, info := range prog.Imported {
		infos = append(infos, info)
	}
	return infos
}

// ---------- Implementation ----------

// importer holds the working state of the algorithm.
type importer struct {
	conf     *Config                // the client configuration
	prog     *Program               // resulting program
	imported map[string]*importInfo // all imported packages (incl. failures) by import path
}

// importInfo tracks the success or failure of a single import.
type importInfo struct {
	info *PackageInfo // results of typechecking (including type errors)
	err  error        // reason for failure to make a package
}

// Load creates the initial packages specified by conf.{Create,Import}Pkgs,
// loading their dependencies packages as needed.
//
// On success, Load returns a Program containing a PackageInfo for
// each package.  On failure, it returns an error.
//
// If conf.AllowTypeErrors is set, a type error does not cause Load to
// fail, but is recorded in the PackageInfo.TypeError field.
//
// It is an error if no packages were loaded.
//
func (conf *Config) Load() (*Program, error) {
	// Initialize by setting the conf's copy, so all copies of
	// TypeChecker agree on the identity of the map.
	if conf.TypeChecker.Packages == nil {
		conf.TypeChecker.Packages = make(map[string]*types.Package)
	}

	prog := &Program{
		Fset:        conf.fset(),
		Imported:    make(map[string]*PackageInfo),
		ImportMap:   conf.TypeChecker.Packages,
		AllPackages: make(map[*types.Package]*PackageInfo),
	}

	imp := importer{
		conf:     conf,
		prog:     prog,
		imported: make(map[string]*importInfo),
	}

	for path := range conf.ImportPkgs {
		info, err := imp.importPackage(path)
		if err != nil {
			// TODO(adonovan): don't abort Load just
			// because of a parse error in one package.
			return nil, err // e.g. parse error (but not type error)
		}
		prog.Imported[path] = info
	}

	for _, create := range conf.CreatePkgs {
		path := create.Path
		if create.Path == "" && len(create.Files) > 0 {
			path = create.Files[0].Name.Name
		}
		prog.Created = append(prog.Created, imp.createPackage(path, create.Files...))
	}

	if len(prog.Imported)+len(prog.Created) == 0 {
		return nil, errors.New("no initial packages were specified")
	}

	// Create infos for indirectly imported packages.
	// e.g. incomplete packages without syntax, loaded from export data.
	for _, obj := range prog.ImportMap {
		if prog.AllPackages[obj] == nil {
			prog.AllPackages[obj] = &PackageInfo{Pkg: obj, Importable: true}
		}
	}

	if !conf.AllowTypeErrors {
		// Report errors in indirectly imported packages.
		var errpkgs []string
		for _, info := range prog.AllPackages {
			if info.TypeError != nil {
				errpkgs = append(errpkgs, info.Pkg.Path())
			}
		}
		if errpkgs != nil {
			return nil, fmt.Errorf("couldn't load packages due to type errors: %s",
				strings.Join(errpkgs, ", "))
		}
	}

	markErrorFreePackages(prog.AllPackages)

	return prog, nil
}

// markErrorFreePackages sets the TransitivelyErrorFree flag on all
// applicable packages.
func markErrorFreePackages(allPackages map[*types.Package]*PackageInfo) {
	// Build the transpose of the import graph.
	importedBy := make(map[*types.Package]map[*types.Package]bool)
	for P := range allPackages {
		for _, Q := range P.Imports() {
			clients, ok := importedBy[Q]
			if !ok {
				clients = make(map[*types.Package]bool)
				importedBy[Q] = clients
			}
			clients[P] = true
		}
	}

	// Find all packages reachable from some error package.
	reachable := make(map[*types.Package]bool)
	var visit func(*types.Package)
	visit = func(p *types.Package) {
		if !reachable[p] {
			reachable[p] = true
			for q := range importedBy[p] {
				visit(q)
			}
		}
	}
	for _, info := range allPackages {
		if info.TypeError != nil {
			visit(info.Pkg)
		}
	}

	// Mark the others as "transitively error-free".
	for _, info := range allPackages {
		if !reachable[info.Pkg] {
			info.TransitivelyErrorFree = true
		}
	}
}

// build returns the effective build context.
func (conf *Config) build() *build.Context {
	if conf.Build != nil {
		return conf.Build
	}
	return &build.Default
}

// doImport imports the package denoted by path.
// It implements the types.Importer signature.
//
// imports is the import map of the importing package, later
// accessible as types.Package.Imports().  If non-nil, doImport will
// update it to include this import.  (It may be nil in recursive
// calls for prefetching.)
//
// It returns an error if a package could not be created
// (e.g. go/build or parse error), but type errors are reported via
// the types.Config.Error callback (the first of which is also saved
// in the package's PackageInfo).
//
// Idempotent.
//
func (imp *importer) doImport(imports map[string]*types.Package, path string) (*types.Package, error) {
	// Package unsafe is handled specially, and has no PackageInfo.
	if path == "unsafe" {
		return types.Unsafe, nil
	}

	info, err := imp.importPackage(path)
	if err != nil {
		return nil, err
	}

	// Update the type checker's package map on success.
	imports[path] = info.Pkg

	return info.Pkg, nil
}

// importPackage imports the package with the given import path, plus
// its dependencies.
//
// Precondition: path != "unsafe".
//
func (imp *importer) importPackage(path string) (*PackageInfo, error) {
	ii, ok := imp.imported[path]
	if !ok {
		// In preorder, initialize the map entry to a cycle
		// error in case importPackage(path) is called again
		// before the import is completed.
		// TODO(adonovan): go/types should be responsible for
		// reporting cycles; see bug 7114.
		ii = &importInfo{err: fmt.Errorf("import cycle in package %s", path)}
		imp.imported[path] = ii

		// Find and create the actual package.
		if augment, ok := imp.conf.ImportPkgs[path]; ok || imp.conf.SourceImports {
			which := "g" // load *.go files
			if augment {
				which = "gt" // augment package by in-package *_test.go files
			}

			ii.info, ii.err = imp.importFromSource(path, which)
		} else {
			ii.info, ii.err = imp.importFromBinary(path)
		}
		if ii.info != nil {
			ii.info.Importable = true
		}
	}

	return ii.info, ii.err
}

// importFromBinary implements package loading from the client-supplied
// external source, e.g. object files from the gc compiler.
//
func (imp *importer) importFromBinary(path string) (*PackageInfo, error) {
	// Determine the caller's effective Import function.
	importfn := imp.conf.TypeChecker.Import
	if importfn == nil {
		importfn = gcimporter.Import
	}
	pkg, err := importfn(imp.conf.TypeChecker.Packages, path)
	if err != nil {
		return nil, err
	}
	info := &PackageInfo{Pkg: pkg}
	imp.prog.AllPackages[pkg] = info
	return info, nil
}

// importFromSource implements package loading by parsing Go source files
// located by go/build.  which indicates which files to include in the
// package.
//
func (imp *importer) importFromSource(path string, which string) (*PackageInfo, error) {
	files, err := parsePackageFiles(imp.conf.build(), imp.conf.fset(), path, which)
	if err != nil {
		return nil, err
	}
	// Type-check the package.
	return imp.createPackage(path, files...), nil
}

// createPackage creates and type-checks a package from the specified
// list of parsed files, importing their dependencies.  It returns a
// PackageInfo containing the resulting types.Package, the ASTs, and
// other type information.
//
// The order of files determines the package initialization order.
//
// path will be the resulting package's Path().
// For an ad-hoc package, this is not necessarily unique.
//
// The resulting package is accessible via AllPackages but is not
// importable, i.e. no 'import' spec can resolve to it.
//
// createPackage never fails, but the resulting package may contain type
// errors; the first of these is recorded in PackageInfo.TypeError.
//
func (imp *importer) createPackage(path string, files ...*ast.File) *PackageInfo {
	info := &PackageInfo{
		Files: files,
		Info: types.Info{
			Types:      make(map[ast.Expr]types.TypeAndValue),
			Objects:    make(map[*ast.Ident]types.Object),
			Implicits:  make(map[ast.Node]types.Object),
			Scopes:     make(map[ast.Node]*types.Scope),
			Selections: make(map[*ast.SelectorExpr]*types.Selection),
		},
	}

	// Use a copy of the types.Config so we can vary IgnoreFuncBodies.
	tc := imp.conf.TypeChecker
	tc.IgnoreFuncBodies = false
	if f := imp.conf.TypeCheckFuncBodies; f != nil {
		tc.IgnoreFuncBodies = !f(path)
	}
	if tc.Error == nil {
		// TODO(adonovan): also save errors in the PackageInfo?
		tc.Error = func(e error) { fmt.Fprintln(os.Stderr, e) }
	}
	tc.Import = imp.doImport // doImport wraps the user's importfn, effectively
	info.Pkg, info.TypeError = tc.Check(path, imp.conf.fset(), files, &info.Info)
	imp.prog.AllPackages[info.Pkg] = info
	return info
}
