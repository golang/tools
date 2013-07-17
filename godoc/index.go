// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains the infrastructure to create an
// identifier and full-text index for a set of Go files.
//
// Algorithm for identifier index:
// - traverse all .go files of the file tree specified by root
// - for each identifier (word) encountered, collect all occurrences (spots)
//   into a list; this produces a list of spots for each word
// - reduce the lists: from a list of spots to a list of FileRuns,
//   and from a list of FileRuns into a list of PakRuns
// - make a HitList from the PakRuns
//
// Details:
// - keep two lists per word: one containing package-level declarations
//   that have snippets, and one containing all other spots
// - keep the snippets in a separate table indexed by snippet index
//   and store the snippet index in place of the line number in a SpotInfo
//   (the line number for spots with snippets is stored in the snippet)
// - at the end, create lists of alternative spellings for a given
//   word
//
// Algorithm for full text index:
// - concatenate all source code in a byte buffer (in memory)
// - add the files to a file set in lockstep as they are added to the byte
//   buffer such that a byte buffer offset corresponds to the Pos value for
//   that file location
// - create a suffix array from the concatenated sources
//
// String lookup in full text index:
// - use the suffix array to lookup a string's offsets - the offsets
//   correspond to the Pos values relative to the file set
// - translate the Pos values back into file and line information and
//   sort the result

package godoc

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"index/suffixarray"
	"io"
	"log"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	"code.google.com/p/go.tools/godoc/util"
)

// TODO(bradfitz,adg): legacy flag vars. clean up.
var (
	MaxResults = 1000

	// index throttle value; 0.0 = no time allocated, 1.0 = full throttle
	IndexThrottle float64 = 0.75

	// IndexFiles is a glob pattern specifying index files; if
	// not empty, the index is read from these files in sorted
	// order")
	IndexFiles string
)

// ----------------------------------------------------------------------------
// InterfaceSlice is a helper type for sorting interface
// slices according to some slice-specific sort criteria.

type comparer func(x, y interface{}) bool

type interfaceSlice struct {
	slice []interface{}
	less  comparer
}

// ----------------------------------------------------------------------------
// RunList

// A RunList is a list of entries that can be sorted according to some
// criteria. A RunList may be compressed by grouping "runs" of entries
// which are equal (according to the sort critera) into a new RunList of
// runs. For instance, a RunList containing pairs (x, y) may be compressed
// into a RunList containing pair runs (x, {y}) where each run consists of
// a list of y's with the same x.
type RunList []interface{}

func (h RunList) sort(less comparer) {
	sort.Sort(&interfaceSlice{h, less})
}

func (p *interfaceSlice) Len() int           { return len(p.slice) }
func (p *interfaceSlice) Less(i, j int) bool { return p.less(p.slice[i], p.slice[j]) }
func (p *interfaceSlice) Swap(i, j int)      { p.slice[i], p.slice[j] = p.slice[j], p.slice[i] }

// Compress entries which are the same according to a sort criteria
// (specified by less) into "runs".
func (h RunList) reduce(less comparer, newRun func(h RunList) interface{}) RunList {
	if len(h) == 0 {
		return nil
	}
	// len(h) > 0

	// create runs of entries with equal values
	h.sort(less)

	// for each run, make a new run object and collect them in a new RunList
	var hh RunList
	i, x := 0, h[0]
	for j, y := range h {
		if less(x, y) {
			hh = append(hh, newRun(h[i:j]))
			i, x = j, h[j] // start a new run
		}
	}
	// add final run, if any
	if i < len(h) {
		hh = append(hh, newRun(h[i:]))
	}

	return hh
}

// ----------------------------------------------------------------------------
// KindRun

// Debugging support. Disable to see multiple entries per line.
const removeDuplicates = true

// A KindRun is a run of SpotInfos of the same kind in a given file.
// The kind (3 bits) is stored in each SpotInfo element; to find the
// kind of a KindRun, look at any of it's elements.
type KindRun []SpotInfo

// KindRuns are sorted by line number or index. Since the isIndex bit
// is always the same for all infos in one list we can compare lori's.
func (k KindRun) Len() int           { return len(k) }
func (k KindRun) Less(i, j int) bool { return k[i].Lori() < k[j].Lori() }
func (k KindRun) Swap(i, j int)      { k[i], k[j] = k[j], k[i] }

// FileRun contents are sorted by Kind for the reduction into KindRuns.
func lessKind(x, y interface{}) bool { return x.(SpotInfo).Kind() < y.(SpotInfo).Kind() }

// newKindRun allocates a new KindRun from the SpotInfo run h.
func newKindRun(h RunList) interface{} {
	run := make(KindRun, len(h))
	for i, x := range h {
		run[i] = x.(SpotInfo)
	}

	// Spots were sorted by file and kind to create this run.
	// Within this run, sort them by line number or index.
	sort.Sort(run)

	if removeDuplicates {
		// Since both the lori and kind field must be
		// same for duplicates, and since the isIndex
		// bit is always the same for all infos in one
		// list we can simply compare the entire info.
		k := 0
		prev := SpotInfo(1<<32 - 1) // an unlikely value
		for _, x := range run {
			if x != prev {
				run[k] = x
				k++
				prev = x
			}
		}
		run = run[0:k]
	}

	return run
}

// ----------------------------------------------------------------------------
// FileRun

// A Pak describes a Go package.
type Pak struct {
	Path string // path of directory containing the package
	Name string // package name as declared by package clause
}

// Paks are sorted by name (primary key) and by import path (secondary key).
func (p *Pak) less(q *Pak) bool {
	return p.Name < q.Name || p.Name == q.Name && p.Path < q.Path
}

// A File describes a Go file.
type File struct {
	Name string // directory-local file name
	Pak  *Pak   // the package to which the file belongs
}

// Path returns the file path of f.
func (f *File) Path() string {
	return pathpkg.Join(f.Pak.Path, f.Name)
}

// A Spot describes a single occurrence of a word.
type Spot struct {
	File *File
	Info SpotInfo
}

// A FileRun is a list of KindRuns belonging to the same file.
type FileRun struct {
	File   *File
	Groups []KindRun
}

// Spots are sorted by file path for the reduction into FileRuns.
func lessSpot(x, y interface{}) bool {
	fx := x.(Spot).File
	fy := y.(Spot).File
	// same as "return fx.Path() < fy.Path()" but w/o computing the file path first
	px := fx.Pak.Path
	py := fy.Pak.Path
	return px < py || px == py && fx.Name < fy.Name
}

// newFileRun allocates a new FileRun from the Spot run h.
func newFileRun(h RunList) interface{} {
	file := h[0].(Spot).File

	// reduce the list of Spots into a list of KindRuns
	h1 := make(RunList, len(h))
	for i, x := range h {
		h1[i] = x.(Spot).Info
	}
	h2 := h1.reduce(lessKind, newKindRun)

	// create the FileRun
	groups := make([]KindRun, len(h2))
	for i, x := range h2 {
		groups[i] = x.(KindRun)
	}
	return &FileRun{file, groups}
}

// ----------------------------------------------------------------------------
// PakRun

// A PakRun describes a run of *FileRuns of a package.
type PakRun struct {
	Pak   *Pak
	Files []*FileRun
}

// Sorting support for files within a PakRun.
func (p *PakRun) Len() int           { return len(p.Files) }
func (p *PakRun) Less(i, j int) bool { return p.Files[i].File.Name < p.Files[j].File.Name }
func (p *PakRun) Swap(i, j int)      { p.Files[i], p.Files[j] = p.Files[j], p.Files[i] }

// FileRuns are sorted by package for the reduction into PakRuns.
func lessFileRun(x, y interface{}) bool {
	return x.(*FileRun).File.Pak.less(y.(*FileRun).File.Pak)
}

// newPakRun allocates a new PakRun from the *FileRun run h.
func newPakRun(h RunList) interface{} {
	pak := h[0].(*FileRun).File.Pak
	files := make([]*FileRun, len(h))
	for i, x := range h {
		files[i] = x.(*FileRun)
	}
	run := &PakRun{pak, files}
	sort.Sort(run) // files were sorted by package; sort them by file now
	return run
}

// ----------------------------------------------------------------------------
// HitList

// A HitList describes a list of PakRuns.
type HitList []*PakRun

// PakRuns are sorted by package.
func lessPakRun(x, y interface{}) bool { return x.(*PakRun).Pak.less(y.(*PakRun).Pak) }

func reduce(h0 RunList) HitList {
	// reduce a list of Spots into a list of FileRuns
	h1 := h0.reduce(lessSpot, newFileRun)
	// reduce a list of FileRuns into a list of PakRuns
	h2 := h1.reduce(lessFileRun, newPakRun)
	// sort the list of PakRuns by package
	h2.sort(lessPakRun)
	// create a HitList
	h := make(HitList, len(h2))
	for i, p := range h2 {
		h[i] = p.(*PakRun)
	}
	return h
}

// filter returns a new HitList created by filtering
// all PakRuns from h that have a matching pakname.
func (h HitList) filter(pakname string) HitList {
	var hh HitList
	for _, p := range h {
		if p.Pak.Name == pakname {
			hh = append(hh, p)
		}
	}
	return hh
}

// ----------------------------------------------------------------------------
// AltWords

type wordPair struct {
	canon string // canonical word spelling (all lowercase)
	alt   string // alternative spelling
}

// An AltWords describes a list of alternative spellings for a
// canonical (all lowercase) spelling of a word.
type AltWords struct {
	Canon string   // canonical word spelling (all lowercase)
	Alts  []string // alternative spelling for the same word
}

// wordPairs are sorted by their canonical spelling.
func lessWordPair(x, y interface{}) bool { return x.(*wordPair).canon < y.(*wordPair).canon }

// newAltWords allocates a new AltWords from the *wordPair run h.
func newAltWords(h RunList) interface{} {
	canon := h[0].(*wordPair).canon
	alts := make([]string, len(h))
	for i, x := range h {
		alts[i] = x.(*wordPair).alt
	}
	return &AltWords{canon, alts}
}

func (a *AltWords) filter(s string) *AltWords {
	var alts []string
	for _, w := range a.Alts {
		if w != s {
			alts = append(alts, w)
		}
	}
	if len(alts) > 0 {
		return &AltWords{a.Canon, alts}
	}
	return nil
}

// ----------------------------------------------------------------------------
// Indexer

// Adjust these flags as seems best.
const includeMainPackages = true
const includeTestFiles = true

type IndexResult struct {
	Decls  RunList // package-level declarations (with snippets)
	Others RunList // all other occurrences
}

// Statistics provides statistics information for an index.
type Statistics struct {
	Bytes int // total size of indexed source files
	Files int // number of indexed source files
	Lines int // number of lines (all files)
	Words int // number of different identifiers
	Spots int // number of identifier occurrences
}

// An Indexer maintains the data structures and provides the machinery
// for indexing .go files under a file tree. It implements the path.Visitor
// interface for walking file trees, and the ast.Visitor interface for
// walking Go ASTs.
type Indexer struct {
	fset     *token.FileSet          // file set for all indexed files
	sources  bytes.Buffer            // concatenated sources
	packages map[string]*Pak         // map of canonicalized *Paks
	words    map[string]*IndexResult // RunLists of Spots
	snippets []*Snippet              // indices are stored in SpotInfos
	current  *token.File             // last file added to file set
	file     *File                   // AST for current file
	decl     ast.Decl                // AST for current decl
	stats    Statistics
}

func (x *Indexer) lookupPackage(path, name string) *Pak {
	// In the source directory tree, more than one package may
	// live in the same directory. For the packages map, construct
	// a key that includes both the directory path and the package
	// name.
	key := path + ":" + name
	pak := x.packages[key]
	if pak == nil {
		pak = &Pak{path, name}
		x.packages[key] = pak
	}
	return pak
}

func (x *Indexer) addSnippet(s *Snippet) int {
	index := len(x.snippets)
	x.snippets = append(x.snippets, s)
	return index
}

func (x *Indexer) visitIdent(kind SpotKind, id *ast.Ident) {
	if id != nil {
		lists, found := x.words[id.Name]
		if !found {
			lists = new(IndexResult)
			x.words[id.Name] = lists
		}

		if kind == Use || x.decl == nil {
			// not a declaration or no snippet required
			info := makeSpotInfo(kind, x.current.Line(id.Pos()), false)
			lists.Others = append(lists.Others, Spot{x.file, info})
		} else {
			// a declaration with snippet
			index := x.addSnippet(NewSnippet(x.fset, x.decl, id))
			info := makeSpotInfo(kind, index, true)
			lists.Decls = append(lists.Decls, Spot{x.file, info})
		}

		x.stats.Spots++
	}
}

func (x *Indexer) visitFieldList(kind SpotKind, flist *ast.FieldList) {
	for _, f := range flist.List {
		x.decl = nil // no snippets for fields
		for _, name := range f.Names {
			x.visitIdent(kind, name)
		}
		ast.Walk(x, f.Type)
		// ignore tag - not indexed at the moment
	}
}

func (x *Indexer) visitSpec(kind SpotKind, spec ast.Spec) {
	switch n := spec.(type) {
	case *ast.ImportSpec:
		x.visitIdent(ImportDecl, n.Name)
		// ignore path - not indexed at the moment

	case *ast.ValueSpec:
		for _, n := range n.Names {
			x.visitIdent(kind, n)
		}
		ast.Walk(x, n.Type)
		for _, v := range n.Values {
			ast.Walk(x, v)
		}

	case *ast.TypeSpec:
		x.visitIdent(TypeDecl, n.Name)
		ast.Walk(x, n.Type)
	}
}

func (x *Indexer) visitGenDecl(decl *ast.GenDecl) {
	kind := VarDecl
	if decl.Tok == token.CONST {
		kind = ConstDecl
	}
	x.decl = decl
	for _, s := range decl.Specs {
		x.visitSpec(kind, s)
	}
}

func (x *Indexer) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case nil:
		// nothing to do

	case *ast.Ident:
		x.visitIdent(Use, n)

	case *ast.FieldList:
		x.visitFieldList(VarDecl, n)

	case *ast.InterfaceType:
		x.visitFieldList(MethodDecl, n.Methods)

	case *ast.DeclStmt:
		// local declarations should only be *ast.GenDecls;
		// ignore incorrect ASTs
		if decl, ok := n.Decl.(*ast.GenDecl); ok {
			x.decl = nil // no snippets for local declarations
			x.visitGenDecl(decl)
		}

	case *ast.GenDecl:
		x.decl = n
		x.visitGenDecl(n)

	case *ast.FuncDecl:
		kind := FuncDecl
		if n.Recv != nil {
			kind = MethodDecl
			ast.Walk(x, n.Recv)
		}
		x.decl = n
		x.visitIdent(kind, n.Name)
		ast.Walk(x, n.Type)
		if n.Body != nil {
			ast.Walk(x, n.Body)
		}

	case *ast.File:
		x.decl = nil
		x.visitIdent(PackageClause, n.Name)
		for _, d := range n.Decls {
			ast.Walk(x, d)
		}

	default:
		return x
	}

	return nil
}

func pkgName(filename string) string {
	// use a new file set each time in order to not pollute the indexer's
	// file set (which must stay in sync with the concatenated source code)
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.PackageClauseOnly)
	if err != nil || file == nil {
		return ""
	}
	return file.Name.Name
}

// addFile adds a file to the index if possible and returns the file set file
// and the file's AST if it was successfully parsed as a Go file. If addFile
// failed (that is, if the file was not added), it returns file == nil.
func (x *Indexer) addFile(filename string, goFile bool) (file *token.File, ast *ast.File) {
	// open file
	f, err := FS.Open(filename)
	if err != nil {
		return
	}
	defer f.Close()

	// The file set's base offset and x.sources size must be in lock-step;
	// this permits the direct mapping of suffix array lookup results to
	// to corresponding Pos values.
	//
	// When a file is added to the file set, its offset base increases by
	// the size of the file + 1; and the initial base offset is 1. Add an
	// extra byte to the sources here.
	x.sources.WriteByte(0)

	// If the sources length doesn't match the file set base at this point
	// the file set implementation changed or we have another error.
	base := x.fset.Base()
	if x.sources.Len() != base {
		panic("internal error: file base incorrect")
	}

	// append file contents (src) to x.sources
	if _, err := x.sources.ReadFrom(f); err == nil {
		src := x.sources.Bytes()[base:]

		if goFile {
			// parse the file and in the process add it to the file set
			if ast, err = parser.ParseFile(x.fset, filename, src, parser.ParseComments); err == nil {
				file = x.fset.File(ast.Pos()) // ast.Pos() is inside the file
				return
			}
			// file has parse errors, and the AST may be incorrect -
			// set lines information explicitly and index as ordinary
			// text file (cannot fall through to the text case below
			// because the file has already been added to the file set
			// by the parser)
			file = x.fset.File(token.Pos(base)) // token.Pos(base) is inside the file
			file.SetLinesForContent(src)
			ast = nil
			return
		}

		if util.IsText(src) {
			// only add the file to the file set (for the full text index)
			file = x.fset.AddFile(filename, x.fset.Base(), len(src))
			file.SetLinesForContent(src)
			return
		}
	}

	// discard possibly added data
	x.sources.Truncate(base - 1) // -1 to remove added byte 0 since no file was added
	return
}

// Design note: Using an explicit white list of permitted files for indexing
// makes sure that the important files are included and massively reduces the
// number of files to index. The advantage over a blacklist is that unexpected
// (non-blacklisted) files won't suddenly explode the index.

// Files are whitelisted if they have a file name or extension
// present as key in whitelisted.
var whitelisted = map[string]bool{
	".bash":        true,
	".c":           true,
	".cc":          true,
	".cpp":         true,
	".cxx":         true,
	".css":         true,
	".go":          true,
	".goc":         true,
	".h":           true,
	".hh":          true,
	".hpp":         true,
	".hxx":         true,
	".html":        true,
	".js":          true,
	".out":         true,
	".py":          true,
	".s":           true,
	".sh":          true,
	".txt":         true,
	".xml":         true,
	"AUTHORS":      true,
	"CONTRIBUTORS": true,
	"LICENSE":      true,
	"Makefile":     true,
	"PATENTS":      true,
	"README":       true,
}

// isWhitelisted returns true if a file is on the list
// of "permitted" files for indexing. The filename must
// be the directory-local name of the file.
func isWhitelisted(filename string) bool {
	key := pathpkg.Ext(filename)
	if key == "" {
		// file has no extension - use entire filename
		key = filename
	}
	return whitelisted[key]
}

func (x *Indexer) visitFile(dirname string, f os.FileInfo, fulltextIndex bool) {
	if f.IsDir() {
		return
	}

	filename := pathpkg.Join(dirname, f.Name())
	goFile := false

	switch {
	case isGoFile(f):
		if !includeTestFiles && (!isPkgFile(f) || strings.HasPrefix(filename, "test/")) {
			return
		}
		if !includeMainPackages && pkgName(filename) == "main" {
			return
		}
		goFile = true

	case !fulltextIndex || !isWhitelisted(f.Name()):
		return
	}

	file, fast := x.addFile(filename, goFile)
	if file == nil {
		return // addFile failed
	}

	if fast != nil {
		// we've got a Go file to index
		x.current = file
		pak := x.lookupPackage(dirname, fast.Name.Name)
		x.file = &File{f.Name(), pak}
		ast.Walk(x, fast)
	}

	// update statistics
	x.stats.Bytes += file.Size()
	x.stats.Files++
	x.stats.Lines += file.LineCount()
}

// ----------------------------------------------------------------------------
// Index

type LookupResult struct {
	Decls  HitList // package-level declarations (with snippets)
	Others HitList // all other occurrences
}

type Index struct {
	fset     *token.FileSet           // file set used during indexing; nil if no textindex
	suffixes *suffixarray.Index       // suffixes for concatenated sources; nil if no textindex
	words    map[string]*LookupResult // maps words to hit lists
	alts     map[string]*AltWords     // maps canonical(words) to lists of alternative spellings
	snippets []*Snippet               // all snippets, indexed by snippet index
	stats    Statistics
}

func canonical(w string) string { return strings.ToLower(w) }

// NewIndex creates a new index for the .go files
// in the directories given by dirnames.
//
func NewIndex(dirnames <-chan string, fulltextIndex bool, throttle float64) *Index {
	var x Indexer
	th := util.NewThrottle(throttle, 100*time.Millisecond) // run at least 0.1s at a time

	// initialize Indexer
	// (use some reasonably sized maps to start)
	x.fset = token.NewFileSet()
	x.packages = make(map[string]*Pak, 256)
	x.words = make(map[string]*IndexResult, 8192)

	// index all files in the directories given by dirnames
	for dirname := range dirnames {
		list, err := FS.ReadDir(dirname)
		if err != nil {
			continue // ignore this directory
		}
		for _, f := range list {
			if !f.IsDir() {
				x.visitFile(dirname, f, fulltextIndex)
			}
			th.Throttle()
		}
	}

	if !fulltextIndex {
		// the file set, the current file, and the sources are
		// not needed after indexing if no text index is built -
		// help GC and clear them
		x.fset = nil
		x.sources.Reset()
		x.current = nil // contains reference to fset!
	}

	// for each word, reduce the RunLists into a LookupResult;
	// also collect the word with its canonical spelling in a
	// word list for later computation of alternative spellings
	words := make(map[string]*LookupResult)
	var wlist RunList
	for w, h := range x.words {
		decls := reduce(h.Decls)
		others := reduce(h.Others)
		words[w] = &LookupResult{
			Decls:  decls,
			Others: others,
		}
		wlist = append(wlist, &wordPair{canonical(w), w})
		th.Throttle()
	}
	x.stats.Words = len(words)

	// reduce the word list {canonical(w), w} into
	// a list of AltWords runs {canonical(w), {w}}
	alist := wlist.reduce(lessWordPair, newAltWords)

	// convert alist into a map of alternative spellings
	alts := make(map[string]*AltWords)
	for i := 0; i < len(alist); i++ {
		a := alist[i].(*AltWords)
		alts[a.Canon] = a
	}

	// create text index
	var suffixes *suffixarray.Index
	if fulltextIndex {
		suffixes = suffixarray.New(x.sources.Bytes())
	}

	return &Index{x.fset, suffixes, words, alts, x.snippets, x.stats}
}

type fileIndex struct {
	Words    map[string]*LookupResult
	Alts     map[string]*AltWords
	Snippets []*Snippet
	Fulltext bool
}

func (x *fileIndex) Write(w io.Writer) error {
	return gob.NewEncoder(w).Encode(x)
}

func (x *fileIndex) Read(r io.Reader) error {
	return gob.NewDecoder(r).Decode(x)
}

// Write writes the index x to w.
func (x *Index) Write(w io.Writer) error {
	fulltext := false
	if x.suffixes != nil {
		fulltext = true
	}
	fx := fileIndex{
		x.words,
		x.alts,
		x.snippets,
		fulltext,
	}
	if err := fx.Write(w); err != nil {
		return err
	}
	if fulltext {
		encode := func(x interface{}) error {
			return gob.NewEncoder(w).Encode(x)
		}
		if err := x.fset.Write(encode); err != nil {
			return err
		}
		if err := x.suffixes.Write(w); err != nil {
			return err
		}
	}
	return nil
}

// Read reads the index from r into x; x must not be nil.
// If r does not also implement io.ByteReader, it will be wrapped in a bufio.Reader.
func (x *Index) Read(r io.Reader) error {
	// We use the ability to read bytes as a plausible surrogate for buffering.
	if _, ok := r.(io.ByteReader); !ok {
		r = bufio.NewReader(r)
	}
	var fx fileIndex
	if err := fx.Read(r); err != nil {
		return err
	}
	x.words = fx.Words
	x.alts = fx.Alts
	x.snippets = fx.Snippets
	if fx.Fulltext {
		x.fset = token.NewFileSet()
		decode := func(x interface{}) error {
			return gob.NewDecoder(r).Decode(x)
		}
		if err := x.fset.Read(decode); err != nil {
			return err
		}
		x.suffixes = new(suffixarray.Index)
		if err := x.suffixes.Read(r); err != nil {
			return err
		}
	}
	return nil
}

// Stats() returns index statistics.
func (x *Index) Stats() Statistics {
	return x.stats
}

func (x *Index) lookupWord(w string) (match *LookupResult, alt *AltWords) {
	match = x.words[w]
	alt = x.alts[canonical(w)]
	// remove current spelling from alternatives
	// (if there is no match, the alternatives do
	// not contain the current spelling)
	if match != nil && alt != nil {
		alt = alt.filter(w)
	}
	return
}

// isIdentifier reports whether s is a Go identifier.
func isIdentifier(s string) bool {
	for i, ch := range s {
		if unicode.IsLetter(ch) || ch == ' ' || i > 0 && unicode.IsDigit(ch) {
			continue
		}
		return false
	}
	return len(s) > 0
}

// For a given query, which is either a single identifier or a qualified
// identifier, Lookup returns a list of packages, a LookupResult, and a
// list of alternative spellings, if any. Any and all results may be nil.
// If the query syntax is wrong, an error is reported.
func (x *Index) Lookup(query string) (paks HitList, match *LookupResult, alt *AltWords, err error) {
	ss := strings.Split(query, ".")

	// check query syntax
	for _, s := range ss {
		if !isIdentifier(s) {
			err = errors.New("all query parts must be identifiers")
			return
		}
	}

	// handle simple and qualified identifiers
	switch len(ss) {
	case 1:
		ident := ss[0]
		match, alt = x.lookupWord(ident)
		if match != nil {
			// found a match - filter packages with same name
			// for the list of packages called ident, if any
			paks = match.Others.filter(ident)
		}

	case 2:
		pakname, ident := ss[0], ss[1]
		match, alt = x.lookupWord(ident)
		if match != nil {
			// found a match - filter by package name
			// (no paks - package names are not qualified)
			decls := match.Decls.filter(pakname)
			others := match.Others.filter(pakname)
			match = &LookupResult{decls, others}
		}

	default:
		err = errors.New("query is not a (qualified) identifier")
	}

	return
}

func (x *Index) Snippet(i int) *Snippet {
	// handle illegal snippet indices gracefully
	if 0 <= i && i < len(x.snippets) {
		return x.snippets[i]
	}
	return nil
}

type positionList []struct {
	filename string
	line     int
}

func (list positionList) Len() int           { return len(list) }
func (list positionList) Less(i, j int) bool { return list[i].filename < list[j].filename }
func (list positionList) Swap(i, j int)      { list[i], list[j] = list[j], list[i] }

// unique returns the list sorted and with duplicate entries removed
func unique(list []int) []int {
	sort.Ints(list)
	var last int
	i := 0
	for _, x := range list {
		if i == 0 || x != last {
			last = x
			list[i] = x
			i++
		}
	}
	return list[0:i]
}

// A FileLines value specifies a file and line numbers within that file.
type FileLines struct {
	Filename string
	Lines    []int
}

// LookupRegexp returns the number of matches and the matches where a regular
// expression r is found in the full text index. At most n matches are
// returned (thus found <= n).
//
func (x *Index) LookupRegexp(r *regexp.Regexp, n int) (found int, result []FileLines) {
	if x.suffixes == nil || n <= 0 {
		return
	}
	// n > 0

	var list positionList
	// FindAllIndex may returns matches that span across file boundaries.
	// Such matches are unlikely, buf after eliminating them we may end up
	// with fewer than n matches. If we don't have enough at the end, redo
	// the search with an increased value n1, but only if FindAllIndex
	// returned all the requested matches in the first place (if it
	// returned fewer than that there cannot be more).
	for n1 := n; found < n; n1 += n - found {
		found = 0
		matches := x.suffixes.FindAllIndex(r, n1)
		// compute files, exclude matches that span file boundaries,
		// and map offsets to file-local offsets
		list = make(positionList, len(matches))
		for _, m := range matches {
			// by construction, an offset corresponds to the Pos value
			// for the file set - use it to get the file and line
			p := token.Pos(m[0])
			if file := x.fset.File(p); file != nil {
				if base := file.Base(); base <= m[1] && m[1] <= base+file.Size() {
					// match [m[0], m[1]) is within the file boundaries
					list[found].filename = file.Name()
					list[found].line = file.Line(p)
					found++
				}
			}
		}
		if found == n || len(matches) < n1 {
			// found all matches or there's no chance to find more
			break
		}
	}
	list = list[0:found]
	sort.Sort(list) // sort by filename

	// collect matches belonging to the same file
	var last string
	var lines []int
	addLines := func() {
		if len(lines) > 0 {
			// remove duplicate lines
			result = append(result, FileLines{last, unique(lines)})
			lines = nil
		}
	}
	for _, m := range list {
		if m.filename != last {
			addLines()
			last = m.filename
		}
		lines = append(lines, m.line)
	}
	addLines()

	return
}

// InvalidateIndex should be called whenever any of the file systems
// under godoc's observation change so that the indexer is kicked on.
func (c *Corpus) invalidateIndex() {
	c.fsModified.Set(nil)
	c.refreshMetadata()
}

// indexUpToDate() returns true if the search index is not older
// than any of the file systems under godoc's observation.
//
func indexUpToDate() bool {
	_, fsTime := FSModified.Get()
	_, siTime := SearchIndex.Get()
	return !fsTime.After(siTime)
}

// feedDirnames feeds the directory names of all directories
// under the file system given by root to channel c.
//
func feedDirnames(root *util.RWValue, c chan<- string) {
	if dir, _ := root.Get(); dir != nil {
		for d := range dir.(*Directory).iter(false) {
			c <- d.Path
		}
	}
}

// fsDirnames() returns a channel sending all directory names
// of all the file systems under godoc's observation.
//
func fsDirnames() <-chan string {
	c := make(chan string, 256) // buffered for fewer context switches
	go func() {
		feedDirnames(&FSTree, c)
		close(c)
	}()
	return c
}

func readIndex(filenames string) error {
	matches, err := filepath.Glob(filenames)
	if err != nil {
		return err
	} else if matches == nil {
		return fmt.Errorf("no index files match %q", filenames)
	}
	sort.Strings(matches) // make sure files are in the right order
	files := make([]io.Reader, 0, len(matches))
	for _, filename := range matches {
		f, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer f.Close()
		files = append(files, f)
	}
	x := new(Index)
	if err := x.Read(io.MultiReader(files...)); err != nil {
		return err
	}
	SearchIndex.Set(x)
	return nil
}

func UpdateIndex() {
	if Verbose {
		log.Printf("updating index...")
	}
	start := time.Now()
	index := NewIndex(fsDirnames(), MaxResults > 0, IndexThrottle)
	stop := time.Now()
	SearchIndex.Set(index)
	if Verbose {
		secs := stop.Sub(start).Seconds()
		stats := index.Stats()
		log.Printf("index updated (%gs, %d bytes of source, %d files, %d lines, %d unique words, %d spots)",
			secs, stats.Bytes, stats.Files, stats.Lines, stats.Words, stats.Spots)
	}
	memstats := new(runtime.MemStats)
	runtime.ReadMemStats(memstats)
	log.Printf("before GC: bytes = %d footprint = %d", memstats.HeapAlloc, memstats.Sys)
	runtime.GC()
	runtime.ReadMemStats(memstats)
	log.Printf("after  GC: bytes = %d footprint = %d", memstats.HeapAlloc, memstats.Sys)
}

// RunIndexer runs forever, indexing.
func RunIndexer() {
	// initialize the index from disk if possible
	if IndexFiles != "" {
		if err := readIndex(IndexFiles); err != nil {
			log.Printf("error reading index: %s", err)
		}
	}

	// repeatedly update the index when it goes out of date
	for {
		if !indexUpToDate() {
			// index possibly out of date - make a new one
			UpdateIndex()
		}
		delay := 60 * time.Second // by default, try every 60s
		if false {                // TODO(bradfitz): was: *testDir != "" {
			// in test mode, try once a second for fast startup
			delay = 1 * time.Second
		}
		time.Sleep(delay)
	}
}
