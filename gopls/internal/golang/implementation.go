// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"iter"
	"reflect"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/methodsets"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/moreiters"
	"golang.org/x/tools/internal/typesinternal"
)

// This file defines the new implementation of the 'implementation'
// operator that does not require type-checker data structures for an
// unbounded number of packages.
//
// TODO(adonovan):
// - Audit to ensure robustness in face of type errors.
// - Eliminate false positives due to 'tricky' cases of the global algorithm.
// - Ensure we have test coverage of:
//      type aliases
//      nil, PkgName, Builtin (all errors)
//      any (empty result)
//      method of unnamed interface type (e.g. var x interface { f() })
//        (the global algorithm may find implementations of this type
//         but will not include it in the index.)

// Implementation returns a new sorted array of locations of
// declarations of types that implement (or are implemented by) the
// type referred to at the given position.
//
// If the position denotes a method, the computation is applied to its
// receiver type and then its corresponding methods are returned.
func Implementation(ctx context.Context, snapshot *cache.Snapshot, f file.Handle, pp protocol.Position) ([]protocol.Location, error) {
	ctx, done := event.Start(ctx, "golang.Implementation")
	defer done()

	locs, err := implementations(ctx, snapshot, f, pp)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(locs, protocol.CompareLocation)
	locs = slices.Compact(locs) // de-duplicate
	return locs, nil
}

func implementations(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, pp protocol.Position) ([]protocol.Location, error) {
	// Type check the current package.
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, err
	}
	pos, err := pgf.PositionPos(pp)
	if err != nil {
		return nil, err
	}
	cur, _ := pgf.Cursor.FindByPos(pos, pos) // can't fail

	// Find implementations based on func signatures.
	if locs, err := implFuncs(pkg, cur, pos); err != errNotHandled {
		return locs, err
	}

	// Find implementations based on method sets.
	var (
		locsMu sync.Mutex
		locs   []protocol.Location
	)
	// relation=0 here means infer direction of the relation
	// (Supertypes/Subtypes) from concreteness of query type/method.
	// (Ideally the implementations request would provide directionality
	// so that one could ask for, say, the superinterfaces of io.ReadCloser;
	// see https://github.com/golang/go/issues/68641#issuecomment-2269293762.)
	const relation = methodsets.TypeRelation(0)
	err = implementationsMsets(ctx, snapshot, pkg, cur, relation, func(_ metadata.PackagePath, _ string, _ bool, loc protocol.Location) {
		locsMu.Lock()
		locs = append(locs, loc)
		locsMu.Unlock()
	})
	return locs, err
}

// An implYieldFunc is a callback called for each match produced by the implementation machinery.
// - name describes the type or method.
// - abstract indicates that the result is an interface type or interface method.
//
// implYieldFunc implementations must be concurrency-safe.
type implYieldFunc func(pkgpath metadata.PackagePath, name string, abstract bool, loc protocol.Location)

// implementationsMsets computes implementations of the type at the
// position specifed by cur, by method sets.
//
// rel specifies the desired direction of the relation: Subtype,
// Supertype, or both. As a special case, zero means infer the
// direction from the concreteness of the query object: Supertype for
// a concrete type, Subtype for an interface.
//
// It is shared by Implementations and TypeHierarchy.
func implementationsMsets(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, cur inspector.Cursor, rel methodsets.TypeRelation, yield implYieldFunc) error {
	// First, find the object referenced at the cursor.
	// The object may be declared in a different package.
	obj, err := implementsObj(pkg.TypesInfo(), cur)
	if err != nil {
		return err
	}

	// If the resulting object has a position, we can expand the search to types
	// in the declaring package(s). In this case, we must re-type check these
	// packages in the same realm.
	var (
		declOffset int
		declURI    protocol.DocumentURI
		localPkgs  []*cache.Package
	)
	if obj.Pos().IsValid() { // no local package for error or error.Error
		declPosn := safetoken.StartPosition(pkg.FileSet(), obj.Pos())
		declOffset = declPosn.Offset
		// Type-check the declaring package (incl. variants) for use
		// by the "local" search, which uses type information to
		// enumerate all types within the package that satisfy the
		// query type, even those defined local to a function.
		declURI = protocol.URIFromPath(declPosn.Filename)
		declMPs, err := snapshot.MetadataForFile(ctx, declURI, true)
		if err != nil {
			return err
		}
		if len(declMPs) == 0 {
			return fmt.Errorf("no packages for file %s", declURI)
		}
		ids := make([]PackageID, len(declMPs))
		for i, mp := range declMPs {
			ids[i] = mp.ID
		}
		localPkgs, err = snapshot.TypeCheck(ctx, ids...)
		if err != nil {
			return err
		}
	}

	pkg = nil // no longer used

	// Is the selected identifier a type name or method?
	// (For methods, report the corresponding method names.)
	queryType, queryMethod := typeOrMethod(obj)
	if queryType == nil {
		return bug.Errorf("%s is not a type or method", obj.Name()) // should have been handled by implementsObj
	}

	// Compute the method-set fingerprint used as a key to the global search.
	key, hasMethods := methodsets.KeyOf(queryType)
	if !hasMethods {
		// A type with no methods yields an empty result.
		// (No point reporting that every type satisfies 'any'.)
		return nil
	}

	// If the client specified no relation, infer it
	// from the concreteness of the query type.
	if rel == 0 {
		rel = cond(types.IsInterface(queryType),
			methodsets.Subtype,
			methodsets.Supertype)
	}

	// The global search needs to look at every package in the
	// forward transitive closure of the workspace; see package
	// ./methodsets.
	//
	// For now we do all the type checking before beginning the search.
	// TODO(adonovan): opt: search in parallel topological order
	// so that we can overlap index lookup with typechecking.
	// I suspect a number of algorithms on the result of TypeCheck could
	// be optimized by being applied as soon as each package is available.
	globalMetas, err := snapshot.AllMetadata(ctx)
	if err != nil {
		return err
	}
	metadata.RemoveIntermediateTestVariants(&globalMetas)
	globalIDs := make([]PackageID, 0, len(globalMetas))

	var pkgPath PackagePath
	if obj.Pkg() != nil { // nil for error
		pkgPath = PackagePath(obj.Pkg().Path())
	}
	for _, mp := range globalMetas {
		if mp.PkgPath == pkgPath {
			continue // declaring package is handled by local implementation
		}
		globalIDs = append(globalIDs, mp.ID)
	}
	indexes, err := snapshot.MethodSets(ctx, globalIDs...)
	if err != nil {
		return fmt.Errorf("querying method sets: %v", err)
	}

	// Search local and global packages in parallel.
	var group errgroup.Group

	// local search
	for _, pkg := range localPkgs {
		// The localImplementations algorithm assumes needle and haystack
		// belong to a single package (="realm" of types symbol identities),
		// so we need to recompute obj for each local package.
		// (By contrast the global algorithm is name-based.)
		group.Go(func() error {
			pkgID := pkg.Metadata().ID

			// Find declaring identifier based on (URI, offset)
			// so that localImplementations can locate the
			// corresponding obj/queryType/queryMethod in pkg.
			declFile, err := pkg.File(declURI)
			if err != nil {
				return err // "can't happen"
			}
			pos, err := safetoken.Pos(declFile.Tok, declOffset)
			if err != nil {
				return err // also "can't happen"
			}
			curIdent, ok := declFile.Cursor.FindByPos(pos, pos)
			if !ok {
				return bug.Errorf("position not within file") // can't happen
			}
			id, ok := curIdent.Node().(*ast.Ident)
			if !ok {
				return ErrNoIdentFound // checked earlier
			}
			if err := localImplementations(ctx, snapshot, pkg, id, rel, yield); err != nil {
				return fmt.Errorf("querying local implementations %q: %v", pkgID, err)
			}
			return nil
		})
	}
	// global search
	for _, index := range indexes {
		group.Go(func() error {
			for _, res := range index.Search(key, rel, queryMethod) {
				loc := res.Location
				// Map offsets to protocol.Locations in parallel (may involve I/O).
				group.Go(func() error {
					ploc, err := offsetToLocation(ctx, snapshot, loc.Filename, loc.Start, loc.End)
					if err != nil {
						return err
					}
					yield(index.PkgPath, res.TypeName, res.IsInterface, ploc)
					return nil
				})
			}
			return nil
		})
	}
	return group.Wait()
}

// typeOrMethod returns the type and optional method to use in an
// Implementations operation on the specified symbol.
// It returns a nil type to indicate that the query should not proceed.
//
// (It is factored out to allow it to be used both in the query package
// then (in [localImplementations]) again in the declaring package.)
func typeOrMethod(obj types.Object) (types.Type, *types.Func) {
	switch obj := obj.(type) {
	case *types.TypeName:
		return obj.Type(), nil
	case *types.Func:
		// For methods, use the receiver type, which may be anonymous.
		if recv := obj.Signature().Recv(); recv != nil {
			return recv.Type(), obj
		}
	}
	return nil, nil
}

// offsetToLocation converts an offset-based position to a protocol.Location,
// which requires reading the file.
func offsetToLocation(ctx context.Context, snapshot *cache.Snapshot, filename string, start, end int) (protocol.Location, error) {
	uri := protocol.URIFromPath(filename)
	fh, err := snapshot.ReadFile(ctx, uri)
	if err != nil {
		return protocol.Location{}, err // cancelled, perhaps
	}
	content, err := fh.Content()
	if err != nil {
		return protocol.Location{}, err // nonexistent or deleted ("can't happen")
	}
	m := protocol.NewMapper(uri, content)
	return m.OffsetLocation(start, end)
}

// implementsObj returns the object to query for implementations,
// which is a type name or method.
func implementsObj(info *types.Info, cur inspector.Cursor) (types.Object, error) {
	// This function inherits the limitation of its predecessor in
	// requiring the selection to be an identifier (of a type or
	// method). But there's no fundamental reason why one could
	// not pose this query about any selected piece of syntax that
	// has a type and thus a method set.
	// (If LSP was more thorough about passing text selections as
	// intervals to queries, you could ask about the method set of a
	// subexpression such as x.f().)
	// [Note that this process has begun; see #69058.]
	id, ok := cur.Node().(*ast.Ident)
	if !ok {
		return nil, ErrNoIdentFound
	}

	// Is the object a type or method? Reject other kinds.
	obj := info.Uses[id]
	if obj == nil {
		// Check uses first (unlike ObjectOf) so that T in
		// struct{T} is treated as a reference to a type,
		// not a declaration of a field.
		obj = info.Defs[id]
	}
	switch obj := obj.(type) {
	case *types.TypeName:
		// ok
	case *types.Func:
		if obj.Signature().Recv() == nil {
			return nil, fmt.Errorf("%s is a function, not a method (query at 'func' token to find matching signatures)", id.Name)
		}
	case nil:
		return nil, fmt.Errorf("%s denotes unknown object", id.Name)
	default:
		// e.g. *types.Var -> "var".
		kind := strings.ToLower(strings.TrimPrefix(reflect.TypeOf(obj).String(), "*types."))
		// TODO(adonovan): improve upon "nil is a nil, not a type".
		return nil, fmt.Errorf("%s is a %s, not a type", id.Name, kind)
	}

	return obj, nil
}

// localImplementations searches within pkg for declarations of all
// supertypes (if rel contains Supertype) or subtypes (if rel contains
// Subtype) of the type or method declared by id within the same
// package, and returns a new unordered array of their locations.
//
// If method is non-nil, the function instead returns the location
// of each type's method (if any) of that ID.
//
// ("Local" refers to the search within the same package, but this
// function's results may include type declarations that are local to
// a function body. The global search index excludes such types
// because reliably naming such types is hard.)
//
// Results are reported via the yield function.
func localImplementations(ctx context.Context, snapshot *cache.Snapshot, pkg *cache.Package, id *ast.Ident, rel methodsets.TypeRelation, yield implYieldFunc) error {
	queryType, queryMethod := typeOrMethod(pkg.TypesInfo().Defs[id])
	if queryType == nil {
		return bug.Errorf("can't find corresponding symbol for %q in package %q", id.Name, pkg)
	}
	queryType = methodsets.EnsurePointer(queryType)

	var msets typeutil.MethodSetCache

	matches := func(candidateType types.Type) bool {
		// Test the direction of the relation.
		// The client may request either direction or both
		// (e.g. when the client is References),
		// and the Result reports each test independently;
		// both tests succeed when comparing identical
		// interface types.
		var got methodsets.TypeRelation
		if rel&methodsets.Supertype != 0 && implements(&msets, queryType, candidateType) {
			got |= methodsets.Supertype
		}
		if rel&methodsets.Subtype != 0 && implements(&msets, candidateType, queryType) {
			got |= methodsets.Subtype
		}
		return got != 0
	}

	// Scan through all type declarations in the syntax.
	for _, pgf := range pkg.CompiledGoFiles() {
		for cur := range pgf.Cursor.Preorder((*ast.TypeSpec)(nil)) {
			spec := cur.Node().(*ast.TypeSpec)
			if spec.Name == id {
				continue // avoid self-comparison of query type
			}
			def := pkg.TypesInfo().Defs[spec.Name]
			if def == nil {
				continue // "can't happen" for types
			}
			if def.(*types.TypeName).IsAlias() {
				continue // skip type aliases to avoid duplicate reporting
			}
			candidateType := methodsets.EnsurePointer(def.Type())
			if !matches(candidateType) {
				continue
			}

			// Ignore types with empty method sets.
			// (No point reporting that every type satisfies 'any'.)
			mset := msets.MethodSet(candidateType)
			if mset.Len() == 0 {
				continue
			}

			isInterface := types.IsInterface(def.Type())

			if queryMethod == nil {
				// Found matching type.
				loc := mustLocation(pgf, spec.Name)
				yield(pkg.Metadata().PkgPath, spec.Name.Name, isInterface, loc)
				continue
			}

			// Find corresponding method.
			//
			// We can't use LookupFieldOrMethod because it requires
			// the methodID's types.Package, which we don't know.
			// We could recursively search pkg.Imports for it,
			// but it's easier to walk the method set.
			for method := range mset.Methods() {
				m := method.Obj()
				if m.Pos() == id.Pos() {
					continue // avoid self-comparison of query method
				}
				if m.Id() == queryMethod.Id() {
					posn := safetoken.StartPosition(pkg.FileSet(), m.Pos())
					loc, err := offsetToLocation(ctx, snapshot, posn.Filename, posn.Offset, posn.Offset+len(m.Name()))
					if err != nil {
						return err
					}
					yield(pkg.Metadata().PkgPath, m.Name(), isInterface, loc)
					break
				}
			}
		}
	}

	// Special case: for types that satisfy error,
	// report error in builtin.go (see #59527).
	//
	// (An inconsistency: we always report the type error
	// even when the query was for the method error.Error.)
	if matches(errorType) {
		loc, err := errorLocation(ctx, snapshot)
		if err != nil {
			return err
		}
		yield("", "error", true, loc)
	}

	return nil
}

var errorType = types.Universe.Lookup("error").Type()

// errorLocation returns the location of the 'error' type in builtin.go.
func errorLocation(ctx context.Context, snapshot *cache.Snapshot) (protocol.Location, error) {
	pgf, err := snapshot.BuiltinFile(ctx)
	if err != nil {
		return protocol.Location{}, err
	}
	for _, decl := range pgf.File.Decls {
		if decl, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range decl.Specs {
				if spec, ok := spec.(*ast.TypeSpec); ok && spec.Name.Name == "error" {
					return pgf.NodeLocation(spec.Name)
				}
			}
		}
	}
	return protocol.Location{}, fmt.Errorf("built-in error type not found")
}

// implements reports whether x implements y.
// If one or both types are generic, the result indicates whether the
// interface may be implemented under some instantiation.
func implements(msets *typeutil.MethodSetCache, x, y types.Type) bool {
	if !types.IsInterface(y) {
		return false
	}

	// For each interface method of y, check that x has it too.
	// It is not necessary to compute x's complete method set.
	//
	// If y is a constraint interface (!y.IsMethodSet()), we
	// ignore non-interface terms, leading to occasional spurious
	// matches. We could in future filter based on them, but it
	// would lead to divergence with the global (fingerprint-based)
	// algorithm, which operates only on methodsets.
	ymset := msets.MethodSet(y)
	for method := range ymset.Methods() {
		ym := method.Obj().(*types.Func)

		xobj, _, _ := types.LookupFieldOrMethod(x, false, ym.Pkg(), ym.Name())
		xm, ok := xobj.(*types.Func)
		if !ok {
			return false // x lacks a method of y
		}
		if !unify(xm.Signature(), ym.Signature(), nil) {
			return false // signatures do not match
		}
	}
	return true // all methods found
}

// unify reports whether the types of x and y match.
//
// If unifier is nil, unify reports only whether it succeeded.
// If unifier is non-nil, it is populated with the values
// of type parameters determined during a successful unification.
// If unification succeeds without binding a type parameter, that parameter
// will not be present in the map.
//
// On entry, the unifier's contents are treated as the values of already-bound type
// parameters, constraining the unification.
//
// For example, if unifier is an empty (not nil) map on entry, then the types
//
//	func[T any](T, int)
//
// and
//
//	func[U any](bool, U)
//
// will unify, with T=bool and U=int.
// That is, the contents of unifier after unify returns will be
//
//	{T: bool, U: int}
//
// where "T" is the type parameter T and "bool" is the basic type for bool.
//
// But if unifier is {T: int} is int on entry, then unification will fail, because T
// does not unify with bool.
//
// Unify does not preserve aliases. For example, given the following:
//
//	type String = string
//	type A[T] = T
//
// unification succeeds with T bound to string, not String.
//
// See also: unify in cache/methodsets/fingerprint, which implements
// unification for type fingerprints, for the global index.
//
// BUG: literal interfaces are not handled properly. But this function is currently
// used only for signatures, where such types are very rare.
func unify(x, y types.Type, unifier map[*types.TypeParam]types.Type) bool {
	// bindings[tp] is the binding for type parameter tp.
	// Although type parameters are nominally bound to types, each bindings[tp]
	// is a pointer to a type, so unbound variables that unify can share a binding.
	bindings := map[*types.TypeParam]*types.Type{}

	// Bindings is initialized with pointers to the provided types.
	for tp, t := range unifier {
		bindings[tp] = &t
	}

	// bindingFor returns the *types.Type in bindings for tp if tp is not nil,
	// creating one if needed.
	bindingFor := func(tp *types.TypeParam) *types.Type {
		if tp == nil {
			return nil
		}
		b := bindings[tp]
		if b == nil {
			b = new(types.Type)
			bindings[tp] = b
		}
		return b
	}

	// bind sets b to t if b does not occur in t.
	bind := func(b *types.Type, t types.Type) bool {
		for tp := range typeParams(t) {
			if b == bindings[tp] {
				return false // failed "occurs" check
			}
		}
		*b = t
		return true
	}

	// uni performs the actual unification.
	depth := 0
	var uni func(x, y types.Type) bool
	uni = func(x, y types.Type) bool {
		// Panic if recursion gets too deep, to detect bugs before
		// overflowing the stack.
		depth++
		defer func() { depth-- }()
		if depth > 100 {
			panic("unify: max depth exceeded")
		}

		x = types.Unalias(x)
		y = types.Unalias(y)

		tpx, _ := x.(*types.TypeParam)
		tpy, _ := y.(*types.TypeParam)
		if tpx != nil || tpy != nil {
			// Identical type params unify.
			if tpx == tpy {
				return true
			}
			bx := bindingFor(tpx)
			by := bindingFor(tpy)

			// If both args are type params and neither is bound, have them share a binding.
			if bx != nil && by != nil && *bx == nil && *by == nil {
				// Arbitrarily give y's binding to x.
				bindings[tpx] = by
				return true
			}
			// Treat param bindings like original args in what follows.
			if bx != nil && *bx != nil {
				x = *bx
			}
			if by != nil && *by != nil {
				y = *by
			}
			// If the x param is unbound, bind it to y.
			if bx != nil && *bx == nil {
				return bind(bx, y)
			}
			// If the y param is unbound, bind it to x.
			if by != nil && *by == nil {
				return bind(by, x)
			}
			// Unify the binding of a bound parameter.
			return uni(x, y)
		}

		// Neither arg is a type param.

		if reflect.TypeOf(x) != reflect.TypeOf(y) {
			return false // mismatched types
		}

		switch x := x.(type) {
		case *types.Array:
			y := y.(*types.Array)
			return x.Len() == y.Len() &&
				uni(x.Elem(), y.Elem())

		case *types.Basic:
			y := y.(*types.Basic)
			return x.Kind() == y.Kind()

		case *types.Chan:
			y := y.(*types.Chan)
			return x.Dir() == y.Dir() &&
				uni(x.Elem(), y.Elem())

		case *types.Interface:
			y := y.(*types.Interface)
			// TODO(adonovan,jba): fix: for correctness, we must check
			// that both interfaces have the same set of methods
			// modulo type parameters, while avoiding the risk of
			// unbounded interface recursion.
			//
			// Since non-empty interface literals are vanishingly
			// rare in methods signatures, we ignore this for now.
			// If more precision is needed we could compare method
			// names and arities, still without full recursion.
			return x.NumMethods() == y.NumMethods()

		case *types.Map:
			y := y.(*types.Map)
			return uni(x.Key(), y.Key()) &&
				uni(x.Elem(), y.Elem())

		case *types.Named:
			y := y.(*types.Named)
			if x.Origin() != y.Origin() {
				return false // different named types
			}
			xtargs := x.TypeArgs()
			ytargs := y.TypeArgs()
			if xtargs.Len() != ytargs.Len() {
				return false // arity error (ill-typed)
			}
			for i := range xtargs.Len() {
				if !uni(xtargs.At(i), ytargs.At(i)) {
					return false // mismatched type args
				}
			}
			return true

		case *types.Pointer:
			y := y.(*types.Pointer)
			return uni(x.Elem(), y.Elem())

		case *types.Signature:
			y := y.(*types.Signature)
			return x.Variadic() == y.Variadic() &&
				uni(x.Params(), y.Params()) &&
				uni(x.Results(), y.Results())

		case *types.Slice:
			y := y.(*types.Slice)
			return uni(x.Elem(), y.Elem())

		case *types.Struct:
			y := y.(*types.Struct)
			if x.NumFields() != y.NumFields() {
				return false
			}
			for i := range x.NumFields() {
				xf := x.Field(i)
				yf := y.Field(i)
				if xf.Embedded() != yf.Embedded() ||
					xf.Name() != yf.Name() ||
					x.Tag(i) != y.Tag(i) ||
					!xf.Exported() && xf.Pkg() != yf.Pkg() ||
					!uni(xf.Type(), yf.Type()) {
					return false
				}
			}
			return true

		case *types.Tuple:
			y := y.(*types.Tuple)
			if x.Len() != y.Len() {
				return false
			}
			for i := range x.Len() {
				if !uni(x.At(i).Type(), y.At(i).Type()) {
					return false
				}
			}
			return true

		default: // incl. *Union, *TypeParam
			panic(fmt.Sprintf("unexpected Type %#v", x))
		}
	}

	if !uni(x, y) {
		clear(unifier)
		return false
	}

	// Populate the input map with the resulting types.
	if unifier != nil {
		for tparam, tptr := range bindings {
			unifier[tparam] = *tptr
		}
	}
	return true
}

// typeParams yields all the free type parameters within t that are relevant for
// unification.
//
// Note: this function is tailored for the specific needs of the unification algorithm.
// Don't try to use it for other purposes, see [typeparams.Free] instead.
func typeParams(t types.Type) iter.Seq[*types.TypeParam] {

	return func(yield func(*types.TypeParam) bool) {
		seen := map[*types.TypeParam]bool{} // yield each type param only once

		// tps(t) yields each TypeParam in t and returns false to stop.
		var tps func(types.Type) bool
		tps = func(t types.Type) bool {
			t = types.Unalias(t)

			switch t := t.(type) {
			case *types.TypeParam:
				if seen[t] {
					return true
				}
				seen[t] = true
				return yield(t)

			case *types.Basic:
				return true

			case *types.Array:
				return tps(t.Elem())

			case *types.Chan:
				return tps(t.Elem())

			case *types.Interface:
				// TODO(jba): implement.
				return true

			case *types.Map:
				return tps(t.Key()) && tps(t.Elem())

			case *types.Named:
				if t.Origin() == t {
					// generic type: look at type params
					return moreiters.Every(t.TypeParams().TypeParams(),
						func(tp *types.TypeParam) bool { return tps(tp) })
				}
				// instantiated type: look at type args
				return moreiters.Every(t.TypeArgs().Types(), tps)

			case *types.Pointer:
				return tps(t.Elem())

			case *types.Signature:
				return tps(t.Params()) && tps(t.Results())

			case *types.Slice:
				return tps(t.Elem())

			case *types.Struct:
				return moreiters.Every(t.Fields(),
					func(v *types.Var) bool { return tps(v.Type()) })

			case *types.Tuple:
				return moreiters.Every(t.Variables(),
					func(v *types.Var) bool { return tps(v.Type()) })

			default: // incl. *Union
				panic(fmt.Sprintf("unexpected Type %#v", t))
			}
		}

		tps(t)
	}
}

var (
	// TODO(adonovan): why do various RPC handlers related to
	// IncomingCalls return (nil, nil) on the protocol in response
	// to this error? That seems like a violation of the protocol.
	// Is it perhaps a workaround for VSCode behavior?
	errNoObjectFound = errors.New("no object found")
)

// --- Implementations based on signature types --

// implFuncs finds Implementations based on func types.
//
// Just as an interface type abstracts a set of concrete methods, a
// function type abstracts a set of concrete functions. Gopls provides
// analogous operations for navigating from abstract to concrete and
// back in the domain of function types.
//
// A single type (for example http.HandlerFunc) can have both an
// underlying type of function (types.Signature) and have methods that
// cause it to implement an interface. To avoid a confusing user
// interface we want to separate the two operations so that the user
// can unambiguously specify the query they want.
//
// So, whereas Implementations queries on interface types are usually
// keyed by an identifier of a named type, Implementations queries on
// function types are keyed by the "func" keyword, or by the "(" of a
// call expression. The query relates two sets of locations:
//
//  1. the "func" token of each function declaration (FuncDecl or
//     FuncLit). These are analogous to declarations of concrete
//     methods.
//
//  2. uses of abstract functions:
//
//     (a) the "func" token of each FuncType that is not part of
//     Func{Decl,Lit}. These are analogous to interface{...} types.
//
//     (b) the "(" paren of each dynamic call on a value of an
//     abstract function type. These are analogous to references to
//     interface method names, but no names are involved, which has
//     historically made them hard to search for.
//
// An Implementations query on a location in set 1 returns set 2,
// and vice versa.
//
// curSel denotes the selected syntax node whose type drives the
// pos indicates the exact cursor position.
//
// implFuncs returns errNotHandled to indicate that we should try the
// regular method-sets algorithm.
func implFuncs(pkg *cache.Package, curSel inspector.Cursor, pos token.Pos) ([]protocol.Location, error) {
	info := pkg.TypesInfo()
	if info.Types == nil || info.Defs == nil || info.Uses == nil {
		panic("one of info.Types, .Defs or .Uses is nil")
	}

	// Find innermost enclosing FuncType or CallExpr.
	//
	// We are looking for specific tokens (FuncType.Func and
	// CallExpr.Lparen), but FindPos prefers an adjoining
	// subexpression: given f(x) without additional spaces between
	// tokens, FindPos always returns either f or x, never the
	// CallExpr itself. Thus we must ascend the tree.
	//
	// Another subtlety: due to an edge case in go/ast, FindPos at
	// FuncDecl.Type.Func does not return FuncDecl.Type, only the
	// FuncDecl, because the orders of tree positions and tokens
	// are inconsistent. Consequently, the ancestors for a "func"
	// token of Func{Lit,Decl} do not include FuncType, hence the
	// explicit cases below.
	for cur := range curSel.Enclosing(
		(*ast.FuncDecl)(nil),
		(*ast.FuncLit)(nil),
		(*ast.FuncType)(nil),
		(*ast.CallExpr)(nil),
	) {
		switch n := cur.Node().(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			if inToken(n.Pos(), "func", pos) {
				// Case 1: concrete function declaration.
				// Report uses of corresponding function types.
				switch n := n.(type) {
				case *ast.FuncDecl:
					return funcUses(pkg, info.Defs[n.Name].Type())
				case *ast.FuncLit:
					return funcUses(pkg, info.TypeOf(n.Type))
				}
			}

		case *ast.FuncType:
			if n.Func.IsValid() && inToken(n.Func, "func", pos) && !beneathFuncDef(cur) {
				// Case 2a: function type.
				// Report declarations of corresponding concrete functions.
				return funcDefs(pkg, info.TypeOf(n))
			}

		case *ast.CallExpr:
			if inToken(n.Lparen, "(", pos) {
				t := dynamicFuncCallType(info, n)
				if t == nil {
					return nil, fmt.Errorf("not a dynamic function call")
				}
				// Case 2b: dynamic call of function value.
				// Report declarations of corresponding concrete functions.
				return funcDefs(pkg, t)
			}
		}
	}

	// It's probably a query of a named type or method.
	// Fall back to the method-sets computation.
	return nil, errNotHandled
}

var errNotHandled = errors.New("not handled")

// funcUses returns all locations in the workspace that are dynamic
// uses of the specified function type.
func funcUses(pkg *cache.Package, t types.Type) ([]protocol.Location, error) {
	var locs []protocol.Location

	// local search
	for _, pgf := range pkg.CompiledGoFiles() {
		for cur := range pgf.Cursor.Preorder((*ast.CallExpr)(nil), (*ast.FuncType)(nil)) {
			var pos, end token.Pos
			var ftyp types.Type
			switch n := cur.Node().(type) {
			case *ast.CallExpr:
				ftyp = dynamicFuncCallType(pkg.TypesInfo(), n)
				pos, end = n.Lparen, n.Lparen+token.Pos(len("("))

			case *ast.FuncType:
				if !beneathFuncDef(cur) {
					// func type (not def)
					ftyp = pkg.TypesInfo().TypeOf(n)
					pos, end = n.Func, n.Func+token.Pos(len("func"))
				}
			}
			if ftyp == nil {
				continue // missing type information
			}
			if unify(t, ftyp, nil) {
				loc, err := pgf.PosLocation(pos, end)
				if err != nil {
					return nil, err
				}
				locs = append(locs, loc)
			}
		}
	}

	// TODO(adonovan): implement global search

	return locs, nil
}

// funcDefs returns all locations in the workspace that define
// functions of the specified type.
func funcDefs(pkg *cache.Package, t types.Type) ([]protocol.Location, error) {
	var locs []protocol.Location

	// local search
	for _, pgf := range pkg.CompiledGoFiles() {
		for curFn := range pgf.Cursor.Preorder((*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)) {
			fn := curFn.Node()
			var ftyp types.Type
			switch fn := fn.(type) {
			case *ast.FuncDecl:
				ftyp = pkg.TypesInfo().Defs[fn.Name].Type()
			case *ast.FuncLit:
				ftyp = pkg.TypesInfo().TypeOf(fn)
			}
			if ftyp == nil {
				continue // missing type information
			}
			if unify(t, ftyp, nil) {
				pos := fn.Pos()
				loc, err := pgf.PosLocation(pos, pos+token.Pos(len("func")))
				if err != nil {
					return nil, err
				}
				locs = append(locs, loc)
			}
		}
	}

	// TODO(adonovan): implement global search, by analogy with
	// methodsets algorithm.
	//
	// One optimization: if any signature type has free package
	// names, look for matches only in packages among the rdeps of
	// those packages.

	return locs, nil
}

// beneathFuncDef reports whether the specified FuncType cursor is a
// child of Func{Decl,Lit}.
func beneathFuncDef(cur inspector.Cursor) bool {
	switch ek, _ := cur.ParentEdge(); ek {
	case edge.FuncDecl_Type, edge.FuncLit_Type:
		return true
	}
	return false
}

// dynamicFuncCallType reports whether call is a dynamic (non-method) function call.
// If so, it returns the function type, otherwise nil.
//
// Tested via ../test/marker/testdata/implementation/signature.txt.
func dynamicFuncCallType(info *types.Info, call *ast.CallExpr) types.Type {
	if typesinternal.ClassifyCall(info, call) == typesinternal.CallDynamic {
		if tv, ok := info.Types[call.Fun]; ok {
			return tv.Type.Underlying()
		}
	}
	return nil
}

// inToken reports whether pos is within the token of
// the specified position and string.
func inToken(tokPos token.Pos, tokStr string, pos token.Pos) bool {
	return tokPos <= pos && pos <= tokPos+token.Pos(len(tokStr))
}
