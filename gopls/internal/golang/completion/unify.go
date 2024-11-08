// Below was copied from go/types/unify.go on September 24, 2024,
// and combined with snippets from other files as well.
// It is copied to implement unification for code completion inferences,
// in lieu of an official type unification API.
//
// TODO: When such an API is available, the code below should deleted.
//
// Due to complexity of extracting private types from the go/types package,
// the unifier does not fully implement interface unification.
//
// The code has been modified to compile without introducing any key functionality changes.
//

// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements type unification.
//
// Type unification attempts to make two types x and y structurally
// equivalent by determining the types for a given list of (bound)
// type parameters which may occur within x and y. If x and y are
// structurally different (say []T vs chan T), or conflicting
// types are determined for type parameters, unification fails.
// If unification succeeds, as a side-effect, the types of the
// bound type parameters may be determined.
//
// Unification typically requires multiple calls u.unify(x, y) to
// a given unifier u, with various combinations of types x and y.
// In each call, additional type parameter types may be determined
// as a side effect and recorded in u.
// If a call fails (returns false), unification fails.
//
// In the unification context, structural equivalence of two types
// ignores the difference between a defined type and its underlying
// type if one type is a defined type and the other one is not.
// It also ignores the difference between an (external, unbound)
// type parameter and its core type.
// If two types are not structurally equivalent, they cannot be Go
// identical types. On the other hand, if they are structurally
// equivalent, they may be Go identical or at least assignable, or
// they may be in the type set of a constraint.
// Whether they indeed are identical or assignable is determined
// upon instantiation and function argument passing.

package completion

import (
	"fmt"
	"go/types"
	"strings"
)

const (
	// Upper limit for recursion depth. Used to catch infinite recursions
	// due to implementation issues (e.g., see issues go.dev/issue/48619, go.dev/issue/48656).
	unificationDepthLimit = 50

	// Whether to panic when unificationDepthLimit is reached.
	// If disabled, a recursion depth overflow results in a (quiet)
	// unification failure.
	panicAtUnificationDepthLimit = true

	// If enableCoreTypeUnification is set, unification will consider
	// the core types, if any, of non-local (unbound) type parameters.
	enableCoreTypeUnification = true
)

// A unifier maintains a list of type parameters and
// corresponding types inferred for each type parameter.
// A unifier is created by calling newUnifier.
type unifier struct {
	// handles maps each type parameter to its inferred type through
	// an indirection *Type called (inferred type) "handle".
	// Initially, each type parameter has its own, separate handle,
	// with a nil (i.e., not yet inferred) type.
	// After a type parameter P is unified with a type parameter Q,
	// P and Q share the same handle (and thus type). This ensures
	// that inferring the type for a given type parameter P will
	// automatically infer the same type for all other parameters
	// unified (joined) with P.
	handles map[*types.TypeParam]*types.Type
	depth   int // recursion depth during unification
}

// newUnifier returns a new unifier initialized with the given type parameter
// and corresponding type argument lists. The type argument list may be shorter
// than the type parameter list, and it may contain nil types. Matching type
// parameters and arguments must have the same index.
func newUnifier(tparams []*types.TypeParam, targs []types.Type) *unifier {
	handles := make(map[*types.TypeParam]*types.Type, len(tparams))
	// Allocate all handles up-front: in a correct program, all type parameters
	// must be resolved and thus eventually will get a handle.
	// Also, sharing of handles caused by unified type parameters is rare and
	// so it's ok to not optimize for that case (and delay handle allocation).
	for i, x := range tparams {
		var t types.Type
		if i < len(targs) {
			t = targs[i]
		}
		handles[x] = &t
	}
	return &unifier{handles, 0}
}

// unifyMode controls the behavior of the unifier.
type unifyMode uint

const (
	// If unifyModeAssign is set, we are unifying types involved in an assignment:
	// they may match inexactly at the top, but element types must match
	// exactly.
	unifyModeAssign unifyMode = 1 << iota

	// If unifyModeExact is set, types unify if they are identical (or can be
	// made identical with suitable arguments for type parameters).
	// Otherwise, a named type and a type literal unify if their
	// underlying types unify, channel directions are ignored, and
	// if there is an interface, the other type must implement the
	// interface.
	unifyModeExact
)

// This function was copied from go/types/unify.go
//
// unify attempts to unify x and y and reports whether it succeeded.
// As a side-effect, types may be inferred for type parameters.
// The mode parameter controls how types are compared.
func (u *unifier) unify(x, y types.Type, mode unifyMode) bool {
	return u.nify(x, y, mode)
}

type typeParamsById []*types.TypeParam

// join unifies the given type parameters x and y.
// If both type parameters already have a type associated with them
// and they are not joined, join fails and returns false.
func (u *unifier) join(x, y *types.TypeParam) bool {
	switch hx, hy := u.handles[x], u.handles[y]; {
	case hx == hy:
		// Both type parameters already share the same handle. Nothing to do.
	case *hx != nil && *hy != nil:
		// Both type parameters have (possibly different) inferred types. Cannot join.
		return false
	case *hx != nil:
		// Only type parameter x has an inferred type. Use handle of x.
		u.setHandle(y, hx)
	// This case is treated like the default case.
	// case *hy != nil:
	// 	// Only type parameter y has an inferred type. Use handle of y.
	//	u.setHandle(x, hy)
	default:
		// Neither type parameter has an inferred type. Use handle of y.
		u.setHandle(x, hy)
	}
	return true
}

// asBoundTypeParam returns x.(*types.TypeParam) if x is a type parameter recorded with u.
// Otherwise, the result is nil.
func (u *unifier) asBoundTypeParam(x types.Type) *types.TypeParam {
	if x, _ := types.Unalias(x).(*types.TypeParam); x != nil {
		if _, found := u.handles[x]; found {
			return x
		}
	}
	return nil
}

// setHandle sets the handle for type parameter x
// (and all its joined type parameters) to h.
func (u *unifier) setHandle(x *types.TypeParam, h *types.Type) {
	hx := u.handles[x]
	for y, hy := range u.handles {
		if hy == hx {
			u.handles[y] = h
		}
	}
}

// at returns the (possibly nil) type for type parameter x.
func (u *unifier) at(x *types.TypeParam) types.Type {
	return *u.handles[x]
}

// set sets the type t for type parameter x;
// t must not be nil.
func (u *unifier) set(x *types.TypeParam, t types.Type) {
	*u.handles[x] = t
}

// unknowns returns the number of type parameters for which no type has been set yet.
func (u *unifier) unknowns() int {
	n := 0
	for _, h := range u.handles {
		if *h == nil {
			n++
		}
	}
	return n
}

// inferred returns the list of inferred types for the given type parameter list.
// The result is never nil and has the same length as tparams; result types that
// could not be inferred are nil. Corresponding type parameters and result types
// have identical indices.
func (u *unifier) inferred(tparams []*types.TypeParam) []types.Type {
	list := make([]types.Type, len(tparams))
	for i, x := range tparams {
		list[i] = u.at(x)
	}
	return list
}

// asInterface returns the underlying type of x as an interface if
// it is a non-type parameter interface. Otherwise it returns nil.
func asInterface(x types.Type) (i *types.Interface) {
	if _, ok := types.Unalias(x).(*types.TypeParam); !ok {
		i, _ = x.Underlying().(*types.Interface)
	}
	return i
}

func isTypeParam(t types.Type) bool {
	_, ok := types.Unalias(t).(*types.TypeParam)
	return ok
}

func asNamed(t types.Type) *types.Named {
	n, _ := types.Unalias(t).(*types.Named)
	return n
}

func isTypeLit(t types.Type) bool {
	switch types.Unalias(t).(type) {
	case *types.Named, *types.TypeParam:
		return false
	}
	return true
}

// identicalOrigin reports whether x and y originated in the same declaration.
func identicalOrigin(x, y *types.Named) bool {
	// TODO(gri) is this correct?
	return x.Origin().Obj() == y.Origin().Obj()
}

func match(x, y types.Type) types.Type {
	// Common case: we don't have channels.
	if types.Identical(x, y) {
		return x
	}

	// We may have channels that differ in direction only.
	if x, _ := x.(*types.Chan); x != nil {
		if y, _ := y.(*types.Chan); y != nil && types.Identical(x.Elem(), y.Elem()) {
			// We have channels that differ in direction only.
			// If there's an unrestricted channel, select the restricted one.
			switch {
			case x.Dir() == types.SendRecv:
				return y
			case y.Dir() == types.SendRecv:
				return x
			}
		}
	}

	// types are different
	return nil
}

func coreType(t types.Type) types.Type {
	t = types.Unalias(t)
	tpar, _ := t.(*types.TypeParam)
	if tpar == nil {
		return t.Underlying()
	}

	return nil
}

func sameId(obj *types.Var, pkg *types.Package, name string, foldCase bool) bool {
	// If we don't care about capitalization, we also ignore packages.
	if foldCase && strings.EqualFold(obj.Name(), name) {
		return true
	}
	// spec:
	// "Two identifiers are different if they are spelled differently,
	// or if they appear in different packages and are not exported.
	// Otherwise, they are the same."
	if obj.Name() != name {
		return false
	}
	// obj.Name == name
	if obj.Exported() {
		return true
	}
	// not exported, so packages must be the same
	if obj.Pkg() != nil && pkg != nil {
		return obj.Pkg() == pkg
	}
	return obj.Pkg().Path() == pkg.Path()
}

// nify implements the core unification algorithm which is an
// adapted version of Checker.identical. For changes to that
// code the corresponding changes should be made here.
// Must not be called directly from outside the unifier.
func (u *unifier) nify(x, y types.Type, mode unifyMode) (result bool) {
	u.depth++
	defer func() {
		u.depth--
	}()

	// nothing to do if x == y
	if x == y || types.Unalias(x) == types.Unalias(y) {
		return true
	}

	// Stop gap for cases where unification fails.
	if u.depth > unificationDepthLimit {
		if panicAtUnificationDepthLimit {
			panic("unification reached recursion depth limit")
		}
		return false
	}

	// Unification is symmetric, so we can swap the operands.
	// Ensure that if we have at least one
	// - defined type, make sure one is in y
	// - type parameter recorded with u, make sure one is in x
	if asNamed(x) != nil || u.asBoundTypeParam(y) != nil {
		x, y = y, x
	}

	// Unification will fail if we match a defined type against a type literal.
	// If we are matching types in an assignment, at the top-level, types with
	// the same type structure are permitted as long as at least one of them
	// is not a defined type. To accommodate for that possibility, we continue
	// unification with the underlying type of a defined type if the other type
	// is a type literal. This is controlled by the exact unification mode.
	// We also continue if the other type is a basic type because basic types
	// are valid underlying types and may appear as core types of type constraints.
	// If we exclude them, inferred defined types for type parameters may not
	// match against the core types of their constraints (even though they might
	// correctly match against some of the types in the constraint's type set).
	// Finally, if unification (incorrectly) succeeds by matching the underlying
	// type of a defined type against a basic type (because we include basic types
	// as type literals here), and if that leads to an incorrectly inferred type,
	// we will fail at function instantiation or argument assignment time.
	//
	// If we have at least one defined type, there is one in y.
	if ny := asNamed(y); mode&unifyModeExact == 0 && ny != nil && isTypeLit(x) {
		y = ny.Underlying()
		// Per the spec, a defined type cannot have an underlying type
		// that is a type parameter.
		// x and y may be identical now
		if x == y || types.Unalias(x) == types.Unalias(y) {
			return true
		}
	}

	// Cases where at least one of x or y is a type parameter recorded with u.
	// If we have at least one type parameter, there is one in x.
	// If we have exactly one type parameter, because it is in x,
	// isTypeLit(x) is false and y was not changed above. In other
	// words, if y was a defined type, it is still a defined type
	// (relevant for the logic below).
	switch px, py := u.asBoundTypeParam(x), u.asBoundTypeParam(y); {
	case px != nil && py != nil:
		// both x and y are type parameters
		if u.join(px, py) {
			return true
		}
		// both x and y have an inferred type - they must match
		return u.nify(u.at(px), u.at(py), mode)

	case px != nil:
		// x is a type parameter, y is not
		if x := u.at(px); x != nil {
			// x has an inferred type which must match y
			if u.nify(x, y, mode) {
				// We have a match, possibly through underlying types.
				xi := asInterface(x)
				yi := asInterface(y)
				xn := asNamed(x) != nil
				yn := asNamed(y) != nil
				// If we have two interfaces, what to do depends on
				// whether they are named and their method sets.
				if xi != nil && yi != nil {
					// Both types are interfaces.
					// If both types are defined types, they must be identical
					// because unification doesn't know which type has the "right" name.
					if xn && yn {
						return types.Identical(x, y)
					}
					return false
					// Below is the original code for reference

					// In all other cases, the method sets must match.
					// The types unified so we know that corresponding methods
					// match and we can simply compare the number of methods.
					// TODO(gri) We may be able to relax this rule and select
					// the more general interface. But if one of them is a defined
					// type, it's not clear how to choose and whether we introduce
					// an order dependency or not. Requiring the same method set
					// is conservative.
					// if len(xi.typeSet().methods) != len(yi.typeSet().methods) {
					// 	return false
					// }
				} else if xi != nil || yi != nil {
					// One but not both of them are interfaces.
					// In this case, either x or y could be viable matches for the corresponding
					// type parameter, which means choosing either introduces an order dependence.
					// Therefore, we must fail unification (go.dev/issue/60933).
					return false
				}
				// If we have inexact unification and one of x or y is a defined type, select the
				// defined type. This ensures that in a series of types, all matching against the
				// same type parameter, we infer a defined type if there is one, independent of
				// order. Type inference or assignment may fail, which is ok.
				// Selecting a defined type, if any, ensures that we don't lose the type name;
				// and since we have inexact unification, a value of equally named or matching
				// undefined type remains assignable (go.dev/issue/43056).
				//
				// Similarly, if we have inexact unification and there are no defined types but
				// channel types, select a directed channel, if any. This ensures that in a series
				// of unnamed types, all matching against the same type parameter, we infer the
				// directed channel if there is one, independent of order.
				// Selecting a directional channel, if any, ensures that a value of another
				// inexactly unifying channel type remains assignable (go.dev/issue/62157).
				//
				// If we have multiple defined channel types, they are either identical or we
				// have assignment conflicts, so we can ignore directionality in this case.
				//
				// If we have defined and literal channel types, a defined type wins to avoid
				// order dependencies.
				if mode&unifyModeExact == 0 {
					switch {
					case xn:
						// x is a defined type: nothing to do.
					case yn:
						// x is not a defined type and y is a defined type: select y.
						u.set(px, y)
					default:
						// Neither x nor y are defined types.
						if yc, _ := y.Underlying().(*types.Chan); yc != nil && yc.Dir() != types.SendRecv {
							// y is a directed channel type: select y.
							u.set(px, y)
						}
					}
				}
				return true
			}
			return false
		}
		// otherwise, infer type from y
		u.set(px, y)
		return true
	}

	// If u.EnableInterfaceInference is set and we don't require exact unification,
	// if both types are interfaces, one interface must have a subset of the
	// methods of the other and corresponding method signatures must unify.
	// If only one type is an interface, all its methods must be present in the
	// other type and corresponding method signatures must unify.

	// Unless we have exact unification, neither x nor y are interfaces now.
	// Except for unbound type parameters (see below), x and y must be structurally
	// equivalent to unify.

	// If we get here and x or y is a type parameter, they are unbound
	// (not recorded with the unifier).
	// Ensure that if we have at least one type parameter, it is in x
	// (the earlier swap checks for _recorded_ type parameters only).
	// This ensures that the switch switches on the type parameter.
	//
	// TODO(gri) Factor out type parameter handling from the switch.
	if isTypeParam(y) {
		x, y = y, x
	}

	// Type elements (array, slice, etc. elements) use emode for unification.
	// Element types must match exactly if the types are used in an assignment.
	emode := mode
	if mode&unifyModeAssign != 0 {
		emode |= unifyModeExact
	}

	// Continue with unaliased types but don't lose original alias names, if any (go.dev/issue/67628).
	xorig, x := x, types.Unalias(x)
	yorig, y := y, types.Unalias(y)

	switch x := x.(type) {
	case *types.Basic:
		// Basic types are singletons except for the rune and byte
		// aliases, thus we cannot solely rely on the x == y check
		// above. See also comment in TypeName.IsAlias.
		if y, ok := y.(*types.Basic); ok {
			return x.Kind() == y.Kind()
		}

	case *types.Array:
		// Two array types unify if they have the same array length
		// and their element types unify.
		if y, ok := y.(*types.Array); ok {
			// If one or both array lengths are unknown (< 0) due to some error,
			// assume they are the same to avoid spurious follow-on errors.
			return (x.Len() < 0 || y.Len() < 0 || x.Len() == y.Len()) && u.nify(x.Elem(), y.Elem(), emode)
		}

	case *types.Slice:
		// Two slice types unify if their element types unify.
		if y, ok := y.(*types.Slice); ok {
			return u.nify(x.Elem(), y.Elem(), emode)
		}

	case *types.Struct:
		// Two struct types unify if they have the same sequence of fields,
		// and if corresponding fields have the same names, their (field) types unify,
		// and they have identical tags. Two embedded fields are considered to have the same
		// name. Lower-case field names from different packages are always different.
		if y, ok := y.(*types.Struct); ok {
			if x.NumFields() == y.NumFields() {
				for i := range x.NumFields() {
					f := x.Field(i)
					g := y.Field(i)
					if f.Embedded() != g.Embedded() ||
						x.Tag(i) != y.Tag(i) ||
						!sameId(f, g.Pkg(), g.Name(), false) ||
						!u.nify(f.Type(), g.Type(), emode) {
						return false
					}
				}
				return true
			}
		}

	case *types.Pointer:
		// Two pointer types unify if their base types unify.
		if y, ok := y.(*types.Pointer); ok {
			return u.nify(x.Elem(), y.Elem(), emode)
		}

	case *types.Tuple:
		// Two tuples types unify if they have the same number of elements
		// and the types of corresponding elements unify.
		if y, ok := y.(*types.Tuple); ok {
			if x.Len() == y.Len() {
				if x != nil {
					for i := range x.Len() {
						v := x.At(i)
						w := y.At(i)
						if !u.nify(v.Type(), w.Type(), mode) {
							return false
						}
					}
				}
				return true
			}
		}

	case *types.Signature:
		// Two function types unify if they have the same number of parameters
		// and result values, corresponding parameter and result types unify,
		// and either both functions are variadic or neither is.
		// Parameter and result names are not required to match.
		// TODO(gri) handle type parameters or document why we can ignore them.
		if y, ok := y.(*types.Signature); ok {
			return x.Variadic() == y.Variadic() &&
				u.nify(x.Params(), y.Params(), emode) &&
				u.nify(x.Results(), y.Results(), emode)
		}

	case *types.Interface:
		return false
		// Below is the original code

		// Two interface types unify if they have the same set of methods with
		// the same names, and corresponding function types unify.
		// Lower-case method names from different packages are always different.
		// The order of the methods is irrelevant.
		// xset := x.typeSet()
		// yset := y.typeSet()
		// if xset.comparable != yset.comparable {
		// 	return false
		// }
		// if !xset.terms.equal(yset.terms) {
		// 	return false
		// }
		// a := xset.methods
		// b := yset.methods
		// if len(a) == len(b) {
		// 	// Interface types are the only types where cycles can occur
		// 	// that are not "terminated" via named types; and such cycles
		// 	// can only be created via method parameter types that are
		// 	// anonymous interfaces (directly or indirectly) embedding
		// 	// the current interface. Example:
		// 	//
		// 	//    type T interface {
		// 	//        m() interface{T}
		// 	//    }
		// 	//
		// 	// If two such (differently named) interfaces are compared,
		// 	// endless recursion occurs if the cycle is not detected.
		// 	//
		// 	// If x and y were compared before, they must be equal
		// 	// (if they were not, the recursion would have stopped);
		// 	// search the ifacePair stack for the same pair.
		// 	//
		// 	// This is a quadratic algorithm, but in practice these stacks
		// 	// are extremely short (bounded by the nesting depth of interface
		// 	// type declarations that recur via parameter types, an extremely
		// 	// rare occurrence). An alternative implementation might use a
		// 	// "visited" map, but that is probably less efficient overall.
		// 	q := &ifacePair{x, y, p}
		// 	for p != nil {
		// 		if p.identical(q) {
		// 			return true // same pair was compared before
		// 		}
		// 		p = p.prev
		// 	}
		// 	if debug {
		// 		assertSortedMethods(a)
		// 		assertSortedMethods(b)
		// 	}
		// 	for i, f := range a {
		// 		g := b[i]
		// 		if f.Id() != g.Id() || !u.nify(f.typ, g.typ, exact, q) {
		// 			return false
		// 		}
		// 	}
		// 	return true
		// }

	case *types.Map:
		// Two map types unify if their key and value types unify.
		if y, ok := y.(*types.Map); ok {
			return u.nify(x.Key(), y.Key(), emode) && u.nify(x.Elem(), y.Elem(), emode)
		}

	case *types.Chan:
		// Two channel types unify if their value types unify
		// and if they have the same direction.
		// The channel direction is ignored for inexact unification.
		if y, ok := y.(*types.Chan); ok {
			return (mode&unifyModeExact == 0 || x.Dir() == y.Dir()) && u.nify(x.Elem(), y.Elem(), emode)
		}

	case *types.Named:
		// Two named types unify if their type names originate in the same type declaration.
		// If they are instantiated, their type argument lists must unify.
		if y := asNamed(y); y != nil {
			// Check type arguments before origins so they unify
			// even if the origins don't match; for better error
			// messages (see go.dev/issue/53692).
			xargs := x.TypeArgs()
			yargs := y.TypeArgs()
			if xargs.Len() != yargs.Len() {
				return false
			}
			for i := range xargs.Len() {
				xarg := xargs.At(i)
				yarg := yargs.At(i)
				if !u.nify(xarg, yarg, mode) {
					return false
				}
			}
			return identicalOrigin(x, y)
		}

	case *types.TypeParam:
		// By definition, a valid type argument must be in the type set of
		// the respective type constraint. Therefore, the type argument's
		// underlying type must be in the set of underlying types of that
		// constraint. If there is a single such underlying type, it's the
		// constraint's core type. It must match the type argument's under-
		// lying type, irrespective of whether the actual type argument,
		// which may be a defined type, is actually in the type set (that
		// will be determined at instantiation time).
		// Thus, if we have the core type of an unbound type parameter,
		// we know the structure of the possible types satisfying such
		// parameters. Use that core type for further unification
		// (see go.dev/issue/50755 for a test case).
		if enableCoreTypeUnification {
			// Because the core type is always an underlying type,
			// unification will take care of matching against a
			// defined or literal type automatically.
			// If y is also an unbound type parameter, we will end
			// up here again with x and y swapped, so we don't
			// need to take care of that case separately.
			if cx := coreType(x); cx != nil {
				// If y is a defined type, it may not match against cx which
				// is an underlying type (incl. int, string, etc.). Use assign
				// mode here so that the unifier automatically takes under(y)
				// if necessary.
				return u.nify(cx, yorig, unifyModeAssign)
			}
		}
		// x != y and there's nothing to do

	case nil:
		// avoid a crash in case of nil type

	default:
		panic(fmt.Sprintf("u.nify(%s, %s, %d)", xorig, yorig, mode))
	}

	return false
}
