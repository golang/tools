// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fingerprint defines a function to [Encode] types as strings
// with the property that identical types have equal string encodings,
// in most cases. In the remaining cases (mostly involving generic
// types), the encodings can be parsed using [Parse] into [Tree] form
// and matched using [Matches].
package fingerprint

import (
	"fmt"
	"go/types"
	"reflect"
	"strconv"
	"strings"
	"text/scanner"
)

// Encode returns an encoding of a [types.Type] such that, in
// most cases, Encode(x) == Encode(y) iff [types.Identical](x, y).
//
// For a minority of types, mostly involving type parameters, identity
// cannot be reduced to string comparison; these types are called
// "tricky", and are indicated by the boolean result.
//
// In general, computing identity correctly for tricky types requires
// the type checker. However, the fingerprint encoding can be parsed
// by [Parse] into a [Tree] form that permits simple matching sufficient
// to allow a type parameter to unify with any subtree; see [Match].
//
// In the standard library, 99.8% of package-level types have a
// non-tricky method-set. The most common exceptions are due to type
// parameters.
//
// fingerprint.Encode is defined only for the signature types of functions
// and methods. It must not be called for "untyped" basic types, nor
// the type of a generic function.
func Encode(t types.Type) (_ string, tricky bool) { return fingerprint(t) }

// A Tree is a parsed form of a fingerprint for use with [Matches].
type Tree struct{ tree sexpr }

// String returns the tree in an unspecified human-readable form.
func (tree Tree) String() string {
	var out strings.Builder
	writeSexpr(&out, tree.tree)
	return out.String()
}

// Parse parses a fingerprint into tree form.
//
// The input must have been produced by [Encode] at the same source
// version; parsing is thus infallible.
func Parse(fp string) Tree {
	return Tree{parseFingerprint(fp)}
}

// Matches reports whether two fingerprint trees match, meaning that
// under some conditions (for example, particular instantiations of
// type parameters) the two types may be identical.
func Matches(x, y Tree) bool {
	return unify(x.tree, y.tree)
}

// Fingerprint syntax
//
// The lexical syntax is essentially Lisp S-expressions:
//
//      expr = STRING | INTEGER | IDENT | '(' expr... ')'
//
// where the tokens are as defined by text/scanner.
//
// The grammar of expression forms is:
//
//      τ = IDENT                       -- named or basic type
//        | (qual STRING IDENT)         -- qualified named type
//        | (array INTEGER τ)
//        | (slice τ)
//        | (ptr τ)
//        | (chan IDENT τ)
//        | (func τ v? τ)               -- signature params, results, variadic?
//        | (map τ τ)
//        | (struct field*)
//        | (tuple τ*)
//        | (interface)                 -- nonempty interface (lossy)
//        | (typeparam INTEGER)
//        | (inst τ τ...)               -- instantiation of a named type
//
//  field = IDENT IDENT STRING τ        -- name, embedded?, tag, type

func fingerprint(t types.Type) (string, bool) {
	var buf strings.Builder
	tricky := false
	var print func(t types.Type)
	print = func(t types.Type) {
		switch t := t.(type) {
		case *types.Alias:
			print(types.Unalias(t))

		case *types.Named:
			targs := t.TypeArgs()
			if targs != nil {
				buf.WriteString("(inst ")
			}
			tname := t.Obj()
			if tname.Pkg() != nil {
				fmt.Fprintf(&buf, "(qual %q %s)", tname.Pkg().Path(), tname.Name())
			} else if tname.Name() != "error" && tname.Name() != "comparable" {
				panic(tname) // error and comparable the only named types with no package
			} else {
				buf.WriteString(tname.Name())
			}
			if targs != nil {
				for t0 := range targs.Types() {
					buf.WriteByte(' ')
					print(t0)
				}
				buf.WriteString(")")
			}

		case *types.Array:
			fmt.Fprintf(&buf, "(array %d ", t.Len())
			print(t.Elem())
			buf.WriteByte(')')

		case *types.Slice:
			buf.WriteString("(slice ")
			print(t.Elem())
			buf.WriteByte(')')

		case *types.Pointer:
			buf.WriteString("(ptr ")
			print(t.Elem())
			buf.WriteByte(')')

		case *types.Map:
			buf.WriteString("(map ")
			print(t.Key())
			buf.WriteByte(' ')
			print(t.Elem())
			buf.WriteByte(')')

		case *types.Chan:
			fmt.Fprintf(&buf, "(chan %d ", t.Dir())
			print(t.Elem())
			buf.WriteByte(')')

		case *types.Tuple:
			buf.WriteString("(tuple")
			for v := range t.Variables() {
				buf.WriteByte(' ')
				print(v.Type())
			}
			buf.WriteByte(')')

		case *types.Basic:
			// Print byte/uint8 as "byte" instead of calling
			// BasicType.String, which prints the two distinctly
			// (even though their Kinds are numerically equal).
			// Ditto for rune/int32.
			switch t.Kind() {
			case types.Byte:
				buf.WriteString("byte")
			case types.Rune:
				buf.WriteString("rune")
			case types.UnsafePointer:
				buf.WriteString(`(qual "unsafe" Pointer)`)
			default:
				if t.Info()&types.IsUntyped != 0 {
					panic("fingerprint of untyped type")
				}
				buf.WriteString(t.String())
			}

		case *types.Signature:
			buf.WriteString("(func ")
			print(t.Params())
			if t.Variadic() {
				buf.WriteString(" v")
			}
			buf.WriteByte(' ')
			print(t.Results())
			buf.WriteByte(')')

		case *types.Struct:
			// Non-empty unnamed struct types in method
			// signatures are vanishingly rare.
			buf.WriteString("(struct")
			for i := range t.NumFields() {
				f := t.Field(i)
				name := f.Name()
				if !f.Exported() {
					name = fmt.Sprintf("(qual %q %s)", f.Pkg().Path(), name)
				}

				// This isn't quite right for embedded type aliases.
				// (See types.TypeString(StructType) and #44410 for context.)
				// But this is vanishingly rare.
				fmt.Fprintf(&buf, " %s %t %q ", name, f.Embedded(), t.Tag(i))
				print(f.Type())
			}
			buf.WriteByte(')')

		case *types.Interface:
			if t.NumMethods() == 0 {
				buf.WriteString("any") // common case
			} else {
				// Interface assignability is particularly
				// tricky due to the possibility of recursion.
				// However, nontrivial interface type literals
				// are exceedingly rare in function signatures.
				//
				// TODO(adonovan): add disambiguating precision
				// (e.g. number of methods, their IDs and arities)
				// as needs arise (i.e. collisions are observed).
				tricky = true
				buf.WriteString("(interface)")
			}

		case *types.TypeParam:
			// Matching of type parameters will require
			// parsing fingerprints and unification.
			tricky = true
			fmt.Fprintf(&buf, "(%s %d)", symTypeparam, t.Index())

		default: // incl. *types.Union
			panic(t)
		}
	}

	print(t)

	return buf.String(), tricky
}

// sexpr defines the representation of a fingerprint tree.
type (
	sexpr  any // = string | int | symbol | *cons | nil
	symbol string
	cons   struct{ car, cdr sexpr }
)

// parseFingerprint returns the type encoded by fp in tree form.
//
// The input must have been produced by [fingerprint] at the same
// source version; parsing is thus infallible.
func parseFingerprint(fp string) sexpr {
	var scan scanner.Scanner
	scan.Error = func(scan *scanner.Scanner, msg string) { panic(msg) }
	scan.Init(strings.NewReader(fp))

	// next scans a token and updates tok.
	var tok rune
	next := func() { tok = scan.Scan() }

	next()

	// parse parses a fingerprint and returns its tree.
	var parse func() sexpr
	parse = func() sexpr {
		if tok == '(' {
			next()         // consume '('
			var head sexpr // empty list
			tailcdr := &head
			for tok != ')' {
				cell := &cons{car: parse()}
				*tailcdr = cell
				tailcdr = &cell.cdr
			}
			next() // consume ')'
			return head
		}

		s := scan.TokenText()
		switch tok {
		case scanner.Ident:
			next() // consume IDENT
			return symbol(s)

		case scanner.Int:
			next() // consume INT
			i, err := strconv.Atoi(s)
			if err != nil {
				panic(err)
			}
			return i

		case scanner.String:
			next() // consume STRING
			s, err := strconv.Unquote(s)
			if err != nil {
				panic(err)
			}
			return s

		default:
			panic(tok)
		}
	}

	return parse()
}

// writeSexpr formats an S-expression.
// It is provided for debugging.
func writeSexpr(out *strings.Builder, x sexpr) {
	switch x := x.(type) {
	case nil:
		out.WriteString("()")
	case string:
		fmt.Fprintf(out, "%q", x)
	case int:
		fmt.Fprintf(out, "%d", x)
	case symbol:
		out.WriteString(string(x))
	case *cons:
		out.WriteString("(")
		for {
			writeSexpr(out, x.car)
			if x.cdr == nil {
				break
			} else if cdr, ok := x.cdr.(*cons); ok {
				x = cdr
				out.WriteByte(' ')
			} else {
				// Dotted list: should never happen,
				// but support it for debugging.
				out.WriteString(" . ")
				print(x.cdr)
				break
			}
		}
		out.WriteString(")")
	default:
		panic(x)
	}
}

// unify reports whether x and y match, in the presence of type parameters.
// The constraints on type parameters are ignored, but each type parameter must
// have a consistent binding.
func unify(x, y sexpr) bool {

	// maxTypeParam returns the maximum type parameter index in x.
	var maxTypeParam func(x sexpr) int
	maxTypeParam = func(x sexpr) int {
		if i := typeParamIndex(x); i >= 0 {
			return i
		}
		if c, ok := x.(*cons); ok {
			return max(maxTypeParam(c.car), maxTypeParam(c.cdr))
		}
		return -1
	}

	// xBindings[i] is the binding for type parameter #i in x, and similarly for y.
	// Although type parameters are nominally bound to sexprs, each bindings[i]
	// is a *sexpr, so unbound variables can share a binding.
	xBindings := make([]*sexpr, maxTypeParam(x)+1)
	for i := range len(xBindings) {
		xBindings[i] = new(sexpr)
	}
	yBindings := make([]*sexpr, maxTypeParam(y)+1)
	for i := range len(yBindings) {
		yBindings[i] = new(sexpr)
	}

	// bind sets binding b to s from bindings if it does not occur in s.
	bind := func(b *sexpr, s sexpr, bindings []*sexpr) bool {
		// occurs reports whether b is present in s.
		var occurs func(s sexpr) bool
		occurs = func(s sexpr) bool {
			if j := typeParamIndex(s); j >= 0 {
				return b == bindings[j]
			}
			if c, ok := s.(*cons); ok {
				return occurs(c.car) || occurs(c.cdr)
			}
			return false
		}

		if occurs(s) {
			return false
		}
		*b = s
		return true
	}

	var uni func(x, y sexpr) bool
	uni = func(x, y sexpr) bool {
		var bx, by *sexpr
		ix := typeParamIndex(x)
		if ix >= 0 {
			bx = xBindings[ix]
		}
		iy := typeParamIndex(y)
		if iy >= 0 {
			by = yBindings[iy]
		}

		if bx != nil || by != nil {
			// If both args are type params and neither is bound, have them share a binding.
			if bx != nil && by != nil && *bx == nil && *by == nil {
				xBindings[ix] = yBindings[iy]
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
				return bind(bx, y, yBindings)
			}
			// If the y param is unbound, bind it to x.
			if by != nil && *by == nil {
				return bind(by, x, xBindings)
			}
			// Unify the binding of a bound parameter.
			return uni(x, y)
		}

		// Neither arg is a type param.
		if reflect.TypeOf(x) != reflect.TypeOf(y) {
			return false // type mismatch
		}
		switch x := x.(type) {
		case nil, string, int, symbol:
			return x == y
		case *cons:
			y := y.(*cons)
			if !uni(x.car, y.car) {
				return false
			}
			if x.cdr == nil {
				return y.cdr == nil
			}
			if y.cdr == nil {
				return false
			}
			return uni(x.cdr, y.cdr)
		default:
			panic(fmt.Sprintf("unify %T %T", x, y))
		}
	}
	// At least one param is bound. Unify its binding with the other.
	return uni(x, y)
}

// typeParamIndex returns the index of the type parameter,
// if x has the form "(typeparam INTEGER)", otherwise -1.
func typeParamIndex(x sexpr) int {
	if x, ok := x.(*cons); ok {
		if sym, ok := x.car.(symbol); ok && sym == symTypeparam {
			return x.cdr.(*cons).car.(int)
		}
	}
	return -1
}

const symTypeparam = "typeparam"
