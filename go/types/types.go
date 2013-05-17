// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import "go/ast"

// A Type represents a type of Go.
// All types implement the Type interface.
type Type interface {
	// Underlying returns the underlying type of a type.
	Underlying() Type

	// For a pointer type (or a named type denoting a pointer type),
	// Deref returns the pointer's element type. For all other types,
	// Deref returns the receiver.
	Deref() Type

	// String returns a string representation of a type.
	String() string

	// TODO(gri) Which other functionality should move here?
	// Candidates are all predicates (IsIdentical(), etc.),
	// and some others. What is the design principle?
}

// BasicKind describes the kind of basic type.
type BasicKind int

const (
	Invalid BasicKind = iota // type is invalid

	// predeclared types
	Bool
	Int
	Int8
	Int16
	Int32
	Int64
	Uint
	Uint8
	Uint16
	Uint32
	Uint64
	Uintptr
	Float32
	Float64
	Complex64
	Complex128
	String
	UnsafePointer

	// types for untyped values
	UntypedBool
	UntypedInt
	UntypedRune
	UntypedFloat
	UntypedComplex
	UntypedString
	UntypedNil

	// aliases
	Byte = Uint8
	Rune = Int32
)

// BasicInfo is a set of flags describing properties of a basic type.
type BasicInfo int

// Properties of basic types.
const (
	IsBoolean BasicInfo = 1 << iota
	IsInteger
	IsUnsigned
	IsFloat
	IsComplex
	IsString
	IsUntyped

	IsOrdered   = IsInteger | IsFloat | IsString
	IsNumeric   = IsInteger | IsFloat | IsComplex
	IsConstType = IsBoolean | IsNumeric | IsString
)

// A Basic represents a basic type.
type Basic struct {
	kind BasicKind
	info BasicInfo
	size int64 // use DefaultSizeof to get size
	name string
}

// Kind returns the kind of basic type b.
func (b *Basic) Kind() BasicKind { return b.kind }

// Info returns information about properties of basic type b.
func (b *Basic) Info() BasicInfo { return b.info }

// Name returns the name of basic type b.
func (b *Basic) Name() string { return b.name }

// An Array represents an array type.
type Array struct {
	len int64
	elt Type
}

// NewArray returns a new array type for the given element type and length.
func NewArray(elem Type, len int64) *Array { return &Array{len, elem} }

// Len returns the length of array a.
func (a *Array) Len() int64 { return a.len }

// Elem returns element type of array a.
func (a *Array) Elem() Type { return a.elt }

// A Slice represents a slice type.
type Slice struct {
	elt Type
}

// NewSlice returns a new slice type for the given element type.
func NewSlice(elem Type) *Slice { return &Slice{elem} }

// Elem returns the element type of slice s.
func (s *Slice) Elem() Type { return s.elt }

// A Field represents a field of a struct.
// TODO(gri): Should make this just a Var?
type Field struct {
	Pkg         *Package
	Name        string
	Type        Type
	IsAnonymous bool
}

// A Struct represents a struct type.
type Struct struct {
	fields  []*Field
	tags    []string // field tags; nil of there are no tags
	offsets []int64  // field offsets in bytes, lazily computed
}

func NewStruct(fields []*Field, tags []string) *Struct {
	return &Struct{fields: fields, tags: tags}
}

func (s *Struct) NumFields() int     { return len(s.fields) }
func (s *Struct) Field(i int) *Field { return s.fields[i] }
func (s *Struct) Tag(i int) string {
	if i < len(s.tags) {
		return s.tags[i]
	}
	return ""
}
func (s *Struct) ForEachField(f func(*Field)) {
	for _, fld := range s.fields {
		f(fld)
	}
}

func (f *Field) isMatch(pkg *Package, name string) bool {
	// spec:
	// "Two identifiers are different if they are spelled differently,
	// or if they appear in different packages and are not exported.
	// Otherwise, they are the same."
	if name != f.Name {
		return false
	}
	// f.Name == name
	return ast.IsExported(name) || pkg.path == f.Pkg.path
}

func (s *Struct) fieldIndex(pkg *Package, name string) int {
	for i, f := range s.fields {
		if f.isMatch(pkg, name) {
			return i
		}
	}
	return -1
}

// A Pointer represents a pointer type.
type Pointer struct {
	base Type
}

// NewPointer returns a new pointer type for the given element (base) type.
func NewPointer(elem Type) *Pointer { return &Pointer{elem} }

// Elem returns the element type for the given pointer p.
func (p *Pointer) Elem() Type { return p.base }

// A Tuple represents an ordered list of variables; a nil *Tuple is a valid (empty) tuple.
// Tuples are used as components of signatures and to represent the type of multiple
// assignments; they are not first class types of Go.
type Tuple struct {
	vars []*Var
}

// NewTuple returns a new tuple for the given variables.
func NewTuple(x ...*Var) *Tuple {
	if len(x) > 0 {
		return &Tuple{x}
	}
	return nil
}

// Len returns the number variables of tuple t.
func (t *Tuple) Len() int {
	if t != nil {
		return len(t.vars)
	}
	return 0
}

// At returns the i'th variable of tuple t.
func (t *Tuple) At(i int) *Var { return t.vars[i] }

// ForEach calls f with each variable of tuple t in index order.
// TODO(gri): Do we keep ForEach or should we abandon it in favor or Len and At?
func (t *Tuple) ForEach(f func(*Var)) {
	if t != nil {
		for _, x := range t.vars {
			f(x)
		}
	}
}

// A Signature represents a (non-builtin) function type.
type Signature struct {
	recv       *Var   // nil if not a method
	params     *Tuple // (incoming) parameters from left to right; or nil
	results    *Tuple // (outgoing) results from left to right; or nil
	isVariadic bool   // true if the last parameter's type is of the form ...T
}

// NewSignature returns a new function type for the given receiver, parameters,
// and results, either of which may be nil. If isVariadic is set, the function
// is variadic.
func NewSignature(recv *Var, params, results *Tuple, isVariadic bool) *Signature {
	return &Signature{recv, params, results, isVariadic}
}

// Recv returns the receiver of signature s, or nil.
func (s *Signature) Recv() *Var { return s.recv }

// Params returns the parameters of signature s, or nil.
func (s *Signature) Params() *Tuple { return s.params }

// Results returns the results of signature s, or nil.
func (s *Signature) Results() *Tuple { return s.results }

// IsVariadic reports whether the signature s is variadic.
func (s *Signature) IsVariadic() bool { return s.isVariadic }

// builtinId is an id of a builtin function.
type builtinId int

// Predeclared builtin functions.
const (
	// Universe scope
	_Append builtinId = iota
	_Cap
	_Close
	_Complex
	_Copy
	_Delete
	_Imag
	_Len
	_Make
	_New
	_Panic
	_Print
	_Println
	_Real
	_Recover

	// Unsafe package
	_Alignof
	_Offsetof
	_Sizeof

	// Testing support
	_Assert
	_Trace
)

// A Builtin represents the type of a built-in function.
type Builtin struct {
	id          builtinId
	name        string
	nargs       int // number of arguments (minimum if variadic)
	isVariadic  bool
	isStatement bool // true if the built-in is valid as an expression statement
}

// Name returns the name of the built-in function b.
func (b *Builtin) Name() string {
	return b.name
}

// An Interface represents an interface type.
type Interface struct {
	methods ObjSet
}

// NumMethods returns the number of methods of interface t.
func (t *Interface) NumMethods() int { return len(t.methods.entries) }

// Method returns the i'th method of interface t for 0 <= i < t.NumMethods().
func (t *Interface) Method(i int) *Func {
	return t.methods.entries[i].(*Func)
}

// IsEmpty() reports whether t is an empty interface.
func (t *Interface) IsEmpty() bool { return len(t.methods.entries) == 0 }

// ForEachMethod calls f with each method of interface t in index order.
// TODO(gri) Should we abandon this in favor of NumMethods and Method?
func (t *Interface) ForEachMethod(f func(*Func)) {
	for _, obj := range t.methods.entries {
		f(obj.(*Func))
	}
}

// A Map represents a map type.
type Map struct {
	key, elt Type
}

// NewMap returns a new map for the given key and element types.
func NewMap(key, elem Type) *Map {
	return &Map{key, elem}
}

// Key returns the key type of map m.
func (m *Map) Key() Type { return m.key }

// Elem returns the element type of map m.
func (m *Map) Elem() Type { return m.elt }

// A Chan represents a channel type.
type Chan struct {
	dir ast.ChanDir
	elt Type
}

// NewChan returns a new channel type for the given direction and element type.
func NewChan(dir ast.ChanDir, elem Type) *Chan {
	return &Chan{dir, elem}
}

// Dir returns the direction of channel c.
func (c *Chan) Dir() ast.ChanDir { return c.dir }

// Elem returns the element type of channel c.
func (c *Chan) Elem() Type { return c.elt }

// A Named represents a named type.
type Named struct {
	obj        *TypeName // corresponding declared object
	underlying Type      // nil if not fully declared yet; never a *Named
	methods    ObjSet    // directly associated methods (not the method set of this type)
}

// NewNamed returns a new named type for the given type name, underlying type, and associated methods.
func NewNamed(obj *TypeName, underlying Type, methods ObjSet) *Named {
	typ := &Named{obj, underlying, methods}
	if obj.typ == nil {
		obj.typ = typ
	}
	return typ
}

// TypeName returns the type name for the named type t.
func (t *Named) Obj() *TypeName { return t.obj }

// NumMethods returns the number of methods directly associated with named type t.
func (t *Named) NumMethods() int { return len(t.methods.entries) }

// Method returns the i'th method of named type t for 0 <= i < t.NumMethods().
func (t *Named) Method(i int) *Func {
	return t.methods.entries[i].(*Func)
}

// ForEachMethod calls f with each method associated with t in index order.
// TODO(gri) Should we abandon this in favor of NumMethods and Method?
func (t *Named) ForEachMethod(fn func(*Func)) {
	for _, obj := range t.methods.entries {
		fn(obj.(*Func))
	}
}

// Implementations for Type methods.

func (t *Basic) Underlying() Type     { return t }
func (t *Array) Underlying() Type     { return t }
func (t *Slice) Underlying() Type     { return t }
func (t *Struct) Underlying() Type    { return t }
func (t *Pointer) Underlying() Type   { return t }
func (t *Tuple) Underlying() Type     { return t }
func (t *Signature) Underlying() Type { return t }
func (t *Builtin) Underlying() Type   { return t }
func (t *Interface) Underlying() Type { return t }
func (t *Map) Underlying() Type       { return t }
func (t *Chan) Underlying() Type      { return t }
func (t *Named) Underlying() Type     { return t.underlying }

func (t *Basic) Deref() Type     { return t }
func (t *Array) Deref() Type     { return t }
func (t *Slice) Deref() Type     { return t }
func (t *Struct) Deref() Type    { return t }
func (t *Pointer) Deref() Type   { return t.base }
func (t *Tuple) Deref() Type     { return t }
func (t *Signature) Deref() Type { return t }
func (t *Builtin) Deref() Type   { return t }
func (t *Interface) Deref() Type { return t }
func (t *Map) Deref() Type       { return t }
func (t *Chan) Deref() Type      { return t }
func (t *Named) Deref() Type {
	if p, ok := t.underlying.(*Pointer); ok {
		return p.base
	}
	return t
}

func (t *Basic) String() string     { return typeString(t) }
func (t *Array) String() string     { return typeString(t) }
func (t *Slice) String() string     { return typeString(t) }
func (t *Struct) String() string    { return typeString(t) }
func (t *Pointer) String() string   { return typeString(t) }
func (t *Tuple) String() string     { return typeString(t) }
func (t *Signature) String() string { return typeString(t) }
func (t *Builtin) String() string   { return typeString(t) }
func (t *Interface) String() string { return typeString(t) }
func (t *Map) String() string       { return typeString(t) }
func (t *Chan) String() string      { return typeString(t) }
func (t *Named) String() string     { return typeString(t) }
