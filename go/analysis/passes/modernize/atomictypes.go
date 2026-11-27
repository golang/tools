// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package modernize

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/internal/analysis/analyzerutil"
	typeindexanalyzer "golang.org/x/tools/internal/analysis/typeindex"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/refactor"
	"golang.org/x/tools/internal/typesinternal"
	"golang.org/x/tools/internal/typesinternal/typeindex"
	"golang.org/x/tools/internal/versions"
)

var AtomicTypesAnalyzer = &analysis.Analyzer{
	Name: "atomictypes",
	Doc:  analyzerutil.MustExtractDoc(doc, "atomictypes"),
	Requires: []*analysis.Analyzer{
		inspect.Analyzer,
		typeindexanalyzer.Analyzer,
	},
	Run: runAtomic,
	URL: "https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize#hdr-Analyzer_atomictypes",
}

// TODO(mkalil): support the Pointer variants.
// Consider the following function signatures for pointer loading:
// func LoadPointer(addr *unsafe.Pointer) (val unsafe.Pointer)
// func (x *Pointer[T]) Load() *T
// Since the former uses *unsafe.Pointer while the latter uses *Pointer[T],
// we would need to determine the type T to apply the transformation, and there
// will be additional edits required to remove any *unsafe.Pointer casts.
// 	"LoadPointer", "StorePointer", "SwapPointer", "CompareAndSwapPointer"

// sync/atomic functions of interest. Some added in go1.19, some added in go1.23.
var syncAtomicFuncs = []string{
	// Added in go1.19.
	"AddInt32", "AddInt64", "AddUint32", "AddUint64", "AddUintptr",
	"CompareAndSwapInt32", "CompareAndSwapInt64", "CompareAndSwapUint32", "CompareAndSwapUint64", "CompareAndSwapUintptr",
	"LoadInt32", "LoadInt64", "LoadUint32", "LoadUint64", "LoadUintptr",
	"StoreInt32", "StoreInt64", "StoreUint32", "StoreUint64", "StoreUintptr",
	"SwapInt32", "SwapInt64", "SwapUint32", "SwapUint64", "SwapUintptr",
	// Added in go1.23.
	"AndInt32", "AndInt64", "AndUint32", "AndUint64", "AndUintptr",
	"OrInt32", "OrInt64", "OrUint32", "OrUint64", "OrUintptr",
}

func runAtomic(pass *analysis.Pass) (any, error) {
	if !typesinternal.Imports(pass.Pkg, "sync/atomic") {
		return nil, nil // doesn't directly import sync/atomic
	}

	var (
		inspect = pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		index   = pass.ResultOf[typeindexanalyzer.Analyzer].(*typeindex.Index)
		info    = pass.TypesInfo
	)

	// Gather all candidate variables v appearing
	// in calls to atomic.AddInt32(&v, ...) et al.
	var (
		atomicPkg *types.Package
		vars      = make(map[*types.Var]string) // maps candidate vars v to the name of the call they appear in
	)
	for _, funcName := range syncAtomicFuncs {
		obj := index.Object("sync/atomic", funcName)
		if obj == nil {
			continue
		}
		atomicPkg = obj.Pkg()
		for curCall := range index.Calls(obj) {
			call := curCall.Node().(*ast.CallExpr)
			if unary, ok := call.Args[0].(*ast.UnaryExpr); ok && unary.Op == token.AND {
				var v *types.Var
				switch x := unary.X.(type) {
				case *ast.Ident:
					v, _ = info.Uses[x].(*types.Var)
				case *ast.SelectorExpr:
					if seln, ok := info.Selections[x]; ok {
						v, _ = seln.Obj().(*types.Var)
					}
				}
				if v != nil && !v.Exported() {
					// v must be a non-exported package or local var, or a struct field.
					switch v.Kind() {
					case types.RecvVar, types.ParamVar, types.ResultVar:
						continue // fix would change func signature
					}
					vars[v] = funcName
				}
			}
		}
	}
	if len(vars) == 0 {
		return nil, nil
	}

	// Gather all fields of struct types used in unkeyed composite literals.
	// Such literals don't contain identifiers for their fields, so they don't
	// appear in the type index's uses of a field object.
	unkeyedFields := make(map[*types.Var]bool)
	for curLit := range inspect.Root().Preorder((*ast.CompositeLit)(nil)) {
		lit := curLit.Node().(*ast.CompositeLit)
		if len(lit.Elts) == 0 {
			continue
		}
		// Cannot mix keyed and unkeyed elements, so this must be an unkeyed literal.
		if _, keyed := lit.Elts[0].(*ast.KeyValueExpr); keyed {
			continue
		}
		typ := info.TypeOf(lit)
		if typ == nil {
			continue
		}
		st, ok := typesinternal.Unpointer(typ).Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for field := range st.Fields() {
			unkeyedFields[field.Origin()] = true
		}
	}

	// Gather all fields of struct types whose values are converted or
	// assigned to a different but identical struct type. Changing the type of
	// such a field would make the conversion or assignment invalid, e.g.
	//
	//	type A struct{ x int32 }
	//	type B struct{ x int32 }
	//	var _ = B(A{}) // invalid if A.x becomes an atomic.Int32
	//
	// Such conversions don't mention the field, so they don't appear in the
	// type index's uses of a field object.
	convertedFields := make(map[*types.Var]bool)
	findConversions(info, inspect.Root(), func(st *types.Struct) {
		for field := range st.Fields() {
			convertedFields[field.Origin()] = true
		}
	})

	// Check that all uses of each candidate variable
	// appear in calls of the form atomic.AddInt32(&v, ...).
nextvar:
	for v, funcName := range vars {
		fixFiles := make(map[*ast.File]bool) // unique files involved in the current fix

		// Check the form of the declaration: var v int or struct { v int }
		def, ok := index.Def(v)
		if !ok {
			continue
		}
		var (
			typ   ast.Expr
			names []*ast.Ident
		)
		switch parent := def.Parent().Node().(type) {
		case *ast.Field: // struct { v int }
			names = parent.Names
			typ = parent.Type
			if unkeyedFields[v.Origin()] {
				// Do not change the type of a field used by an unkeyed literal:
				// its positional element may no longer be assignable.
				continue
			}
			if convertedFields[v.Origin()] {
				// Do not change the type of a field whose struct type is
				// identical to another struct type: conversions and
				// assignments between them would no longer be valid.
				continue
			}
		case *ast.ValueSpec: // var v int
			if len(parent.Values) > 0 {
				// e.g. var v int = 5
				// skip because rewriting as `var v atomic.Int32 = 5` is invalid
				continue
			}
			names = parent.Names
			typ = parent.Type
		}
		if len(names) != 1 || typ == nil {
			continue // v is not the sole var declared here (e.g. var x, y int32); or no explicit type
		}
		oldType := info.TypeOf(typ)                             // e.g. "int32"
		newType := strings.Title(oldType.Underlying().String()) // e.g. "Int32"

		file := astutil.EnclosingFile(def)
		// Get package prefix to avoid shadowing, and import edits, which may be
		// necessary if the fix spans files.
		// For example: file "a" declares an int32, which doesn't require the
		// sync/atomic import, while file "b" calls atomic.LoadInt32(v). After
		// the fix, the need for the import will shift from the use in file "b"
		// (which becomes a method call v.Load) to the declaration in file "a"
		// (which becomes atomic.Int32).
		pkgPrefix, edits := refactor.AddImport(pass.TypesInfo, file, "atomic", "sync/atomic", "", def.Node().Pos())
		// Edit the type.
		//
		// var v int32
		//       ------------
		// var v atomic.Int32
		edits = append(edits, analysis.TextEdit{
			Pos:     typ.Pos(),
			End:     typ.End(),
			NewText: fmt.Appendf(nil, "%s%s", pkgPrefix, newType),
		})
		fixFiles[file] = true

		// Each valid use is an Ident v or Selector expr.v within an atomic.F(&...) call.
		var validUses []inspector.Cursor
		for cur := range index.Uses(v) {
			if v.IsField() && cur.ParentEdgeKind() == edge.KeyValueExpr_Key {
				continue nextvar // we cannot fix initial an value assignment T{v: 1}
			}
			if cur.ParentEdgeKind() == edge.SelectorExpr_Sel {
				cur = cur.Parent() // ascend from v to expr.v
			}
			// Inv: cur is the l-value expression denoting v.
			// v must appear beneath atomic.AddInt32(&v, ...) call.
			valid := false
			if cur.ParentEdgeKind() == edge.UnaryExpr_X &&
				cur.Parent().Node().(*ast.UnaryExpr).Op == token.AND {
				if ek, idx := cur.Parent().ParentEdge(); ek == edge.CallExpr_Args && idx == 0 {
					curCall := cur.Parent().Parent()
					call := curCall.Node().(*ast.CallExpr)
					if fn, ok := typeutil.Callee(info, call).(*types.Func); ok && fn.Pkg() == atomicPkg {
						valid = true
					}
				}
			}

			if !valid {
				// More complex case: reject candidate.
				//
				// For example, cur may be an unsynchronized load (e.g. v == 0). To
				// avoid a type conversion error, we'd have to rewrite this as
				// v.Load(). However, this is an invalid rewrite: if the program is
				// mixing atomic operations with unsynchronized reads, the author
				// might have accidentally introduced a data race and the suggested
				// fix could obscure the mistake. Or, if the usage is intentional,
				// rewriting may result in a behavior change.
				continue nextvar
			}
			validUses = append(validUses, cur)
		}

		for _, cur := range validUses {
			vexpr := cur.Node()
			call := cur.Parent().Parent().Node().(*ast.CallExpr)
			fn := typeutil.Callee(info, call).(*types.Func)
			// atomic.AddInt32(&v,    ...)
			// ----------------- -----
			//                  v.Add(...)
			after := vexpr.End() // LoadInt32(&v⁁)
			if len(call.Args) > 1 {
				after = call.Args[1].Pos() // AddInt32(&v, ⁁...)
			}
			verb := strings.TrimSuffix(fn.Name(), newType) // "AddInt32" => "Add"
			edits = append(edits, []analysis.TextEdit{
				{
					Pos: call.Pos(),
					End: vexpr.Pos(),
				},
				{
					Pos:     vexpr.End(),
					End:     after,
					NewText: fmt.Appendf(nil, ".%s(", verb),
				},
			}...)
			fixFiles[astutil.EnclosingFile(cur)] = true
		}

		// Check minimum Go version: go1.19, or 1.23 for the And/Or functions.
		if !(analyzerutil.FileUsesGoVersion(pass, file, versions.Go1_19) ||
			analyzerutil.FileUsesGoVersion(pass, file, versions.Go1_23) &&
				(strings.HasPrefix(funcName, "And") || strings.HasPrefix(funcName, "Or"))) {
			continue
		}

		// Skip if v is not local and the package has ignored files as it may be
		// an incomplete transformation.
		if !isLocal(v) && len(pass.IgnoredFiles) > 0 {
			continue
		}

		pass.Report(analysis.Diagnostic{
			Pos:     names[0].Pos(),
			End:     typ.End(),
			Message: fmt.Sprintf("var %s %s may be simplified using atomic.%s", v.Name(), oldType, newType),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message:   fmt.Sprintf("Replace %s by atomic.%s", oldType, newType),
				TextEdits: edits,
			}},
		})
	}

	return nil, nil
}

// findConversions calls mark for both struct types of each explicit
// conversion or implicit assignment (in assignments, variable declarations,
// call arguments, return statements, composite literals and channel sends)
// in which a value of one struct type is converted or assigned to a
// different struct type with an identical underlying type.
func findConversions(info *types.Info, root inspector.Cursor, mark func(*types.Struct)) {
	// check reports a conversion from type from to type to.
	check := func(from, to types.Type) {
		if from == nil || to == nil || types.Identical(from, to) {
			return
		}
		fromStruct, ok1 := from.Underlying().(*types.Struct)
		toStruct, ok2 := to.Underlying().(*types.Struct)
		if ok1 && ok2 && types.Identical(fromStruct, toStruct) {
			mark(fromStruct)
			mark(toStruct)
		}
	}

	// valueTypes returns the types of the values denoted by exprs,
	// expanding a single multi-valued expression such as f().
	valueTypes := func(exprs []ast.Expr) []types.Type {
		if len(exprs) == 1 {
			if tuple, ok := info.TypeOf(exprs[0]).(*types.Tuple); ok {
				ts := make([]types.Type, tuple.Len())
				for i := range ts {
					ts[i] = tuple.At(i).Type()
				}
				return ts
			}
		}
		ts := make([]types.Type, len(exprs))
		for i, expr := range exprs {
			ts[i] = info.TypeOf(expr)
		}
		return ts
	}

	for cur := range root.Preorder(
		(*ast.CallExpr)(nil),
		(*ast.AssignStmt)(nil),
		(*ast.ValueSpec)(nil),
		(*ast.ReturnStmt)(nil),
		(*ast.CompositeLit)(nil),
		(*ast.SendStmt)(nil),
	) {
		switch n := cur.Node().(type) {
		case *ast.CallExpr:
			tv, ok := info.Types[n.Fun]
			if !ok || tv.Type == nil {
				continue
			}
			if tv.IsType() {
				// Conversion T(x), including pointer conversions (*T)(p).
				if len(n.Args) == 1 {
					from := info.TypeOf(n.Args[0])
					check(from, tv.Type)
					if from != nil {
						fromPtr, ok1 := from.Underlying().(*types.Pointer)
						toPtr, ok2 := tv.Type.Underlying().(*types.Pointer)
						if ok1 && ok2 {
							check(fromPtr.Elem(), toPtr.Elem())
						}
					}
				}
			} else if sig, ok := tv.Type.Underlying().(*types.Signature); ok {
				// Call f(x): each argument is assigned to its parameter.
				params := sig.Params()
				for i, arg := range valueTypes(n.Args) {
					switch {
					case sig.Variadic() && i >= params.Len()-1:
						last := params.At(params.Len() - 1).Type()
						if n.Ellipsis.IsValid() {
							check(arg, last)
						} else if slice, ok := last.Underlying().(*types.Slice); ok {
							check(arg, slice.Elem())
						}
					case i < params.Len():
						check(arg, params.At(i).Type())
					}
				}
			}

		case *ast.AssignStmt:
			if rhs := valueTypes(n.Rhs); len(rhs) == len(n.Lhs) {
				for i, lhs := range n.Lhs {
					check(rhs[i], info.TypeOf(lhs))
				}
			}

		case *ast.ValueSpec:
			if n.Type != nil {
				for _, value := range valueTypes(n.Values) {
					check(value, info.TypeOf(n.Type))
				}
			}

		case *ast.ReturnStmt:
			var sig *types.Signature
			for fn := range cur.Enclosing((*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)) {
				switch fn := fn.Node().(type) {
				case *ast.FuncDecl:
					if obj, ok := info.Defs[fn.Name].(*types.Func); ok {
						sig = obj.Signature()
					}
				case *ast.FuncLit:
					sig, _ = info.TypeOf(fn).(*types.Signature)
				}
				break // innermost function only
			}
			if sig != nil {
				if results := valueTypes(n.Results); len(results) == sig.Results().Len() {
					for i, result := range results {
						check(result, sig.Results().At(i).Type())
					}
				}
			}

		case *ast.CompositeLit:
			// Each element is assigned to its field, element, or map key/value type.
			typ := info.TypeOf(n)
			if typ == nil {
				continue
			}
			switch u := typesinternal.Unpointer(typ).Underlying().(type) {
			case *types.Struct:
				for i, elt := range n.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if key, ok := kv.Key.(*ast.Ident); ok {
							if field, ok := info.Uses[key].(*types.Var); ok {
								check(info.TypeOf(kv.Value), field.Type())
							}
						}
					} else if i < u.NumFields() {
						check(info.TypeOf(elt), u.Field(i).Type())
					}
				}
			case *types.Slice, *types.Array, *types.Map:
				var keyType, elemType types.Type
				switch u := u.(type) {
				case *types.Slice:
					elemType = u.Elem()
				case *types.Array:
					elemType = u.Elem()
				case *types.Map:
					keyType, elemType = u.Key(), u.Elem()
				}
				for _, elt := range n.Elts {
					if kv, ok := elt.(*ast.KeyValueExpr); ok {
						if keyType != nil {
							check(info.TypeOf(kv.Key), keyType)
						}
						check(info.TypeOf(kv.Value), elemType)
					} else {
						check(info.TypeOf(elt), elemType)
					}
				}
			}

		case *ast.SendStmt:
			if t := info.TypeOf(n.Chan); t != nil {
				if ch, ok := t.Underlying().(*types.Chan); ok {
					check(info.TypeOf(n.Value), ch.Elem())
				}
			}
		}
	}
}
