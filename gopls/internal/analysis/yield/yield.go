// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package yield

// TODO(adonovan): also check for this pattern:
//
// 	for x := range seq {
// 		yield(x)
// 	}
//
// which should be entirely rewritten as
//
// 	seq(yield)
//
// to avoid unnecessary range desugaring and chains of dynamic calls.

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"iter"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/analysis/analyzerutil"
)

//go:embed doc.go
var doc string

var Analyzer = &analysis.Analyzer{
	Name:     "yield",
	Doc:      analyzerutil.MustExtractDoc(doc, "yield"),
	Requires: []*analysis.Analyzer{inspect.Analyzer, buildssa.Analyzer},
	Run:      run,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/yield",
}

func run(pass *analysis.Pass) (any, error) {
	inspector := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// Find all calls to yield of the right type.
	yieldCalls := make(map[token.Pos]*ast.CallExpr) // keyed by CallExpr.Lparen.
	nodeFilter := []ast.Node{(*ast.CallExpr)(nil)}
	inspector.Preorder(nodeFilter, func(n ast.Node) {
		call := n.(*ast.CallExpr)
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "yield" {
			if sig, ok := pass.TypesInfo.TypeOf(id).(*types.Signature); ok &&
				sig.Params().Len() < 3 &&
				sig.Results().Len() == 1 &&
				types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool]) {
				yieldCalls[call.Lparen] = call
			}
		}
	})

	// Common case: nothing to do.
	if len(yieldCalls) == 0 {
		return nil, nil
	}

	// Study the control flow using SSA.
	buildssa := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)
	for _, fn := range buildssa.SrcFuncs {
		// TODO(adonovan): opt: skip functions that don't contain any yield calls.

		// Find the yield calls in SSA.
		type callInfo struct {
			syntax   *ast.CallExpr
			index    int // index of instruction within its block
			reported bool
		}
		ssaYieldCalls := make(map[*ssa.Call]*callInfo)
		for _, b := range fn.Blocks {
			for i, instr := range b.Instrs {
				if call, ok := instr.(*ssa.Call); ok {
					if syntax, ok := yieldCalls[call.Pos()]; ok {
						ssaYieldCalls[call] = &callInfo{syntax: syntax, index: i}
					}
				}
			}
		}
		isYieldCall := func(v ssa.Value) bool {
			call, ok := v.(*ssa.Call)
			return ok && ssaYieldCalls[call] != nil
		}

		// Now search for a control path from the instruction after a
		// yield call to another yield call--possible the same one,
		// following all block successors except "if yield() { ... }";
		// in such cases we know that yield returned true.
		//
		// Note that this is a "may" dataflow analysis: it
		// reports when a yield function _may_ be called again
		// without a positive intervening check, but it is
		// possible that the check is beyond the ability of
		// the representation to detect, perhaps involving
		// sophisticated use of booleans, indirect state (not
		// in SSA registers), or multiple flow paths some of
		// which are infeasible.
		//
		// A "must" analysis (which would report when a second
		// yield call can only be reached after failing the
		// boolean check) would be too conservative.
		// In particular, the most common mistake is to
		// forget to check the boolean at all.
		for call, info := range ssaYieldCalls {
			visited := make([]bool, len(fn.Blocks)) // visited BasicBlock.Indexes

			// visit visits the instructions of a block (or a suffix if start > 0).
			var visit func(b *ssa.BasicBlock, start int)
			visit = func(b *ssa.BasicBlock, start int) {
				if visited[b.Index] {
					return
				}
				if start == 0 {
					visited[b.Index] = true
				}
				for _, instr := range b.Instrs[start:] {
					switch instr := instr.(type) {
					case *ssa.Call:

						// Precondition: v has a pos within a CallExpr.
						enclosingCall := func(v ssa.Value) ast.Node {
							pos := v.Pos()
							cur, ok := inspector.Root().FindByPos(pos, pos)
							if !ok {
								panic(fmt.Sprintf("can't find node at %v", safetoken.StartPosition(pass.Fset, pos)))
							}
							call, _ := cursorutil.FirstEnclosing[*ast.CallExpr](cur)
							if call == nil {
								panic(fmt.Sprintf("no call enclosing %v", safetoken.StartPosition(pass.Fset, pos)))
							}
							return call
						}

						if !info.reported && ssaYieldCalls[instr] != nil {
							info.reported = true
							var (
								where   = "" // "" => same yield call (a loop)
								related []analysis.RelatedInformation
							)
							// Also report location of reached yield call, if distinct.
							if instr != call {
								otherLine := safetoken.StartPosition(pass.Fset, instr.Pos()).Line
								where = fmt.Sprintf("(on L%d) ", otherLine)
								otherCallExpr := enclosingCall(instr)
								related = []analysis.RelatedInformation{{
									Pos:     otherCallExpr.Pos(),
									End:     otherCallExpr.End(),
									Message: "other call here",
								}}
							}
							callExpr := enclosingCall(call)
							pass.Report(analysis.Diagnostic{
								Pos:     callExpr.Pos(),
								End:     callExpr.End(),
								Message: fmt.Sprintf("yield may be called again %safter returning false", where),
								Related: related,
							})
						}
					case *ssa.If:
						// Visit both successors, unless cond is yield() or its negation.
						// In that case visit only the "if !yield()" block.
						t, f := reachableSuccs(instr.Cond, isYieldCall)
						if t {
							visit(b.Succs[0], 0)
						}
						if f {
							visit(b.Succs[1], 0)
						}

					case *ssa.Jump:
						visit(b.Succs[0], 0)
					}
				}
			}

			// Start at the instruction after the yield call.
			visit(call.Block(), info.index+1)
		}
	}

	return nil, nil
}

// reachableSuccs reports whether the (true, false) outcomes of the
// condition are possible.
func reachableSuccs(cond ssa.Value, isYieldCall func(ssa.Value) bool) (_t, _f bool) {
	// If the condition is...
	//
	// ...a constant, we know only one successor is reachable.
	//
	// ...a yield call, we assume that it returned false,
	// and treat it like a constant.
	//
	// ...a negation !v, we strip the negation and flip the sense
	// of the result.
	//
	// ...a phi node, we recursively find all non-phi leaves
	// of the phi graph and treat them like a conjunction,
	// e.g. if false || true || yield || yield { ... }.
	//
	// (We don't actually analyze || and && in this way,
	// but we could do them too.)

	// This logic addresses cases where conditions are
	// materialized as booleans such as this
	//
	//   ok := yield()
	//   ok = ok && yield()
	//   ok = ok && yield()
	//
	// which in SSA becomes:
	//
	//   yield()
	//   phi(false, yield())
	//   phi(false, yield())
	//
	// and we can reduce each phi(false, x) to just x.
	//
	// Similarly this case:
	//
	//	var ok bool
	//	if foo { ok = yield() }
	//	else   { ok = yield() }
	//	if ok { ... }
	//
	// can be analyzed as "if yield || yield".

	// all[false] => all cases are false
	// all[true]  => all cases are true
	all := [2]bool{true, true}
	for v := range unphi(cond) {
		sense := 1 // 0=false 1=true

		// Strip off any NOT operators.
		for {
			unop, ok := v.(*ssa.UnOp)
			if !(ok && unop.Op == token.NOT) {
				break
			}
			v = unop.X
			sense = 1 - sense
		}

		switch {
		case is[*ssa.Const](v):
			// "if false" means not all cases are true,
			// and vice versa.
			if constant.BoolVal(v.(*ssa.Const).Value) {
				sense = 1 - sense
			}
			all[sense] = false

		case isYieldCall(v):
			// "if yield" is assumed to be false.
			all[sense] = false // ¬ all cases are true

		default:
			// Unknown condition:
			// ¬ all cases are false
			// ¬ all cases are true
			return true, true
		}
	}
	if all[0] && all[1] {
		panic("unphi returned empty sequence")
	}
	return !all[0], !all[1]
}

func is[T any](x any) bool {
	_, ok := x.(T)
	return ok
}

// -- SSA helpers --

// unphi returns the sequence of values formed by recursively
// replacing phi nodes in v by their non-phi operands.
func unphi(v ssa.Value) iter.Seq[ssa.Value] {
	return func(yield func(ssa.Value) bool) {
		_ = every(v, yield)
	}
}

// every reports whether predicate f is true of each value in the
// sequence formed by recursively replacing phi nodes in v by their
// operands.
func every(v ssa.Value, f func(ssa.Value) bool) bool {
	var seen map[*ssa.Phi]bool
	var visit func(v ssa.Value) bool
	visit = func(v ssa.Value) bool {
		if phi, ok := v.(*ssa.Phi); ok {
			if !seen[phi] {
				if seen == nil {
					seen = make(map[*ssa.Phi]bool)
				}
				seen[phi] = true
				for _, edge := range phi.Edges {
					if !visit(edge) {
						return false
					}
				}
			}
			return true
		}
		return f(v)
	}
	return visit(v)
}
