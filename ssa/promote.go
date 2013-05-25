package ssa

// This file defines algorithms related to "promotion" of field and
// method selector expressions e.x, such as desugaring implicit field
// and method selections, method-set computation, and construction of
// synthetic "bridge" methods.

import (
	"fmt"

	"code.google.com/p/go.tools/go/types"
)

// anonFieldPath is a linked list of anonymous fields entered by
// breadth-first traversal has entered, rightmost (outermost) first.
// e.g. "e.f" denoting "e.A.B.C.f" would have a path [C, B, A].
// Common tails may be shared.
//
// It is used by various "promotion"-related algorithms.
//
type anonFieldPath struct {
	tail  *anonFieldPath
	index int // index of field within enclosing types.Struct.Fields
	field *types.Field
}

func (p *anonFieldPath) contains(f *types.Field) bool {
	for ; p != nil; p = p.tail {
		if p.field == f {
			return true
		}
	}
	return false
}

// reverse returns the linked list reversed, as a slice.
func (p *anonFieldPath) reverse() []*anonFieldPath {
	n := 0
	for q := p; q != nil; q = q.tail {
		n++
	}
	s := make([]*anonFieldPath, n)
	n = 0
	for ; p != nil; p = p.tail {
		s[len(s)-1-n] = p
		n++
	}
	return s
}

// isIndirect returns true if the path indirects a pointer.
func (p *anonFieldPath) isIndirect() bool {
	for ; p != nil; p = p.tail {
		if isPointer(p.field.Type) {
			return true
		}
	}
	return false
}

// Method Set construction ----------------------------------------

// A candidate is a method eligible for promotion: a method of an
// abstract (interface) or concrete (anonymous struct or named) type,
// along with the anonymous field path via which it is implicitly
// reached.  If there is exactly one candidate for a given id, it will
// be promoted to membership of the original type's method-set.
//
// Candidates with path=nil are trivially members of the original
// type's method-set.
//
type candidate struct {
	method   *types.Func    // method object of abstract or concrete type
	concrete *Function      // actual method (iff concrete)
	path     *anonFieldPath // desugared selector path
}

// For debugging.
func (c candidate) String() string {
	s := ""
	// Inefficient!
	for p := c.path; p != nil; p = p.tail {
		s = "." + p.field.Name + s
	}
	return "@" + s + "." + c.method.Name()
}

// ptrRecv returns true if this candidate has a pointer receiver.
func (c candidate) ptrRecv() bool {
	return c.concrete != nil && isPointer(c.concrete.Signature.Recv().Type())
}

// MethodSet returns the method set for type typ,
// building bridge methods as needed for promoted methods.
// A nil result indicates an empty set.
//
// Thread-safe.
func (p *Program) MethodSet(typ types.Type) MethodSet {
	if !canHaveConcreteMethods(typ, true) {
		return nil
	}

	p.methodSetsMu.Lock()
	defer p.methodSetsMu.Unlock()

	// TODO(adonovan): Using Types as map keys doesn't properly
	// de-dup.  e.g. *NamedType are canonical but *Struct and
	// others are not.  Need to de-dup using typemap.T.
	mset := p.methodSets[typ]
	if mset == nil {
		mset = buildMethodSet(p, typ)
		p.methodSets[typ] = mset
	}
	return mset
}

// buildMethodSet computes the concrete method set for type typ.
// It is the implementation of Program.MethodSet.
//
func buildMethodSet(prog *Program, typ types.Type) MethodSet {
	if prog.mode&LogSource != 0 {
		defer logStack("buildMethodSet %s %T", typ, typ)()
	}

	// cands maps ids (field and method names) encountered at any
	// level of of the breadth-first traversal to a unique
	// promotion candidate.  A nil value indicates a "blocked" id
	// (i.e. a field or ambiguous method).
	//
	// nextcands is the same but carries just the level in progress.
	cands, nextcands := make(map[Id]*candidate), make(map[Id]*candidate)

	var next, list []*anonFieldPath
	list = append(list, nil) // hack: nil means "use typ"

	// For each level of the type graph...
	for len(list) > 0 {
		// Invariant: next=[], nextcands={}.

		// Collect selectors from one level into 'nextcands'.
		// Record the next levels into 'next'.
		for _, node := range list {
			t := typ // first time only
			if node != nil {
				t = node.field.Type
			}
			t = t.Deref()

			if nt, ok := t.(*types.Named); ok {
				nt.ForEachMethod(func(m *types.Func) {
					addCandidate(nextcands, MakeId(m.Name(), m.Pkg()), m, prog.concreteMethods[m], node)
				})
				t = nt.Underlying()
			}

			switch t := t.(type) {
			case *types.Interface:
				t.ForEachMethod(func(m *types.Func) {
					addCandidate(nextcands, MakeId(m.Name(), m.Pkg()), m, nil, node)
				})

			case *types.Struct:
				for i, n := 0, t.NumFields(); i < n; i++ {
					f := t.Field(i)
					nextcands[MakeId(f.Name, f.Pkg)] = nil // a field: block id
					// Queue up anonymous fields for next iteration.
					// Break cycles to ensure termination.
					if f.IsAnonymous && !node.contains(f) {
						next = append(next, &anonFieldPath{node, i, f})
					}
				}
			}
		}

		// Examine collected selectors.
		// Promote unique, non-blocked ones to cands.
		for id, cand := range nextcands {
			delete(nextcands, id)
			if cand == nil {
				// Update cands so we ignore it at all deeper levels.
				// Don't clobber existing (shallower) binding!
				if _, ok := cands[id]; !ok {
					cands[id] = nil // block id
				}
				continue
			}
			if _, ok := cands[id]; ok {
				// Ignore candidate: a shallower binding exists.
			} else {
				cands[id] = cand
			}
		}
		list, next = next, list[:0] // reuse array
	}

	// Build method sets and bridge methods.
	mset := make(MethodSet)
	for id, cand := range cands {
		if cand == nil {
			continue // blocked; ignore
		}
		if cand.ptrRecv() && !(isPointer(typ) || cand.path.isIndirect()) {
			// A candidate concrete method f with receiver
			// *C is promoted into the method set of
			// (non-pointer) E iff the implicit path selection
			// is indirect, e.g. e.A->B.C.f
			continue
		}
		var method *Function
		if cand.path == nil {
			// Trivial member of method-set; no bridge needed.
			method = cand.concrete
		} else {
			method = makeBridgeMethod(prog, typ, cand)
		}
		if method == nil {
			panic("unexpected nil method in method set")
		}
		mset[id] = method
	}
	return mset
}

// addCandidate adds the promotion candidate (method, node) to m[id].
// If m[id] already exists (whether nil or not), m[id] is set to nil.
// If method denotes a concrete method, concrete is its implementation.
//
func addCandidate(m map[Id]*candidate, id Id, method *types.Func, concrete *Function, node *anonFieldPath) {
	prev, found := m[id]
	switch {
	case prev != nil:
		// Two candidates for same selector: ambiguous; block it.
		m[id] = nil
	case found:
		// Already blocked.
	default:
		// A viable candidate.
		m[id] = &candidate{method, concrete, node}
	}
}

// makeBridgeMethod creates a synthetic Function that delegates to a
// "promoted" method.  For example, given these decls:
//
//    type A struct {B}
//    type B struct {*C}
//    type C ...
//    func (*C) f()
//
// then makeBridgeMethod(typ=A, cand={method:(*C).f, path:[B,*C]}) will
// synthesize this bridge method:
//
//    func (a A) f() { return a.B.C->f() }
//
// prog is the program to which the synthesized method will belong.
// typ is the receiver type of the bridge method.  cand is the
// candidate method to be promoted; it may be concrete or an interface
// method.
//
func makeBridgeMethod(prog *Program, typ types.Type, cand *candidate) *Function {
	old := cand.method.Type().(*types.Signature)
	sig := types.NewSignature(types.NewVar(nil, "recv", typ), old.Params(), old.Results(), old.IsVariadic())

	if prog.mode&LogSource != 0 {
		defer logStack("makeBridgeMethod %s, %s, type %s", typ, cand, sig)()
	}

	fn := &Function{
		Name_:     cand.method.Name(),
		Signature: sig,
		Prog:      prog,
	}
	fn.startBody()
	fn.addSpilledParam(sig.Recv())
	createParams(fn)

	// Each bridge method performs a sequence of selections,
	// then tailcalls the promoted method.
	// We use pointer arithmetic (FieldAddr possibly followed by
	// Load) in preference to value extraction (Field possibly
	// preceded by Load).
	var v Value = fn.Locals[0] // spilled receiver
	if isPointer(typ) {
		v = emitLoad(fn, v)
	}
	// Iterate over selections e.A.B.C.f in the natural order [A,B,C].
	for _, p := range cand.path.reverse() {
		// Loop invariant: v holds a pointer to a struct.
		if _, ok := v.Type().Deref().Underlying().(*types.Struct); !ok {
			panic(fmt.Sprint("not a *struct: ", v.Type(), p.field.Type))
		}
		sel := &FieldAddr{
			X:     v,
			Field: p.index,
		}
		sel.setType(pointer(p.field.Type))
		v = fn.emit(sel)
		if isPointer(p.field.Type) {
			v = emitLoad(fn, v)
		}
	}
	if !cand.ptrRecv() {
		v = emitLoad(fn, v)
	}

	var c Call
	if cand.concrete != nil {
		c.Call.Func = cand.concrete
		c.Call.Args = append(c.Call.Args, v)
	} else {
		c.Call.Recv = v
		c.Call.Method = 0
	}
	for _, arg := range fn.Params[1:] {
		c.Call.Args = append(c.Call.Args, arg)
	}
	emitTailCall(fn, &c)
	fn.finishBody()
	return fn
}

// createParams creates parameters for bridge method fn based on its Signature.
func createParams(fn *Function) {
	var last *Parameter
	tparams := fn.Signature.Params()
	for i, n := 0, tparams.Len(); i < n; i++ {
		p := tparams.At(i)
		name := p.Name()
		if name == "" {
			name = fmt.Sprintf("arg%d", i)
		}
		last = fn.addParam(name, p.Type())
	}
	if fn.Signature.IsVariadic() {
		last.Type_ = types.NewSlice(last.Type_)
	}
}

// Thunks for standalone interface methods ----------------------------------------

// makeImethodThunk returns a synthetic thunk function permitting a
// method id of interface typ to be called like a standalone function,
// e.g.:
//
//   type I interface { f(x int) R }
//   m := I.f  // thunk
//   var i I
//   m(i, 0)
//
// The thunk is defined as if by:
//
//   func I.f(i I, x int, ...) R {
//     return i.f(x, ...)
//   }
//
// TODO(adonovan): opt: currently the stub is created even when used
// in call position: I.f(i, 0).  Clearly this is suboptimal.
//
// TODO(adonovan): memoize creation of these functions in the Program.
//
func makeImethodThunk(prog *Program, typ types.Type, id Id) *Function {
	if prog.mode&LogSource != 0 {
		defer logStack("makeImethodThunk %s.%s", typ, id)()
	}
	itf := typ.Underlying().(*types.Interface)
	index, meth := methodIndex(itf, id)
	sig := *meth.Type().(*types.Signature) // copy; shared Values
	fn := &Function{
		Name_:     meth.Name(),
		Signature: &sig,
		Prog:      prog,
	}
	fn.startBody()
	fn.addParam("recv", typ)
	createParams(fn)
	var c Call
	c.Call.Method = index
	c.Call.Recv = fn.Params[0]
	for _, arg := range fn.Params[1:] {
		c.Call.Args = append(c.Call.Args, arg)
	}
	emitTailCall(fn, &c)
	fn.finishBody()
	return fn
}

// Thunks for bound methods ----------------------------------------

// makeBoundMethodThunk returns a synthetic thunk function that
// delegates to a concrete method.  The thunk has one free variable,
// the method's receiver.  Use MakeClosure with such a thunk to
// construct a bound-method closure.
// e.g.:
//
//   type T int
//   func (t T) meth()
//   var t T
//   f := t.meth
//   f() // calls t.meth()
//
// f is a closure of a synthetic thunk defined as if by:
//
//   f := func() { return t.meth() }
//
// TODO(adonovan): memoize creation of these functions in the Program.
//
func makeBoundMethodThunk(prog *Program, meth *Function, recv Value) *Function {
	if prog.mode&LogSource != 0 {
		defer logStack("makeBoundMethodThunk %s", meth)()
	}
	s := meth.Signature
	fn := &Function{
		Name_:     "bound$" + meth.FullName(),
		Signature: types.NewSignature(nil, s.Params(), s.Results(), s.IsVariadic()), // drop recv
		Prog:      prog,
	}

	cap := &Capture{Name_: "recv", Type_: recv.Type()}
	fn.FreeVars = []*Capture{cap}
	fn.startBody()
	createParams(fn)
	var c Call
	c.Call.Func = meth
	c.Call.Args = []Value{cap}
	for _, arg := range fn.Params {
		c.Call.Args = append(c.Call.Args, arg)
	}
	emitTailCall(fn, &c)
	fn.finishBody()
	return fn
}

// Implicit field promotion ----------------------------------------

// For a given struct type and (promoted) field Id, findEmbeddedField
// returns the path of implicit anonymous field selections, and the
// field index of the explicit (=outermost) selection.
//
// TODO(gri): if go/types/operand.go's lookupFieldBreadthFirst were to
// record (e.g. call a client-provided callback) the implicit field
// selection path discovered for a particular ast.SelectorExpr, we could
// eliminate this function.
//
func findPromotedField(st *types.Struct, id Id) (*anonFieldPath, int) {
	// visited records the types that have been searched already.
	// Invariant: keys are all *types.Named.
	// (types.Type is not a sound map key in general.)
	visited := make(map[types.Type]bool)

	var list, next []*anonFieldPath
	i := 0
	st.ForEachField(func(f *types.Field) {
		if f.IsAnonymous {
			list = append(list, &anonFieldPath{nil, i, f})
		}
		i++
	})

	// Search the current level if there is any work to do and collect
	// embedded types of the next lower level in the next list.
	for {
		// look for name in all types at this level
		for _, node := range list {
			typ := node.field.Type.Deref().(*types.Named)
			if visited[typ] {
				continue
			}
			visited[typ] = true

			switch typ := typ.Underlying().(type) {
			case *types.Struct:
				for i, n := 0, typ.NumFields(); i < n; i++ {
					f := typ.Field(i)
					if MakeId(f.Name, f.Pkg) == id {
						return node, i
					}
				}
				i := 0
				typ.ForEachField(func(f *types.Field) {
					if f.IsAnonymous {
						next = append(next, &anonFieldPath{node, i, f})
					}
					i++
				})
			}
		}

		if len(next) == 0 {
			panic("field not found: " + id.String())
		}

		// No match so far.
		list, next = next, list[:0] // reuse arrays
	}
	panic("unreachable")
}
