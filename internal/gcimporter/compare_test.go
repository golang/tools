// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcimporter_test

// This file defines helpers for comparing decoded packages with the originals.

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"math/big"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/internal/gcimporter"
)

// fileLine returns the file and line (and, optionally, column) of obj.
func fileLine(fset *token.FileSet, obj types.Object, column bool) string {
	posn := fset.Position(obj.Pos())
	filename := filepath.Clean(strings.ReplaceAll(posn.Filename, "$GOROOT", runtime.GOROOT()))
	if column {
		return fmt.Sprintf("%s:%d:%d", filename, posn.Line, posn.Column)
	}
	return fmt.Sprintf("%s:%d", filename, posn.Line)
}

func equalType(x, y types.Type) error {
	x = types.Unalias(x)
	y = types.Unalias(y)
	if reflect.TypeOf(x) != reflect.TypeOf(y) {
		return fmt.Errorf("unequal kinds: %T vs %T", x, y)
	}
	switch x := x.(type) {
	case *types.Interface:
		y := y.(*types.Interface)
		if x.NumEmbeddeds() != y.NumEmbeddeds() {
			return fmt.Errorf("unequal number of embedded interfaces: %d vs %d",
				x.NumEmbeddeds(), y.NumEmbeddeds())
		}
		for i := 0; i < x.NumEmbeddeds(); i++ {
			xi := x.EmbeddedType(i)
			yi := y.EmbeddedType(i)
			if xi.String() != yi.String() {
				return fmt.Errorf("mismatched %dth embedded interface: %s vs %s",
					i, xi, yi)
			}
		}
		if x.NumExplicitMethods() != y.NumExplicitMethods() {
			return fmt.Errorf("unequal methods: %d vs %d",
				x.NumExplicitMethods(), y.NumExplicitMethods())
		}
		for i := 0; i < x.NumExplicitMethods(); i++ {
			xm := x.ExplicitMethod(i)
			ym := y.ExplicitMethod(i)
			if xm.Name() != ym.Name() {
				return fmt.Errorf("mismatched %dth method: %s vs %s", i, xm, ym)
			}
			if err := equalType(xm.Type(), ym.Type()); err != nil {
				return fmt.Errorf("mismatched %s method: %s", xm.Name(), err)
			}
		}
		if x.NumMethods() != y.NumMethods() {
			return fmt.Errorf("unequal methods: %d vs %d",
				x.NumMethods(), y.NumMethods())
		}
		for i := 0; i < x.NumMethods(); i++ {
			xm := x.Method(i)
			ym := y.Method(i)
			if xm.Name() != ym.Name() {
				return fmt.Errorf("mismatched %dth method: %s vs %s", i, xm, ym)
			}
			if err := equalType(xm.Type(), ym.Type()); err != nil {
				return fmt.Errorf("mismatched %s method: %s", xm.Name(), err)
			}
		}
		// Constraints are handled explicitly in the *TypeParam case below, so we
		// don't yet need to consider embeddeds here.
		// TODO(rfindley): consider the type set here.
	case *types.Array:
		y := y.(*types.Array)
		if x.Len() != y.Len() {
			return fmt.Errorf("unequal array lengths: %d vs %d", x.Len(), y.Len())
		}
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("array elements: %s", err)
		}
	case *types.Basic:
		y := y.(*types.Basic)
		if x.Kind() != y.Kind() {
			return fmt.Errorf("unequal basic types: %s vs %s", x, y)
		}
	case *types.Chan:
		y := y.(*types.Chan)
		if x.Dir() != y.Dir() {
			return fmt.Errorf("unequal channel directions: %d vs %d", x.Dir(), y.Dir())
		}
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("channel elements: %s", err)
		}
	case *types.Map:
		y := y.(*types.Map)
		if err := equalType(x.Key(), y.Key()); err != nil {
			return fmt.Errorf("map keys: %s", err)
		}
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("map values: %s", err)
		}
	case *types.Named:
		y := y.(*types.Named)
		return cmpNamed(x, y)
	case *types.Pointer:
		y := y.(*types.Pointer)
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("pointer elements: %s", err)
		}
	case *types.Signature:
		y := y.(*types.Signature)
		if err := equalType(x.Params(), y.Params()); err != nil {
			return fmt.Errorf("parameters: %s", err)
		}
		if err := equalType(x.Results(), y.Results()); err != nil {
			return fmt.Errorf("results: %s", err)
		}
		if x.Variadic() != y.Variadic() {
			return fmt.Errorf("unequal variadicity: %t vs %t",
				x.Variadic(), y.Variadic())
		}
		if (x.Recv() != nil) != (y.Recv() != nil) {
			return fmt.Errorf("unequal receivers: %s vs %s", x.Recv(), y.Recv())
		}
		if x.Recv() != nil {
			if xk, yk := x.Recv().Kind(), y.Recv().Kind(); xk != yk {
				return fmt.Errorf("receiver: unequal var kinds: %v vs %v", xk, yk)
			}
			// TODO(adonovan): enable this check. It fails for the methods of
			// a defined type whose underlying type is another named
			// interface, such as Error in "type E error": the receiver is
			// the declaring interface (error) when type-checked from source,
			// but the defined type (E) when decoded.
			// if xr, yr := x.Recv().Type().String(), y.Recv().Type().String(); xr != yr {
			// 	return fmt.Errorf("unequal receiver types: %s vs %s", xr, yr)
			// }
		}
		if err := equalTypeParams(x.TypeParams(), y.TypeParams()); err != nil {
			return fmt.Errorf("type params: %s", err)
		}
		if err := equalTypeParams(x.RecvTypeParams(), y.RecvTypeParams()); err != nil {
			return fmt.Errorf("recv type params: %s", err)
		}
	case *types.Slice:
		y := y.(*types.Slice)
		if err := equalType(x.Elem(), y.Elem()); err != nil {
			return fmt.Errorf("slice elements: %s", err)
		}
	case *types.Struct:
		y := y.(*types.Struct)
		if x.NumFields() != y.NumFields() {
			return fmt.Errorf("unequal struct fields: %d vs %d",
				x.NumFields(), y.NumFields())
		}
		for i := 0; i < x.NumFields(); i++ {
			xf := x.Field(i)
			yf := y.Field(i)
			if xf.Name() != yf.Name() {
				return fmt.Errorf("mismatched fields: %s vs %s", xf, yf)
			}
			if err := equalType(xf.Type(), yf.Type()); err != nil {
				return fmt.Errorf("struct field %s: %s", xf.Name(), err)
			}
			if x.Tag(i) != y.Tag(i) {
				return fmt.Errorf("struct field %s has unequal tags: %q vs %q",
					xf.Name(), x.Tag(i), y.Tag(i))
			}
		}
	case *types.Tuple:
		y := y.(*types.Tuple)
		if x.Len() != y.Len() {
			return fmt.Errorf("unequal tuple lengths: %d vs %d", x.Len(), y.Len())
		}
		for i := 0; i < x.Len(); i++ {
			if err := equalType(x.At(i).Type(), y.At(i).Type()); err != nil {
				return fmt.Errorf("tuple element %d: %s", i, err)
			}
			if xk, yk := x.At(i).Kind(), y.At(i).Kind(); xk != yk {
				return fmt.Errorf("tuple element %d: unequal var kinds: %v vs %v", i, xk, yk)
			}
		}
	case *types.TypeParam:
		y := y.(*types.TypeParam)
		if x.String() != y.String() {
			return fmt.Errorf("unequal named types: %s vs %s", x, y)
		}
		// For now, just compare constraints by type string to short-circuit
		// cycles. We have to make interfaces explicit as export data currently
		// doesn't support marking interfaces as implicit.
		// TODO(rfindley): remove makeExplicit once export data contains an
		// implicit bit.
		xc := makeExplicit(x.Constraint()).String()
		yc := makeExplicit(y.Constraint()).String()
		if xc != yc {
			return fmt.Errorf("unequal constraints: %s vs %s", xc, yc)
		}

	default:
		panic(fmt.Sprintf("unexpected %T type", x))
	}
	return nil
}

// cmpNamed compares two named types x and y, returning an error for any
// discrepancies. It does not compare their underlying types.
func cmpNamed(x, y *types.Named) error {
	xOrig := x.Origin()
	yOrig := y.Origin()
	if xOrig.String() != yOrig.String() {
		return fmt.Errorf("unequal named types: %s vs %s", x, y)
	}
	if err := equalTypeParams(x.TypeParams(), y.TypeParams()); err != nil {
		return fmt.Errorf("type parameters: %s", err)
	}
	if err := equalTypeArgs(x.TypeArgs(), y.TypeArgs()); err != nil {
		return fmt.Errorf("type arguments: %s", err)
	}
	if x.NumMethods() != y.NumMethods() {
		return fmt.Errorf("unequal methods: %d vs %d",
			x.NumMethods(), y.NumMethods())
	}
	// Unfortunately method sorting is not canonical, so sort before comparing.
	var xms, yms []*types.Func
	for i := 0; i < x.NumMethods(); i++ {
		xms = append(xms, x.Method(i))
		yms = append(yms, y.Method(i))
	}
	for _, ms := range [][]*types.Func{xms, yms} {
		sort.Slice(ms, func(i, j int) bool {
			return ms[i].Name() < ms[j].Name()
		})
	}
	for i, xm := range xms {
		ym := yms[i]
		if xm.Name() != ym.Name() {
			return fmt.Errorf("mismatched %dth method: %s vs %s", i, xm, ym)
		}
		// Calling equalType here leads to infinite recursion, so just compare
		// strings.
		if xm.String() != ym.String() {
			return fmt.Errorf("unequal methods: %s vs %s", xm, ym)
		}
	}
	return nil
}

// makeExplicit returns an explicit version of typ, if typ is an implicit
// interface. Otherwise it returns typ unmodified.
func makeExplicit(typ types.Type) types.Type {
	if iface, _ := typ.(*types.Interface); iface != nil && iface.IsImplicit() {
		var methods []*types.Func
		for i := 0; i < iface.NumExplicitMethods(); i++ {
			methods = append(methods, iface.Method(i))
		}
		var embeddeds []types.Type
		for etyp := range iface.EmbeddedTypes() {
			embeddeds = append(embeddeds, etyp)
		}
		return types.NewInterfaceType(methods, embeddeds)
	}
	return typ
}

func equalTypeArgs(x, y *types.TypeList) error {
	if x.Len() != y.Len() {
		return fmt.Errorf("unequal lengths: %d vs %d", x.Len(), y.Len())
	}
	for i := 0; i < x.Len(); i++ {
		if err := equalType(x.At(i), y.At(i)); err != nil {
			return fmt.Errorf("type %d: %s", i, err)
		}
	}
	return nil
}

func equalTypeParams(x, y *types.TypeParamList) error {
	if x.Len() != y.Len() {
		return fmt.Errorf("unequal lengths: %d vs %d", x.Len(), y.Len())
	}
	for i := 0; i < x.Len(); i++ {
		if err := equalType(x.At(i), y.At(i)); err != nil {
			return fmt.Errorf("type parameter %d: %s", i, err)
		}
	}
	return nil
}

// testPkgData imports a package from export data and compares it with pkg.
func testPkgData(t *testing.T, fset *token.FileSet, pkg *types.Package, exportdata []byte) *types.Package {
	imports := make(map[string]*types.Package)
	fset2 := token.NewFileSet()
	pkg2, err := gcimporter.Decode(fset2, pkg.Path(), exportdata, gcimporter.PackagesFromMap(imports), gcimporter.DecodeOptions{})
	if err != nil {
		t.Errorf("Decode(%s): %v", pkg.Path(), err)
	}

	testPkg(t, fset, pkg, fset2, pkg2, true)
	return pkg2
}

// testPkg compares the decoded package pkg2 with pkg, type-checked
// from source. If exact, positions must agree on the column too, as
// they do for data written by the current encoder (but not, for
// example, for data written by older versions of cmd/export).
func testPkg(t *testing.T, fset *token.FileSet, pkg *types.Package, fset2 *token.FileSet, pkg2 *types.Package, exact bool) {
	if _, err := gcimporter.Encode(fset2, pkg2, gcimporter.EncodeOptions{}); err != nil {
		t.Errorf("reexport %q: %v", pkg.Path(), err)
	}

	// Compare the packages' corresponding members.
	for _, name := range pkg.Scope().Names() {
		if !token.IsExported(name) {
			continue
		}
		obj1 := pkg.Scope().Lookup(name)
		obj2 := pkg2.Scope().Lookup(name)
		if obj2 == nil {
			t.Errorf("%s.%s not found, want %s", pkg.Path(), name, obj1)
			continue
		}

		fl1 := fileLine(fset, obj1, exact)
		fl2 := fileLine(fset2, obj2, exact)
		if fl1 != fl2 {
			t.Errorf("%s.%s: got posn %s, want %s",
				pkg.Path(), name, fl2, fl1)
		}

		if err := cmpObj(obj1, obj2); err != nil {
			t.Errorf("%s.%s: %s\ngot:  %s\nwant: %s",
				pkg.Path(), name, err, obj2, obj1)
		}
	}
}

// cmpObj reports how x and y differ. They are assumed to belong to different
// universes so cannot be compared directly. It is an adapted version of
// equalObj in bexport_test.go.
func cmpObj(x, y types.Object) error {
	if reflect.TypeOf(x) != reflect.TypeOf(y) {
		return fmt.Errorf("%T vs %T", x, y)
	}
	xt := x.Type()
	yt := y.Type()
	switch x := x.(type) {
	case *types.Var, *types.Func:
		// ok
	case *types.Const:
		xval := x.Val()
		yval := y.(*types.Const).Val()
		equal := constant.Compare(xval, token.EQL, yval)
		if !equal {
			// try approx. comparison
			xkind := xval.Kind()
			ykind := yval.Kind()
			if xkind == constant.Complex || ykind == constant.Complex {
				equal = same(constant.Real(xval), constant.Real(yval)) &&
					same(constant.Imag(xval), constant.Imag(yval))
			} else if xkind == constant.Float || ykind == constant.Float {
				equal = same(xval, yval)
			} else if xkind == constant.Unknown && ykind == constant.Unknown {
				equal = true
			}
		}
		if !equal {
			return fmt.Errorf("unequal constants %s vs %s", xval, yval)
		}
	case *types.TypeName:
		if xalias, yalias := x.IsAlias(), y.(*types.TypeName).IsAlias(); xalias != yalias {
			return fmt.Errorf("mismatching IsAlias(): %s vs %s", x, y)
		}

		// equalType does not recurse into the underlying types of named types, so
		// we must pass the underlying type explicitly here. However, in doing this
		// we may skip checking the features of the named types themselves, in
		// situations where the type name is not referenced by the underlying or
		// any other top-level declarations. Therefore, we must explicitly compare
		// named types here, before passing their underlying types into equalType.
		xn, _ := types.Unalias(xt).(*types.Named)
		yn, _ := types.Unalias(yt).(*types.Named)
		if (xn == nil) != (yn == nil) {
			return fmt.Errorf("mismatching types: %T vs %T", xt, yt)
		}
		if xn != nil {
			if err := cmpNamed(xn, yn); err != nil {
				return err
			}
		}
		xt = xt.Underlying()
		yt = yt.Underlying()
	default:
		return fmt.Errorf("unexpected %T", x)
	}
	return equalType(xt, yt)
}

// Use the same floating-point precision (512) as cmd/compile
// (see Mpprec in cmd/compile/internal/gc/mpfloat.go).
const mpprec = 512

// same compares non-complex numeric values and reports if they are approximately equal.
func same(x, y constant.Value) bool {
	xf := gcimporter.ConstantToFloat(x)
	yf := gcimporter.ConstantToFloat(y)
	d := new(big.Float).Sub(xf, yf)
	d.Abs(d)
	eps := big.NewFloat(1.0 / (1 << (mpprec - 1))) // allow for 1 bit of error
	return d.Cmp(eps) < 0
}

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }
