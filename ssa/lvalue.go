package ssa

// lvalues are the union of addressable expressions and map-index
// expressions.

import (
	"code.google.com/p/go.tools/go/types"
	"go/token"
)

// An lvalue represents an assignable location that may appear on the
// left-hand side of an assignment.  This is a generalization of a
// pointer to permit updates to elements of maps.
//
type lvalue interface {
	store(fn *Function, v Value) // stores v into the location
	load(fn *Function) Value     // loads the contents of the location
	typ() types.Type             // returns the type of the location
}

// An address is an lvalue represented by a true pointer.
type address struct {
	addr Value
	star token.Pos // source position, if explicit *addr
}

func (a address) load(fn *Function) Value {
	load := emitLoad(fn, a.addr)
	load.pos = a.star
	return load
}

func (a address) store(fn *Function, v Value) {
	emitStore(fn, a.addr, v).pos = a.star
}

func (a address) typ() types.Type {
	return a.addr.Type().Deref()
}

// An element is an lvalue represented by m[k], the location of an
// element of a map or string.  These locations are not addressable
// since pointers cannot be formed from them, but they do support
// load(), and in the case of maps, store().
//
type element struct {
	m, k Value      // map or string
	t    types.Type // map element type or string byte type
}

func (e *element) load(fn *Function) Value {
	l := &Lookup{
		X:     e.m,
		Index: e.k,
	}
	l.setType(e.t)
	return fn.emit(l)
}

func (e *element) store(fn *Function, v Value) {
	fn.emit(&MapUpdate{
		Map:   e.m,
		Key:   e.k,
		Value: emitConv(fn, v, e.t),
	})
}

func (e *element) typ() types.Type {
	return e.t
}

// A blanks is a dummy variable whose name is "_".
// It is not reified: loads are illegal and stores are ignored.
//
type blank struct{}

func (bl blank) load(fn *Function) Value {
	panic("blank.load is illegal")
}

func (bl blank) store(fn *Function, v Value) {
	// no-op
}

func (bl blank) typ() types.Type {
	// This should be the type of the blank Ident; the typechecker
	// doesn't provide this yet, but fortunately, we don't need it
	// yet either.
	panic("blank.typ is unimplemented")
}
