package pointer

import (
	"bytes"
	"fmt"

	"code.google.com/p/go.tools/go/types"
)

// CanPoint reports whether the type T is pointerlike,
// for the purposes of this analysis.
func CanPoint(T types.Type) bool {
	switch T := T.(type) {
	case *types.Named:
		return CanPoint(T.Underlying())

	case *types.Pointer, *types.Interface, *types.Map, *types.Chan, *types.Signature, *types.Slice:
		return true
	}

	return false // array struct tuple builtin basic
}

// mustDeref returns the element type of its argument, which must be a
// pointer; panic ensues otherwise.
func mustDeref(typ types.Type) types.Type {
	return typ.Underlying().(*types.Pointer).Elem()
}

// A fieldInfo describes one subelement (node) of the flattening-out
// of a type T: the subelement's type and its path from the root of T.
//
// For example, for this type:
//     type line struct{ points []struct{x, y int} }
// flatten() of the inner struct yields the following []fieldInfo:
//    struct{ x, y int }                      ""
//    int                                     ".x"
//    int                                     ".y"
// and flatten(line) yields:
//    struct{ points []struct{x, y int} }     ""
//    struct{ x, y int }                      ".points[*]"
//    int                                     ".points[*].x
//    int                                     ".points[*].y"
//
type fieldInfo struct {
	typ types.Type

	// op and tail describe the path to the element (e.g. ".a#2.b[*].c").
	op   interface{} // *Array: true; *Tuple: int; *Struct: *types.Var; *Named: nil
	tail *fieldInfo
}

// path returns a user-friendly string describing the subelement path.
//
func (fi *fieldInfo) path() string {
	var buf bytes.Buffer
	for p := fi; p != nil; p = p.tail {
		switch op := p.op.(type) {
		case bool:
			fmt.Fprintf(&buf, "[*]")
		case int:
			fmt.Fprintf(&buf, "#%d", op)
		case *types.Var:
			fmt.Fprintf(&buf, ".%s", op.Name())
		}
	}
	return buf.String()
}

// flatten returns a list of directly contained fields in the preorder
// traversal of the type tree of t.  The resulting elements are all
// scalars (basic types or pointerlike types), except for struct/array
// "identity" nodes, whose type is that of the aggregate.
//
// Callers must not mutate the result.
//
func (a *analysis) flatten(t types.Type) []*fieldInfo {
	fl, ok := a.flattenMemo[t]
	if !ok {
		switch t := t.(type) {
		case *types.Named:
			u := t.Underlying()
			if _, ok := u.(*types.Interface); ok {
				// Debuggability hack: don't remove
				// the named type from interfaces as
				// they're very verbose.
				fl = append(fl, &fieldInfo{typ: t})
			} else {
				fl = a.flatten(u)
			}

		case *types.Basic,
			*types.Signature,
			*types.Chan,
			*types.Map,
			*types.Interface,
			*types.Slice,
			*types.Pointer:
			fl = append(fl, &fieldInfo{typ: t})

		case *types.Array:
			fl = append(fl, &fieldInfo{typ: t}) // identity node
			for _, fi := range a.flatten(t.Elem()) {
				fl = append(fl, &fieldInfo{typ: fi.typ, op: true, tail: fi})
			}

		case *types.Struct:
			fl = append(fl, &fieldInfo{typ: t}) // identity node
			for i, n := 0, t.NumFields(); i < n; i++ {
				f := t.Field(i)
				for _, fi := range a.flatten(f.Type()) {
					fl = append(fl, &fieldInfo{typ: fi.typ, op: f, tail: fi})
				}
			}

		case *types.Tuple:
			// No identity node: tuples are never address-taken.
			for i, n := 0, t.Len(); i < n; i++ {
				f := t.At(i)
				for _, fi := range a.flatten(f.Type()) {
					fl = append(fl, &fieldInfo{typ: fi.typ, op: i, tail: fi})
				}
			}

		case *types.Builtin:
			panic("flatten(*types.Builtin)") // not the type of any value

		default:
			panic(t)
		}

		a.flattenMemo[t] = fl
	}

	return fl
}

// sizeof returns the number of pointerlike abstractions (nodes) in the type t.
func (a *analysis) sizeof(t types.Type) uint32 {
	return uint32(len(a.flatten(t)))
}

// offsetOf returns the (abstract) offset of field index within struct
// or tuple typ.
func (a *analysis) offsetOf(typ types.Type, index int) uint32 {
	var offset uint32
	switch t := typ.Underlying().(type) {
	case *types.Tuple:
		for i := 0; i < index; i++ {
			offset += a.sizeof(t.At(i).Type())
		}
	case *types.Struct:
		offset++ // the node for the struct itself
		for i := 0; i < index; i++ {
			offset += a.sizeof(t.Field(i).Type())
		}
	default:
		panic(fmt.Sprintf("offsetOf(%s : %T)", typ, typ))
	}
	return offset
}

// sliceToArray returns the type representing the arrays to which
// slice type slice points.
func sliceToArray(slice types.Type) *types.Array {
	return types.NewArray(slice.Underlying().(*types.Slice).Elem(), 1)
}

// Node set -------------------------------------------------------------------

type nodeset map[nodeid]struct{}

// ---- Accessors ----

func (ns nodeset) String() string {
	var buf bytes.Buffer
	buf.WriteRune('{')
	var sep string
	for n := range ns {
		fmt.Fprintf(&buf, "%sn%d", sep, n)
		sep = ", "
	}
	buf.WriteRune('}')
	return buf.String()
}

// diff returns the set-difference x - y.  nil => empty.
//
// TODO(adonovan): opt: extremely inefficient.  BDDs do this in
// constant time.  Sparse bitvectors are linear but very fast.
func (x nodeset) diff(y nodeset) nodeset {
	var z nodeset
	for k := range x {
		if _, ok := y[k]; !ok {
			z.add(k)
		}
	}
	return z
}

// clone() returns an unaliased copy of x.
func (x nodeset) clone() nodeset {
	return x.diff(nil)
}

// ---- Mutators ----

func (ns *nodeset) add(n nodeid) bool {
	sz := len(*ns)
	if *ns == nil {
		*ns = make(nodeset)
	}
	(*ns)[n] = struct{}{}
	return len(*ns) > sz
}

func (x *nodeset) addAll(y nodeset) bool {
	if y == nil {
		return false
	}
	sz := len(*x)
	if *x == nil {
		*x = make(nodeset)
	}
	for n := range y {
		(*x)[n] = struct{}{}
	}
	return len(*x) > sz
}

// Constraint set -------------------------------------------------------------

type constraintset map[constraint]struct{}

func (cs *constraintset) add(c constraint) bool {
	sz := len(*cs)
	if *cs == nil {
		*cs = make(constraintset)
	}
	(*cs)[c] = struct{}{}
	return len(*cs) > sz
}

// Worklist -------------------------------------------------------------------

// TODO(adonovan): interface may not be general enough for certain
// implementations, e.g. priority queue
//
// Uses double-buffering so nodes can be added during iteration.
type worklist interface {
	empty() bool  // Reports whether active buffer is empty.
	swap() bool   // Switches to the shadow buffer if empty().
	add(nodeid)   // Adds a node to the shadow buffer.
	take() nodeid // Takes a node from the active buffer.  Precondition: !empty().
}

// Horribly naive (and nondeterministic) worklist
// based on two hash-sets.
type mapWorklist struct {
	active, shadow nodeset
}

func (w *mapWorklist) empty() bool {
	return len(w.active) == 0
}

func (w *mapWorklist) swap() bool {
	if w.empty() {
		w.shadow, w.active = w.active, w.shadow
		return true
	}
	return false
}

func (w *mapWorklist) add(n nodeid) {
	w.shadow[n] = struct{}{}
}

func (w *mapWorklist) take() nodeid {
	for k := range w.active {
		delete(w.active, k)
		return k
	}
	panic("worklist.take(): empty active buffer")
}

func makeMapWorklist() worklist {
	return &mapWorklist{make(nodeset), make(nodeset)}
}
