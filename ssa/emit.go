package ssa

// Helpers for emitting SSA instructions.

import (
	"go/token"

	"code.google.com/p/go.tools/go/types"
)

// emitNew emits to f a new (heap Alloc) instruction allocating an
// object of type typ.  pos is the optional source location.
//
func emitNew(f *Function, typ types.Type, pos token.Pos) Value {
	return f.emit(&Alloc{
		typ:  pointer(typ),
		Heap: true,
		pos:  pos,
	})
}

// emitLoad emits to f an instruction to load the address addr into a
// new temporary, and returns the value so defined.
//
func emitLoad(f *Function, addr Value) *UnOp {
	v := &UnOp{Op: token.MUL, X: addr}
	v.setType(addr.Type().Deref())
	f.emit(v)
	return v
}

// emitArith emits to f code to compute the binary operation op(x, y)
// where op is an eager shift, logical or arithmetic operation.
// (Use emitCompare() for comparisons and Builder.logicalBinop() for
// non-eager operations.)
//
func emitArith(f *Function, op token.Token, x, y Value, t types.Type, pos token.Pos) Value {
	switch op {
	case token.SHL, token.SHR:
		x = emitConv(f, x, t)
		y = emitConv(f, y, types.Typ[types.Uint64])

	case token.ADD, token.SUB, token.MUL, token.QUO, token.REM, token.AND, token.OR, token.XOR, token.AND_NOT:
		x = emitConv(f, x, t)
		y = emitConv(f, y, t)

	default:
		panic("illegal op in emitArith: " + op.String())

	}
	v := &BinOp{
		Op: op,
		X:  x,
		Y:  y,
	}
	v.setPos(pos)
	v.setType(t)
	return f.emit(v)
}

// emitCompare emits to f code compute the boolean result of
// comparison comparison 'x op y'.
//
func emitCompare(f *Function, op token.Token, x, y Value, pos token.Pos) Value {
	xt := x.Type().Underlying()
	yt := y.Type().Underlying()

	// Special case to optimise a tagless SwitchStmt so that
	// these are equivalent
	//   switch { case e: ...}
	//   switch true { case e: ... }
	//   if e==true { ... }
	// even in the case when e's type is an interface.
	// TODO(adonovan): opt: generalise to x==true, false!=y, etc.
	if x == vTrue && op == token.EQL {
		if yt, ok := yt.(*types.Basic); ok && yt.Info()&types.IsBoolean != 0 {
			return y
		}
	}

	if types.IsIdentical(xt, yt) {
		// no conversion necessary
	} else if _, ok := xt.(*types.Interface); ok {
		y = emitConv(f, y, x.Type())
	} else if _, ok := yt.(*types.Interface); ok {
		x = emitConv(f, x, y.Type())
	} else if _, ok := x.(*Literal); ok {
		x = emitConv(f, x, y.Type())
	} else if _, ok := y.(*Literal); ok {
		y = emitConv(f, y, x.Type())
	} else {
		// other cases, e.g. channels.  No-op.
	}

	v := &BinOp{
		Op: op,
		X:  x,
		Y:  y,
	}
	v.setPos(pos)
	v.setType(tBool)
	return f.emit(v)
}

// isValuePreserving returns true if a conversion from ut_src to
// ut_dst is value-preserving, i.e. just a change of type.
// Precondition: neither argument is a named type.
//
func isValuePreserving(ut_src, ut_dst types.Type) bool {
	// Identical underlying types?
	if types.IsIdentical(ut_dst, ut_src) {
		return true
	}

	switch ut_dst.(type) {
	case *types.Chan:
		// Conversion between channel types?
		_, ok := ut_src.(*types.Chan)
		return ok

	case *types.Pointer:
		// Conversion between pointers with identical base types?
		_, ok := ut_src.(*types.Pointer)
		return ok

	case *types.Signature:
		// Conversion between f(T) function and (T) func f() method?
		// TODO(adonovan): is this sound?  Discuss with gri.
		_, ok := ut_src.(*types.Signature)
		return ok
	}
	return false
}

// emitConv emits to f code to convert Value val to exactly type typ,
// and returns the converted value.  Implicit conversions are required
// by language assignability rules in assignments, parameter passing,
// etc.
//
func emitConv(f *Function, val Value, typ types.Type) Value {
	t_src := val.Type()

	// Identical types?  Conversion is a no-op.
	if types.IsIdentical(t_src, typ) {
		return val
	}

	ut_dst := typ.Underlying()
	ut_src := t_src.Underlying()

	// Just a change of type, but not value or representation?
	if isValuePreserving(ut_src, ut_dst) {
		c := &ChangeType{X: val}
		c.setType(typ)
		return f.emit(c)
	}

	// Conversion to, or construction of a value of, an interface type?
	if _, ok := ut_dst.(*types.Interface); ok {

		// Assignment from one interface type to another?
		if _, ok := ut_src.(*types.Interface); ok {
			return emitTypeAssert(f, val, typ, token.NoPos)
		}

		// Untyped nil literal?  Return interface-typed nil literal.
		if ut_src == tUntypedNil {
			return nilLiteral(typ)
		}

		// Convert (non-nil) "untyped" literals to their default type.
		// TODO(gri): expose types.isUntyped().
		if t, ok := ut_src.(*types.Basic); ok && t.Info()&types.IsUntyped != 0 {
			val = emitConv(f, val, DefaultType(ut_src))
		}

		mi := &MakeInterface{X: val}
		mi.setType(typ)
		return f.emit(mi)
	}

	// Conversion of a literal to a non-interface type results in
	// a new literal of the destination type and (initially) the
	// same abstract value.  We don't compute the representation
	// change yet; this defers the point at which the number of
	// possible representations explodes.
	if l, ok := val.(*Literal); ok {
		return NewLiteral(l.Value, typ)
	}

	// A representation-changing conversion.
	c := &Convert{X: val}
	c.setType(typ)
	return f.emit(c)
}

// emitStore emits to f an instruction to store value val at location
// addr, applying implicit conversions as required by assignabilty rules.
//
func emitStore(f *Function, addr, val Value) *Store {
	s := &Store{
		Addr: addr,
		Val:  emitConv(f, val, addr.Type().Deref()),
	}
	f.emit(s)
	return s
}

// emitJump emits to f a jump to target, and updates the control-flow graph.
// Postcondition: f.currentBlock is nil.
//
func emitJump(f *Function, target *BasicBlock) {
	b := f.currentBlock
	b.emit(new(Jump))
	addEdge(b, target)
	f.currentBlock = nil
}

// emitIf emits to f a conditional jump to tblock or fblock based on
// cond, and updates the control-flow graph.
// Postcondition: f.currentBlock is nil.
//
func emitIf(f *Function, cond Value, tblock, fblock *BasicBlock) {
	b := f.currentBlock
	b.emit(&If{Cond: cond})
	addEdge(b, tblock)
	addEdge(b, fblock)
	f.currentBlock = nil
}

// emitExtract emits to f an instruction to extract the index'th
// component of tuple, ascribing it type typ.  It returns the
// extracted value.
//
func emitExtract(f *Function, tuple Value, index int, typ types.Type) Value {
	e := &Extract{Tuple: tuple, Index: index}
	// In all cases but one (tSelect's recv), typ is redundant w.r.t.
	// tuple.Type().(*types.Tuple).Values[index].Type.
	e.setType(typ)
	return f.emit(e)
}

// emitTypeAssert emits to f a type assertion value := x.(t) and
// returns the value.  x.Type() must be an interface.
//
func emitTypeAssert(f *Function, x Value, t types.Type, pos token.Pos) Value {
	// Simplify infallible assertions.
	txi := x.Type().Underlying().(*types.Interface)
	if ti, ok := t.Underlying().(*types.Interface); ok {
		// Even when ti==txi, we still need ChangeInterface
		// since it performs a nil-check.
		if isSuperinterface(ti, txi) {
			c := &ChangeInterface{X: x}
			c.setPos(pos)
			c.setType(t)
			return f.emit(c)
		}
	}

	a := &TypeAssert{X: x, AssertedType: t}
	a.setPos(pos)
	a.setType(t)
	return f.emit(a)
}

// emitTypeTest emits to f a type test value,ok := x.(t) and returns
// a (value, ok) tuple.  x.Type() must be an interface.
//
func emitTypeTest(f *Function, x Value, t types.Type, pos token.Pos) Value {
	// TODO(adonovan): opt: simplify infallible tests as per
	// emitTypeAssert, and return (x, vTrue).
	// (Requires that exprN returns a slice of extracted values,
	// not a single Value of type *types.Tuple.)
	a := &TypeAssert{
		X:            x,
		AssertedType: t,
		CommaOk:      true,
	}
	a.setPos(pos)
	a.setType(types.NewTuple(
		types.NewVar(token.NoPos, nil, "value", t),
		varOk,
	))
	return f.emit(a)
}

// emitTailCall emits to f a function call in tail position.  The
// caller is responsible for all fields of 'call' except its type.
// Intended for wrapper methods.
// Precondition: f does/will not use deferred procedure calls.
// Postcondition: f.currentBlock is nil.
//
func emitTailCall(f *Function, call *Call) {
	tresults := f.Signature.Results()
	nr := tresults.Len()
	if nr == 1 {
		call.typ = tresults.At(0).Type()
	} else {
		call.typ = tresults
	}
	tuple := f.emit(call)
	var ret Ret
	switch nr {
	case 0:
		// no-op
	case 1:
		ret.Results = []Value{tuple}
	default:
		for i := 0; i < nr; i++ {
			v := emitExtract(f, tuple, i, tresults.At(i).Type())
			// TODO(adonovan): in principle, this is required:
			//   v = emitConv(f, o.Type, f.Signature.Results[i].Type)
			// but in practice emitTailCall is only used when
			// the types exactly match.
			ret.Results = append(ret.Results, v)
		}
	}
	f.emit(&ret)
	f.currentBlock = nil
}
