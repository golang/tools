// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package vetfile defines the syntax of go.vet files, whose
// directives enable analyzers in go test (GOEXPERIMENT=vetfiles), and
// the directory structure that determines the packages to which they
// apply.
//
// Copied from cmd/go/internal/vetfile (CL 866033, patchset 4) with local
// replacements for cmd/go/internal/{cfg,str}.
package vetfile

import (
	"errors"
	"fmt"
	"go/token"
	"iter"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// Name is the name of the file that holds the analysis
// declarations of its directory. Its syntax follows that of go.mod
// files (see [Parse]). Unlike go.mod, it may appear in any
// directory, including one that contains no package.
const Name = "go.vet"

// TODO(adonovan): when go.vet files leave the experiment, gate them
// on the go version of the declaring module: an error in a main
// module, ignored in a dependency.

// A File is the parsed form of a go.vet file.
type File struct {
	Filename    string       // name of the file, for display
	Decls       []*Decl      // valid declarations, in order
	Diagnostics []Diagnostic // errors (syntax errors and invalid declarations), in order
}

// A Decl is a declaration in a go.vet file:
//
//	enable  SCOPE REF
//	disable SCOPE REF
//
// where SCOPE is ".", "./...", or "importers" (not for disable), or
// "global" (not for disable, and only in GOROOT/src/go.vet), and
// REF is "pkg.Var", "pkg.Var[*]", or "pkg.Var[A,B,...]". A disable
// declaration may instead name analyzers by their Analyzer.Name, as a
// quoted string (e.g. `disable . "printf"`); since names are not
// unique, this disables every analyzer of that name. (An unquoted
// operand is always a reference, so a reference that lacks its
// package path is an error, not a disable by name.)
//
// Declarations may also be grouped in blocks, as in go.mod:
//
//	enable (
//		./...     example.com/check.Housekeeping
//		importers example.com/check.Query
//	)
type Decl struct {
	Enable  bool     // enable or disable
	Scope   string   // ".", "./...", "importers", or "global"
	Var     Var      // the referenced variable
	Members []string // nil for an analyzer (pkg.Var); ["*"] or names for a suite (pkg.Var[...])
	Name    string   // for a disable by name: the Analyzer.Name (Var and Members are empty)

	File *File            // the declaring file, or nil for a synthesized declaration (e.g. from the -vet flag)
	Pos  modfile.Position // position of the declaration's first token, within File
}

// Filename returns the name of the declaring file, or "" for a
// synthesized declaration.
func (decl *Decl) Filename() string {
	if decl.File == nil {
		return ""
	}
	return decl.File.Filename
}

// Position returns the position of the declaration as "file:line:col",
// or "" for a synthesized declaration.
func (decl *Decl) Position() string {
	return formatPos(decl.File, decl.Pos)
}

// Verb returns "enable" or "disable".
func (decl *Decl) Verb() string {
	if decl.Enable {
		return "enable"
	}
	return "disable"
}

// Ref returns the reference as written, e.g. "pkgpath.Var[A,B]", or
// the quoted analyzer name of a disable by name.
func (decl *Decl) Ref() string {
	if decl.Name != "" {
		return strconv.Quote(decl.Name)
	}
	ref := decl.Var.String()
	if decl.Members != nil {
		ref += "[" + strings.Join(decl.Members, ",") + "]"
	}
	return ref
}

func (decl *Decl) String() string {
	return decl.Verb() + " " + decl.Scope + " " + decl.Ref()
}

// A Var identifies an exported analyzer or suite variable,
// pkgpath.Name, referenced by a declaration.
type Var struct {
	PkgPath string
	Name    string
}

func (v Var) String() string { return v.PkgPath + "." + v.Name }

// A Diagnostic is an error in a go.vet file, at the specified
// position. (Pos is zero if it concerns the whole file.) It reports a
// configuration error, not an analysis finding.
//
// Diagnostics are errors, and cause go test to fail (exit nonzero),
// but they do not prevent partial progress: a go.vet file with
// diagnostics is still a result, whose valid declarations take effect.
type Diagnostic struct {
	File    *File
	Pos     modfile.Position // within File
	Message string
}

func (d Diagnostic) String() string {
	return formatPos(d.File, d.Pos) + ": " + d.Message
}

// formatPos formats a position within a file as "file:line:col", or
// "file" if pos is zero, or "" if file is nil. The column is in runes,
// as in go.mod errors.
func formatPos(file *File, pos modfile.Position) string {
	switch {
	case file == nil:
		return ""
	case pos.Line == 0:
		return file.Filename
	default:
		return fmt.Sprintf("%s:%d:%d", file.Filename, pos.Line, pos.LineRune)
	}
}

// Parse parses the contents of a go.vet file. It returns the
// valid declarations, in order, and diagnostics for the invalid ones;
// if the file is not syntactically valid, it returns only diagnostics.
// It cannot fail. filename is used in positions, so it should be the
// name to display.
//
// The file is parsed with the go.mod parser, so the lexical and
// syntactic conventions (comments, quoting, blocks) are those of
// go.mod. Since the parser treats '[', ']', and ',' as separate
// tokens, white space within a reference's member list is not
// significant: "pkg.Var[A, B]" means "pkg.Var[A,B]".
//
// TODO(adonovan): this is a stopgap for the prototype. It uses
// [modfile.ParseLax], which ignores unknown verbs but retains the
// syntax tree, so that we can reuse the go.mod lexer and parser
// without changing x/mod. (A drawback is that a go.mod verb such as
// "require" is interpreted as such before we reject it, and lexical
// errors mention "mod files".) When the GOEXPERIMENT proposal is
// approved, add a third dialect to x/mod/modfile, alongside go.mod
// (Parse) and go.work (ParseWork), documented as experimental, and
// vendor it into cmd. Its API would be that of [File] and
// [Decl], plus the syntax tree:
//
//	// A VetFile is the parsed, interpreted form of a go.vet file.
//	type VetFile struct {
//		Decl   []*VetDecl
//		Syntax *FileSyntax
//	}
//
//	// A VetDecl is a single enable or disable declaration.
//	type VetDecl struct {
//		Enable  bool     // enable or disable
//		Scope   string   // ".", "./...", "importers", or "global"
//		PkgPath string   // package path of the referenced variable
//		Var     string   // name of the referenced variable
//		Members []string // nil (analyzer), ["*"], or member names (suite)
//		Name    string   // Analyzer.Name, for a disable by name
//		Syntax  *Line
//	}
//
//	// ParseVet parses and returns a go.vet file.
//	//
//	// file is the name of the file, used in positions and errors.
//	//
//	// data is the content of the file.
//	func ParseVet(file string, data []byte) (*VetFile, error)
//
// Like ParseWork it would be built on the unexported parse function
// (and so would not interpret go.mod verbs), and Format would work on
// its Syntax. The logic below that interprets the syntax tree would
// move there largely unchanged.
//
// The shared lexer's "mod files must use // comments" errors (two in
// read.go) should then use neutral wording, e.g. "must use //
// comments (not /* */ comments)": errors already begin with
// "file:line:", and go.work has the same problem today. That change
// could land in x/mod independently.
func Parse(filename string, data []byte) *File {
	file := &File{Filename: filename}
	addDiag := func(pos modfile.Position, msg string) {
		file.Diagnostics = append(file.Diagnostics, Diagnostic{file, pos, msg})
	}

	f, err := modfile.ParseLax(filename, data, nil)
	if err != nil {
		if list, ok := errors.AsType[modfile.ErrorList](err); ok {
			for _, e := range list {
				addDiag(e.Pos, e.Err.Error())
			}
		} else {
			addDiag(modfile.Position{}, err.Error()) // whole file
		}
		return file
	}

	parseLine := func(verb string, line *modfile.Line, args []string) {
		d, err := parseDecl(verb, args)
		if err != nil {
			addDiag(line.Start, err.Error())
			return
		}
		d.File = file
		d.Pos = line.Start
		file.Decls = append(file.Decls, d)
	}
	for _, stmt := range f.Syntax.Stmt {
		switch x := stmt.(type) {
		case *modfile.Line:
			parseLine(x.Token[0], x, x.Token[1:])
		case *modfile.LineBlock: // e.g. "enable ( ... )"
			if len(x.Token) > 1 || !isVerb(x.Token[0]) {
				addDiag(x.Start, "unknown block type: "+x.Token[0])
				continue
			}
			for _, line := range x.Line {
				parseLine(x.Token[0], line, line.Token)
			}
		}
	}
	return file
}

func isVerb(verb string) bool { return verb == "enable" || verb == "disable" }

// parseDecl parses a declaration with the specified verb and
// operands (tokens). The result has no file or position.
func parseDecl(verb string, args []string) (*Decl, error) {
	if !isVerb(verb) {
		return nil, fmt.Errorf("unknown directive: %s (want enable or disable)", verb)
	}
	// A reference is a name, optionally followed by a bracketed
	// member list, which the lexer splits into tokens.
	if len(args) < 2 {
		return nil, fmt.Errorf("malformed %s declaration: want scope and reference (e.g. %q)", verb, verb+" . example.com/check.Analyzer")
	}
	if len(args) > 2 && args[2] != "[" {
		return nil, fmt.Errorf("malformed %s declaration: extra operand %q (want scope and reference)", verb, args[2])
	}

	d := &Decl{Enable: verb == "enable", Scope: args[0]}
	switch d.Scope {
	case ".", "./...":
	case "importers", "global":
		if !d.Enable {
			return nil, fmt.Errorf("disable does not accept scope %q (want . or ./...)", d.Scope)
		}
	default:
		if !d.Enable {
			return nil, fmt.Errorf("invalid scope %q in disable declaration (want . or ./...)", d.Scope)
		}
		return nil, fmt.Errorf("invalid scope %q in enable declaration (want ., ./..., or importers)", d.Scope)
	}

	// TODO(adonovan): parse the member list from the tokens
	// directly (name, "[", A, ",", B, "]") instead of joining them
	// and splitting the result again.
	ref := strings.Join(args[1:], "")

	// A quoted string is an Analyzer.Name, permitted only in
	// disable declarations.
	if ref != "" && (ref[0] == '"' || ref[0] == '`') {
		if d.Enable {
			return nil, fmt.Errorf("invalid reference %s: enable requires a qualified name pkgpath.Var (analyzer names may be used only in disable)", ref)
		}
		name, err := strconv.Unquote(ref)
		if err != nil {
			return nil, fmt.Errorf("invalid analyzer name %s: want a quoted identifier", ref)
		}
		if !token.IsIdentifier(name) {
			return nil, fmt.Errorf("invalid analyzer name %s: not an identifier", ref)
		}
		d.Name = name
		return d, nil
	}

	// suite? e.g. pkg.Var[x,y,z]
	name, list, ok := strings.Cut(ref, "[")
	if ok {
		list, ok = strings.CutSuffix(list, "]")
		if !ok {
			return nil, fmt.Errorf("invalid reference %q: missing ']'", ref)
		}
		if list == "*" {
			d.Members = []string{"*"}
		} else {
			d.Members = strings.Split(list, ",")
			seen := make(map[string]bool)
			for _, m := range d.Members {
				if m == "" {
					return nil, fmt.Errorf("invalid reference %q: empty member name", ref)
				}
				if !token.IsIdentifier(m) {
					return nil, fmt.Errorf("invalid reference %q: invalid member name %q", ref, m)
				}
				if seen[m] {
					return nil, fmt.Errorf("invalid reference %q: duplicate member name %q", ref, m)
				}
				seen[m] = true
			}
		}
	}
	d.Var.PkgPath, d.Var.Name, ok = cutLast(name, ".")
	if !ok {
		if !d.Enable && token.IsIdentifier(ref) {
			// Perhaps a name was intended.
			return nil, fmt.Errorf("invalid reference %q: want qualified name pkgpath.Var (to disable analyzers by name, quote it: %q)", ref, ref)
		}
		return nil, fmt.Errorf("invalid reference %q: want qualified name pkgpath.Var", ref)
	}
	if d.Var.PkgPath == "" {
		return nil, fmt.Errorf("invalid reference %q: empty package path", ref)
	}
	if err := module.CheckImportPath(d.Var.PkgPath); err != nil {
		return nil, fmt.Errorf("invalid reference %q: %v", ref, err)
	}
	if !token.IsIdentifier(d.Var.Name) || !token.IsExported(d.Var.Name) {
		return nil, fmt.Errorf("invalid reference %q: %q is not an exported identifier", ref, d.Var.Name)
	}
	return d, nil
}

// moduleRoot returns the root directory of the module whose
// path is modPath, given the directory dir of its package pkgPath.
// It works for modules in any location: a main module, the module
// cache, a replacement directory, or a vendor directory. It returns
// "" if pkgPath is not within modPath or dir does not end with the
// package's path relative to the module.
//
// For example, given the module cache directory
// dir="/gopath/pkg/mod/example.com/m@v1.0.0/a/b" of package
// pkgPath="example.com/m/a/b" of module modPath="example.com/m",
// it strips the relative path "a/b" from dir, returning
// "/gopath/pkg/mod/example.com/m@v1.0.0".
func moduleRoot(dir, pkgPath, modPath string) string {
	dir = filepath.Clean(dir)
	if pkgPath == modPath {
		return dir
	}
	rel, ok := strings.CutPrefix(pkgPath, modPath+"/")
	if !ok {
		return ""
	}
	root, ok := strings.CutSuffix(dir, string(filepath.Separator)+filepath.FromSlash(rel))
	if !ok {
		return ""
	}
	return root
}

// Dirs returns an iterator over the directories whose go.vet
// files may hold local declarations (".", "./...") that apply to the
// package in pkgdir, whose import path is pkgPath, and the import path
// of each, in order of decreasing specificity: pkgdir itself, then its
// ancestors up to and including the root of its tree, stopping early
// at a directory that ./... would not cross (a vendor or testdata
// directory, or one whose name begins with "." or "_").
//
// If goroot, the package is in GOROOT (whose src directory is gorootSrc),
// and the root is GOROOT/src/cmd (whose import path is "cmd") for a
// package in that tree, or otherwise GOROOT/src (whose import path is "").
// The scan continues above vendor directories, so that the declarations
// of GOROOT/src and GOROOT/src/cmd apply to the vendored packages of std
// and cmd too. Otherwise, the root is that of the package's module, whose
// path is modPath, wherever it is (see moduleRoot); Dirs yields
// nothing if modPath is "" or the root cannot be determined.
//
// The import path of each directory is that of the root, joined with
// the directory's path relative to the root. (For a vendored package,
// the import path of a directory is not a prefix of the package's own
// import path.)
func Dirs(pkgdir, pkgPath, modPath string, goroot bool, gorootSrc string) iter.Seq2[string, string] {
	pkgdir = filepath.Clean(pkgdir)
	var root, rootPath string
	switch {
	case goroot && gorootSrc != "":
		root = filepath.Clean(gorootSrc)
		if cmd := filepath.Join(root, "cmd"); hasFilePathPrefix(pkgdir, cmd) {
			root, rootPath = cmd, "cmd"
		}
	case modPath != "":
		root, rootPath = moduleRoot(pkgdir, pkgPath, modPath), modPath
	}
	return func(yield func(dir, dirPath string) bool) {
		if root == "" {
			return
		}
		for dir := pkgdir; hasFilePathPrefix(dir, root); dir = filepath.Dir(dir) {
			if !yield(dir, dirPath(root, rootPath, dir)) {
				return
			}
			// Stop at root, or at a directory that ./... would not cross.
			elem := filepath.Base(dir)
			if dir == root || elem == "testdata" || elem[0] == '.' || elem[0] == '_' ||
				elem == "vendor" && !goroot {
				return
			}
		}
	}
}

// dirPath returns the import path of directory dir, within
// root, whose import path is rootPath.
func dirPath(root, rootPath, dir string) string {
	rel, _ := filepath.Rel(root, dir) // (dir is within root)
	if rel == "." {
		return rootPath
	}
	return path.Join(rootPath, filepath.ToSlash(rel))
}

// Validate moves the declarations of f that are invalid in dir, whose
// import path is dirPath, to f.Diagnostics: those of scope "global"
// unless dir is gorootSrc (GOROOT/src), and those that refer to a
// package not visible from dir (see Visible).
func (f *File) Validate(dir, dirPath, gorootSrc string) {
	dir = filepath.Clean(dir)
	var src string
	var std bool
	if gorootSrc != "" {
		src = filepath.Clean(gorootSrc)
		std = hasFilePathPrefix(dir, src)
	}
	f.Decls = slices.DeleteFunc(f.Decls, func(d *Decl) bool {
		var msg string
		switch {
		case d.Scope == "global" && (src == "" || dir != src):
			msg = "enable global: the global scope is permitted only in GOROOT/src/go.vet"
		case d.Name == "" && !Visible(dirPath, std, d.Var.PkgPath):
			msg = fmt.Sprintf("%s %s: use of internal package %s not allowed", d.Verb(), d.Ref(), d.Var.PkgPath)
		default:
			return false
		}
		f.Diagnostics = append(f.Diagnostics, Diagnostic{f, d.Pos, msg})
		return true
	})
}

// Visible reports whether the package whose import path is
// pkgPath is visible, by the rule for internal packages, from the
// directory whose import path is from. fromStd reports whether that
// directory is in the standard library (GOROOT/src), whose top-level
// internal packages are visible throughout the standard library.
//
// A reference in a go.vet file must be visible from the file's
// directory. (An "importers" declaration applies to every importer,
// even one from which its analyzer is not visible; such an importer
// can disable the analyzer by name.)
//
// (The logic is that of load.disallowInternal, which applies to
// imports, specialized to import paths.)
//
// TODO(adonovan): move load.findInternal into this package (or str)
// so that disallowInternal and Visible share it.
func Visible(from string, fromStd bool, pkgPath string) bool {
	// The final "internal" element governs.
	parent, _, ok := cutLast("/"+pkgPath+"/", "/internal/")
	switch {
	case !ok:
		return true // not internal
	case parent == "":
		return fromStd // top-level internal package of std
	default:
		return hasPathPrefix(from, parent[len("/"):])
	}
}

// cutLast slices s around the last instance of sep, returning the text
// before and after sep. The found result reports whether sep appears
// in s. If sep does not appear in s, cutLast returns s, "", false.
//
// TODO(adonovan): use strings.CutLast once the bootstrap toolchain is
// go1.27 or later.
func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// hasPathPrefix reports whether the slash-separated path s
// begins with the elements in prefix.
// Copied from cmd/go/internal/str.HasPathPrefix.
func hasPathPrefix(s, prefix string) bool {
	if len(s) == len(prefix) {
		return s == prefix
	}
	if prefix == "" {
		return true
	}
	if len(s) > len(prefix) {
		if prefix[len(prefix)-1] == '/' || s[len(prefix)] == '/' {
			return s[:len(prefix)] == prefix
		}
	}
	return false
}

// hasFilePathPrefix reports whether the filesystem path s
// begins with the elements in prefix.
// Copied from cmd/go/internal/str.HasFilePathPrefix.
func hasFilePathPrefix(s, prefix string) bool {
	sv := filepath.VolumeName(s)
	pv := filepath.VolumeName(prefix)

	s = s[len(sv):]
	prefix = prefix[len(pv):]

	if sv != pv {
		sv = strings.ToUpper(sv)
		pv = strings.ToUpper(pv)
	}

	switch {
	default:
		return false
	case sv != pv:
		return false
	case len(s) == len(prefix):
		return s == prefix
	case prefix == "":
		return true
	case len(s) > len(prefix):
		if prefix[len(prefix)-1] == filepath.Separator {
			return strings.HasPrefix(s, prefix)
		}
		return s[len(prefix)] == filepath.Separator && s[:len(prefix)] == prefix
	}
}
