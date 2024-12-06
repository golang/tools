// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package methodsets defines an incremental, serializable index of
// method-set information that allows efficient 'implements' queries
// across packages of the workspace without using the type checker.
//
// This package provides only the "global" (all workspace) search; the
// "local" search within a given package uses a different
// implementation based on type-checker data structures for a single
// package plus variants; see ../implementation.go.
// The local algorithm is more precise as it tests function-local types too.
//
// A global index of function-local types is challenging since they
// may reference other local types, for which we would need to invent
// stable names, an unsolved problem described in passing in Go issue
// 57497. The global algorithm also does not index anonymous interface
// types, even outside function bodies.
//
// Consequently, global results are not symmetric: applying the
// operation twice may not get you back where you started.
package methodsets

// DESIGN
//
// See https://go.dev/cl/452060 for a minimal exposition of the algorithm.
//
// For each method, we compute a fingerprint: a string representing
// the method name and type such that equal fingerprint strings mean
// identical method types.
//
// For efficiency, the fingerprint is reduced to a single bit
// of a uint64, so that the method set can be represented as
// the union of those method bits (a uint64 bitmask).
// Assignability thus reduces to a subset check on bitmasks
// followed by equality checks on fingerprints.
//
// In earlier experiments, using 128-bit masks instead of 64 reduced
// the number of candidates by about 2x. Using (like a Bloom filter) a
// different hash function to compute a second 64-bit mask and
// performing a second mask test reduced it by about 4x.
// Neither had much effect on the running time, presumably because a
// single 64-bit mask is quite effective. See CL 452060 for details.

import (
	"go/token"
	"go/types"
	"hash/crc32"
	"slices"
	"sync/atomic"

	"golang.org/x/tools/go/types/objectpath"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/frob"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/typesinternal"
)

// An Index records the non-empty method sets of all package-level
// types in a package in a form that permits assignability queries
// without the type checker.
type Index struct {
	pkg gobPackage
}

// Decode decodes the given gob-encoded data as an Index.
func Decode(data []byte) *Index {
	var pkg gobPackage
	packageCodec.Decode(data, &pkg)
	return &Index{pkg}
}

// Encode encodes the receiver as gob-encoded data.
func (index *Index) Encode() []byte {
	return packageCodec.Encode(index.pkg)
}

// NewIndex returns a new index of method-set information for all
// package-level types in the specified package.
func NewIndex(fset *token.FileSet, pkg *types.Package) *Index {
	return new(indexBuilder).build(fset, pkg)
}

// A Location records the extent of an identifier in byte-offset form.
//
// Conversion to protocol (UTF-16) form is done by the caller after a
// search, not during index construction.
type Location struct {
	Filename   string
	Start, End int // byte offsets
}

// A Key represents the method set of a given type in a form suitable
// to pass to the (*Index).Search method of many different Indexes.
type Key struct {
	mset *gobMethodSet // note: lacks position information
}

// KeyOf returns the search key for the method sets of a given type.
// It returns false if the type has no methods.
func KeyOf(t types.Type) (Key, bool) {
	mset := methodSetInfo(t, nil)
	if mset.Mask == 0 {
		return Key{}, false // no methods
	}
	return Key{mset}, true
}

// A Result reports a matching type or method in a method-set search.
type Result struct {
	Location Location // location of the type or method

	// methods only:
	PkgPath    string          // path of declaring package (may differ due to embedding)
	ObjectPath objectpath.Path // path of method within declaring package
}

// Search reports each type that implements (or is implemented by) the
// type that produced the search key. If methodID is nonempty, only
// that method of each type is reported.
//
// The result does not include the error.Error method.
// TODO(adonovan): give this special case a more systematic treatment.
func (index *Index) Search(key Key, method *types.Func) []Result {
	var results []Result
	for _, candidate := range index.pkg.MethodSets {
		// Traditionally this feature doesn't report
		// interface/interface elements of the relation.
		// I think that's a mistake.
		// TODO(adonovan): UX: change it, here and in the local implementation.
		if candidate.IsInterface && key.mset.IsInterface {
			continue
		}

		if !implements(candidate, key.mset) && !implements(key.mset, candidate) {
			continue
		}

		if method == nil {
			results = append(results, Result{Location: index.location(candidate.Posn)})
		} else {
			for _, m := range candidate.Methods {
				if m.ID == method.Id() {
					// Don't report error.Error among the results:
					// it has no true source location, no package,
					// and is excluded from the xrefs index.
					if m.PkgPath == 0 || m.ObjectPath == 0 {
						if m.ID != "Error" {
							panic("missing info for" + m.ID)
						}
						continue
					}

					results = append(results, Result{
						Location:   index.location(m.Posn),
						PkgPath:    index.pkg.Strings[m.PkgPath],
						ObjectPath: objectpath.Path(index.pkg.Strings[m.ObjectPath]),
					})
					break
				}
			}
		}
	}
	return results
}

// implements reports whether x implements y.
func implements(x, y *gobMethodSet) bool {
	if !y.IsInterface {
		return false
	}

	// Fast path: neither method set is tricky, so all methods can
	// be compared by equality of ID and Fingerprint, and the
	// entire subset check can be done using the bit mask.
	if !x.Tricky && !y.Tricky {
		if x.Mask&y.Mask != y.Mask {
			return false // x lacks a method of interface y
		}
	}

	// At least one operand is tricky (e.g. contains a type parameter),
	// so we must used tree-based matching (unification).

	// nonmatching reports whether interface method 'my' lacks
	// a matching method in set x. (The sense is inverted for use
	// with slice.ContainsFunc below.)
	nonmatching := func(my *gobMethod) bool {
		for _, mx := range x.Methods {
			if mx.ID == my.ID {
				var match bool
				if !mx.Tricky && !my.Tricky {
					// Fast path: neither method is tricky,
					// so a string match is sufficient.
					match = mx.Sum&my.Sum == my.Sum && mx.Fingerprint == my.Fingerprint
				} else {
					match = unify(mx.parse(), my.parse())
				}
				return !match
			}
		}
		return true // method of y not found in x
	}

	// Each interface method must have a match.
	// (This would be more readable with a DeMorganized
	// variant of ContainsFunc.)
	return !slices.ContainsFunc(y.Methods, nonmatching)
}

func (index *Index) location(posn gobPosition) Location {
	return Location{
		Filename: index.pkg.Strings[posn.File],
		Start:    posn.Offset,
		End:      posn.Offset + posn.Len,
	}
}

// An indexBuilder builds an index for a single package.
type indexBuilder struct {
	gobPackage
	stringIndex map[string]int
}

// build adds to the index all package-level named types of the specified package.
func (b *indexBuilder) build(fset *token.FileSet, pkg *types.Package) *Index {
	_ = b.string("") // 0 => ""

	objectPos := func(obj types.Object) gobPosition {
		posn := safetoken.StartPosition(fset, obj.Pos())
		return gobPosition{b.string(posn.Filename), posn.Offset, len(obj.Name())}
	}

	objectpathFor := new(objectpath.Encoder).For

	// setindexInfo sets the (Posn, PkgPath, ObjectPath) fields for each method declaration.
	setIndexInfo := func(m *gobMethod, method *types.Func) {
		// error.Error has empty Position, PkgPath, and ObjectPath.
		if method.Pkg() == nil {
			return
		}

		// Instantiations of generic methods don't have an
		// object path, so we use the generic.
		p, err := objectpathFor(method.Origin())
		if err != nil {
			// This should never happen for a method of a package-level type.
			// ...but it does (golang/go#70418).
			// Refine the crash into various bug reports.
			report := func() {
				bug.Reportf("missing object path for %s", method.FullName())
			}
			sig := method.Signature()
			if sig.Recv() == nil {
				report()
				return
			}
			_, named := typesinternal.ReceiverNamed(sig.Recv())
			switch {
			case named == nil:
				report()
			case sig.TypeParams().Len() > 0:
				report()
			case method.Origin() != method:
				report() // instantiated?
			case sig.RecvTypeParams().Len() > 0:
				report() // generic?
			default:
				report()
			}
			return
		}

		m.Posn = objectPos(method)
		m.PkgPath = b.string(method.Pkg().Path())
		m.ObjectPath = b.string(string(p))
	}

	// We ignore aliases, though in principle they could define a
	// struct{...}  or interface{...} type, or an instantiation of
	// a generic, that has a novel method set.
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		if tname, ok := scope.Lookup(name).(*types.TypeName); ok && !tname.IsAlias() {
			if mset := methodSetInfo(tname.Type(), setIndexInfo); mset.Mask != 0 {
				mset.Posn = objectPos(tname)
				// Only record types with non-trivial method sets.
				b.MethodSets = append(b.MethodSets, mset)
			}
		}
	}

	return &Index{pkg: b.gobPackage}
}

// string returns a small integer that encodes the string.
func (b *indexBuilder) string(s string) int {
	i, ok := b.stringIndex[s]
	if !ok {
		i = len(b.Strings)
		if b.stringIndex == nil {
			b.stringIndex = make(map[string]int)
		}
		b.stringIndex[s] = i
		b.Strings = append(b.Strings, s)
	}
	return i
}

// methodSetInfo returns the method-set fingerprint of a type.
// It calls the optional setIndexInfo function for each gobMethod.
// This is used during index construction, but not search (KeyOf),
// to store extra information.
func methodSetInfo(t types.Type, setIndexInfo func(*gobMethod, *types.Func)) *gobMethodSet {
	// For non-interface types, use *T
	// (if T is not already a pointer)
	// since it may have more methods.
	mset := types.NewMethodSet(EnsurePointer(t))

	// Convert the method set into a compact summary.
	var mask uint64
	tricky := false
	var buf []byte
	methods := make([]*gobMethod, mset.Len())
	for i := 0; i < mset.Len(); i++ {
		m := mset.At(i).Obj().(*types.Func)
		id := m.Id()
		fp, isTricky := fingerprint(m.Signature())
		if isTricky {
			tricky = true
		}
		buf = append(append(buf[:0], id...), fp...)
		sum := crc32.ChecksumIEEE(buf)
		methods[i] = &gobMethod{ID: id, Fingerprint: fp, Sum: sum, Tricky: isTricky}
		if setIndexInfo != nil {
			setIndexInfo(methods[i], m) // set Position, PkgPath, ObjectPath
		}
		mask |= 1 << uint64(((sum>>24)^(sum>>16)^(sum>>8)^sum)&0x3f)
	}
	return &gobMethodSet{
		IsInterface: types.IsInterface(t),
		Tricky:      tricky,
		Mask:        mask,
		Methods:     methods,
	}
}

// EnsurePointer wraps T in a types.Pointer if T is a named, non-interface type.
// This is useful to make sure you consider a named type's full method set.
func EnsurePointer(T types.Type) types.Type {
	if _, ok := types.Unalias(T).(*types.Named); ok && !types.IsInterface(T) {
		return types.NewPointer(T)
	}

	return T
}

// -- serial format of index --

// (The name says gob but in fact we use frob.)
var packageCodec = frob.CodecFor[gobPackage]()

// A gobPackage records the method set of each package-level type for a single package.
type gobPackage struct {
	Strings    []string // index of strings used by gobPosition.File, gobMethod.{Pkg,Object}Path
	MethodSets []*gobMethodSet
}

// A gobMethodSet records the method set of a single type.
type gobMethodSet struct {
	Posn        gobPosition
	IsInterface bool
	Tricky      bool   // at least one method is tricky; fingerprint must be parsed + unified
	Mask        uint64 // mask with 1 bit from each of methods[*].sum
	Methods     []*gobMethod
}

// A gobMethod records the name, type, and position of a single method.
type gobMethod struct {
	ID          string // (*types.Func).Id() value of method
	Fingerprint string // encoding of types as string of form "(params)(results)"
	Sum         uint32 // checksum of ID + fingerprint
	Tricky      bool   // method type contains tricky features (type params, interface types)

	// index records only (zero in KeyOf; also for index of error.Error).
	Posn       gobPosition // location of method declaration
	PkgPath    int         // path of package containing method declaration
	ObjectPath int         // object path of method relative to PkgPath

	// internal fields (not serialized)
	tree atomic.Pointer[sexpr] // fingerprint tree, parsed on demand
}

// A gobPosition records the file, offset, and length of an identifier.
type gobPosition struct {
	File        int // index into gobPackage.Strings
	Offset, Len int // in bytes
}

// parse returns the method's parsed fingerprint tree.
// It may return a new instance or a cached one.
func (m *gobMethod) parse() sexpr {
	ptr := m.tree.Load()
	if ptr == nil {
		tree := parseFingerprint(m.Fingerprint)
		ptr = &tree
		m.tree.Store(ptr) // may race; that's ok
	}
	return *ptr
}
