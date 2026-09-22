// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

// This file defines utilities for population of method sets.

import (
	"fmt"
	"go/types"

	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/typesinternal"
)

// MethodValue returns the Function implementing method sel, building
// wrapper methods on demand. It returns nil if sel denotes an
// interface or generic method.
//
// Precondition: sel.Kind() == MethodVal.
//
// Thread-safe.
//
// Acquires prog.methodsMu.
func (prog *Program) MethodValue(sel *types.Selection) *Function {
	if sel.Kind() != types.MethodVal {
		panic(fmt.Sprintf("MethodValue(%s) kind != MethodVal", sel))
	}
	T := sel.Recv()
	if types.IsInterface(T) {
		return nil // interface method or type parameter
	}

	// A method has no single Function if it is generic (has its own
	// type parameters) or if its receiver type contains a free type
	// parameter. Check those directly rather than via sel.Type(),
	// which allocates a fresh Signature on every call that would then
	// have to be canonicalized and hashed only to be discarded
	// (#81308): this is the hot path of RTA and VTA.
	obj := sel.Obj().(*types.Func)
	if obj.Type().(*types.Signature).TypeParams().Len() > 0 || prog.isParameterized(T) {
		return nil // generic method or method on generic type
	}

	if prog.mode&LogSource != 0 {
		defer logStack("MethodValue %s %v", T, sel)()
	}

	fn, b := prog.lookupOrCreateMethod(sel, obj)
	if b == nil {
		// Common case: fn was created by an earlier call.
		// Wait for it only if some other builder is still building it.
		if !fn.buildshared.isTransitivelyDone() {
			var b builder
			b.waitForSharedFunction(fn)
			b.iterate()
		}
	} else {
		b.iterate()
	}
	return fn
}

// lookupOrCreateMethod returns the Function implementing method sel
// of the concrete, non-parameterized type sel.Recv(), creating it on
// demand. If it created the Function, it also returns the builder in
// which the new function (and any it depends on) is enqueued, and the
// caller must run that builder to a fixed point; otherwise the builder
// result is nil. Separating the two cases keeps the common hit path
// free of allocations: a builder escapes to the heap.
//
// Acquires prog.methodsMu.
func (prog *Program) lookupOrCreateMethod(sel *types.Selection, obj *types.Func) (*Function, *builder) {
	T := sel.Recv()
	id := obj.Id()

	prog.methodsMu.Lock()
	defer prog.methodsMu.Unlock()

	// Get or create SSA method set.
	mset, ok := prog.methodSets.At(T).(*methodSet)
	if !ok {
		mset = &methodSet{mapping: make(map[string]*Function)}
		prog.methodSets.Set(T, mset)
	}

	// Get or create SSA method.
	if fn, ok := mset.mapping[id]; ok {
		return fn, nil
	}
	b := new(builder)
	var fn *Function
	needsPromotion := len(sel.Index()) > 1
	needsIndirection := !isPointer(recvType(obj)) && isPointer(T)
	if needsPromotion || needsIndirection {
		fn = createWrapper(prog, toSelection(sel), nil)
		fn.buildshared = b.shared()
		b.enqueue(fn)
	} else {
		fn = prog.objectMethod(obj, nil, b)
	}
	if fn.Signature.Recv() == nil {
		panic(fn)
	}
	mset.mapping[id] = fn
	return fn, b
}

// objectMethod returns the Function for a given method symbol.
// The symbol may be an instance of a generic function. It need not
// belong to an existing SSA package created by a call to
// prog.CreatePackage.
//
// objectMethod panics if the function is not a method.
//
// Acquires prog.objectMethodsMu.
func (prog *Program) objectMethod(obj *types.Func, targs []types.Type, b *builder) *Function {
	sig := obj.Type().(*types.Signature)
	if sig.Recv() == nil {
		panic("not a method: " + obj.String())
	}

	// Instantiation of generic?
	if orig := obj.Origin(); orig != obj || len(targs) > 0 {
		return prog.objectMethod(orig, nil, b).instance(receiverTypeArgs(obj), targs, b)
	}

	// Belongs to a created package?
	if fn := prog.FuncValue(obj); fn != nil {
		return fn
	}

	// Consult/update cache of methods created from types.Func.
	prog.objectMethodsMu.Lock()
	defer prog.objectMethodsMu.Unlock()
	fn, ok := prog.objectMethods[obj]
	if !ok {
		fn = createFunction(prog, obj, obj.Name(), nil, nil, "")
		fn.Synthetic = "from type information (on demand)"
		fn.buildshared = b.shared()
		b.enqueue(fn)

		if prog.objectMethods == nil {
			prog.objectMethods = make(map[*types.Func]*Function)
		}
		prog.objectMethods[obj] = fn
	} else {
		b.waitForSharedFunction(fn)
	}
	return fn
}

// LookupMethod returns the implementation of the method of type T
// identified by (pkg, name).  It returns nil if the method exists but
// is an interface method or generic method, and panics if T has no such method.
func (prog *Program) LookupMethod(T types.Type, pkg *types.Package, name string) *Function {
	sel := prog.MethodSets.MethodSet(T).Lookup(pkg, name)
	if sel == nil {
		panic(fmt.Sprintf("%s has no method %s", T, types.Id(pkg, name)))
	}
	return prog.MethodValue(sel)
}

// methodSet contains the (concrete) methods of a concrete type (non-interface, non-parameterized).
type methodSet struct {
	mapping map[string]*Function // populated lazily
}

// RuntimeTypes returns a new unordered slice containing all types in
// the program for which a runtime type is required.
//
// A runtime type is required for any non-parameterized, non-interface
// type that is converted to an interface, or for any type (including
// interface types) derivable from one through reflection.
//
// The methods of such types may be reachable through reflection or
// interface calls even if they are never called directly.
//
// Thread-safe.
//
// Acquires prog.makeInterfaceTypesMu.
func (prog *Program) RuntimeTypes() []types.Type {
	prog.makeInterfaceTypesMu.Lock()
	defer prog.makeInterfaceTypesMu.Unlock()

	// Compute the derived types on demand, since many SSA clients
	// never call RuntimeTypes, and those that do typically call
	// it once (often within ssautil.AllFunctions, which will
	// eventually not use it; see Go issue #69291.) This
	// eliminates the need to eagerly compute all the element
	// types during SSA building.
	var runtimeTypes []types.Type
	var set typeutil.Map // for de-duping identical types
	for t := range prog.makeInterfaceTypes {
		typesinternal.ForEachElement(prog.MethodSets.MethodSet, t, func(t types.Type, access bool) bool {
			if !access {
				return false // inaccessible to reflection
			}
			seen, _ := set.Set(t, true).(bool)
			if !seen {
				runtimeTypes = append(runtimeTypes, t)
			}
			return seen
		})
	}

	return runtimeTypes
}
