// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package yield

// TODO(adonovan): also check for inefficient code using this pattern:
//
// 	for x := range seq {
// 		if !yield(x) {
//			break
//		}
// 	}
//
// which should be entirely rewritten as
//
// 	seq(yield)
//
// to avoid unnecessary range desugaring and chains of dynamic calls.

import (
	"cmp"
	_ "embed"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"iter"
	"math/bits"
	"slices"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/gopls/internal/util/moremaps"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/analysis/analyzerutil"
	"golang.org/x/tools/internal/flow"
	"golang.org/x/tools/internal/typesinternal"
)

// This analyzer uses a classical dataflow analysis to track the set
// of program points that may be reached after a specific yield() call
// must have returned false. It uses the [flow] framework to compute a
// fixed point over the SSA control-flow graph. The lattice value,
// [stateSet], represents a set of facts about the conditions under
// which the current program point _may_ be reached after yield
// returns false. The conditions are the known truth or falsehood of
// selected local Boolean SSA values, specifically constants, yield
// calls, negations, and phis. Values are merged when their conditions
// are equal, or when a stronger condition makes a weaker one
// redundant.
//
// (An earlier implementation used only sparse dataflow analysis but
// had a number of false positives due to loss of precision when
// control flow joins were materialized as boolean values.)
//
// Note that this is a "may" dataflow analysis: it reports when a
// yield function _may_ be called again without a positive intervening
// check, but it is possible that the check is beyond the ability of
// the representation to detect, perhaps involving sophisticated use
// of booleans, indirect state (not in SSA registers), or multiple
// flow paths some of which are infeasible.
//
// A "must" analysis (which would report when a second yield call can
// only be reached after failing the boolean check) would be too
// conservative. In particular, the most common mistake is to forget
// to check the boolean at all.
//
// The analysis ignores 'go' and 'defer' statements.

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
	// It is not strictly necessary that an iterator reference
	// iter.Seq{,2}, but it is overwhelmingly the usual case.
	// Skip any package that does not.
	if !typesinternal.Imports(pass.Pkg, "iter") {
		return nil, nil
	}

	// Find position of each syntactic yield call.
	// We assume each yield function is named "yield".
	var (
		yieldCalls = make(map[token.Pos]*ast.CallExpr) // keyed by CallExpr.Lparen.
		inspector  = pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	)
	for curCall := range inspector.Root().Preorder((*ast.CallExpr)(nil)) {
		call := curCall.Node().(*ast.CallExpr)
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "yield" {
			if sig, ok := pass.TypesInfo.TypeOf(id).(*types.Signature); ok &&
				sig.Params().Len() < 3 &&
				sig.Results().Len() == 1 &&
				types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool]) {
				yieldCalls[call.Lparen] = call
			}
		}
	}

	// Common case: nothing to do.
	if len(yieldCalls) == 0 {
		return nil, nil
	}

	callSyntax := func(call *ssa.Call) *ast.CallExpr {
		return yieldCalls[call.Pos()]
	}

	// Study the control flow using SSA.
	buildssa := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)
	for _, fn := range buildssa.SrcFuncs {
		run1(pass, callSyntax, fn)
	}

	return nil, nil
}

func run1(pass *analysis.Pass, callSyntax func(call *ssa.Call) *ast.CallExpr, fn *ssa.Function) {
	// Find SSA instruction of each yield call.
	ssaYieldCalls := make(map[*ssa.Call]bool)
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if call, ok := instr.(*ssa.Call); ok && callSyntax(call) != nil {
				ssaYieldCalls[call] = true
			}
		}
	}
	if len(ssaYieldCalls) == 0 {
		return
	}
	isYieldCall := func(v ssa.Value) bool {
		call, ok := v.(*ssa.Call)
		return ok && ssaYieldCalls[call]
	}

	numb := make(numbering)

	// Compute the dataflow solution.
	initial := map[int]stateSet{0: nil} // on entry, no states of interest
	result := flow.Forward[lattice](fnGraph{fn}, initial, func(fromID, toID int, in stateSet) stateSet {
		// The transfer function computes the effect on the abstract
		// state of flow along the CFG edge from --> to,
		// including the effects from the 'from' block itself.
		// and the prefix of phis in the 'to' block.
		//
		// In effect, the block's state in the framework
		// corresponds to the point after its prefix of phis.
		var (
			from = fn.Blocks[fromID]
			to   = fn.Blocks[toID]
			out  = in // (do not mutate in)
		)

		for _, instr := range from.Instrs {
			switch instr := instr.(type) {
			case *ssa.Call:
				if isYieldCall(instr) {
					out = out.yieldCall(numb, instr)
				}

			case *ssa.UnOp:
				if instr.Op == token.NOT {
					out = out.not(numb, instr)
				}

			case *ssa.If:
				out = out.if_(numb, instr.Cond, to == from.Succs[0])
			}
		}

		// Process phis in 'to' block.
		if i := slices.Index(to.Preds, from); i >= 0 {
			for _, instr := range to.Instrs {
				if phi, ok := instr.(*ssa.Phi); ok {
					out = out.phi(numb, phi, phi.Edges[i])
				}
			}
		}

		// Opt: avoid renormalizing 'in' if unchanged.
		if len(out) > 0 && !sameSlice(out, in) {
			out = normalize(out)
		}

		return out
	})

	// Gather the problematic calls.
	type problem struct{ first, later *ssa.Call }
	var problems []problem
	for i, b := range fn.Blocks {
		in := result.In(i)
		out := slices.Clone(in)
		for _, instr := range b.Instrs {
			if call, ok := instr.(*ssa.Call); ok && isYieldCall(call) {
				for _, s := range out {
					problems = append(problems, problem{first: s.yield, later: call})
				}
				// Apply intra-block transfer function,
				// in case there are yield calls in sequence.
				out = out.yieldCall(numb, call)
			}
		}
	}

	// Sort, since source order differs from block order,
	// and for each 'first', we want the earliest 'later' in the source.
	slices.SortFunc(problems, func(x, y problem) int {
		if d := cmp.Compare(x.first.Pos(), y.first.Pos()); d != 0 {
			return d
		}
		return cmp.Compare(x.later.Pos(), y.later.Pos())
	})

	// Report a diagnostic for each problematic 'first' call.
	for _, p := range problems {
		if !moremaps.Delete(ssaYieldCalls, p.first) {
			continue // already reported
		}

		var where string
		var related []analysis.RelatedInformation
		if p.later != p.first {
			otherLine := safetoken.StartPosition(pass.Fset, p.later.Pos()).Line
			where = fmt.Sprintf("(on L%d) ", otherLine)
			laterCall := callSyntax(p.later)
			related = []analysis.RelatedInformation{{
				Pos:     laterCall.Pos(),
				End:     laterCall.End(),
				Message: "other call here",
			}}
		}

		firstCall := callSyntax(p.first)
		pass.Report(analysis.Diagnostic{
			Pos:     firstCall.Pos(),
			End:     firstCall.End(),
			Message: fmt.Sprintf("yield may be called again %safter returning false", where),
			Related: related,
		})
	}
}

// A numbering maps SSA values to small nonnegative integers.
type numbering map[ssa.Value]int

// number returns the sequence of number for value v.
func (n numbering) number(v ssa.Value) int {
	i, ok := n[v]
	if !ok {
		i = len(n)
		n[v] = i
	}
	return i
}

// -- lattice --

type lattice struct{}

var _ flow.Semilattice[stateSet] = lattice{}

// Ident returns the identity element of the lattice, an empty stateSet.
func (lattice) Ident() stateSet { return nil }

// Equals reports whether two stateSets are equivalent.
func (lattice) Equals(a, b stateSet) bool {
	return slices.EqualFunc(a, b, state.equal)
}

// Merge combines two stateSets into a minimal unified set, dropping subsets.
// The result is normalized even if the arguments are not.
func (lattice) Merge(a, b stateSet) stateSet {
	return normalize(slices.Concat(a, b))
}

// normalize puts the stateSet in normal form, destructively.
func normalize(ss stateSet) stateSet {
	// We define a total order of states in the set so
	// that set equality is slice equality.
	// States are ordered by four keys in order:
	// - yield call (since this is cheaper than mask.len);
	// - mask.len (number of conditions), smallest first,
	//   so that the later merging step can eliminate
	//   narrow conditions for a given yield call in
	//   favor of broader ones;
	// - mask bit pattern
	// - senses bit pattern
	// The latter two are essentially arbitrary ways
	// to ensure a total order.
	slices.SortFunc(ss, func(x, y state) int {
		if x.yield != y.yield {
			return cmp.Compare(x.yield.Pos(), y.yield.Pos()) // must be non-zero
		}
		if d := cmp.Compare(x.mask.len(), y.mask.len()); d != 0 {
			return d
		}
		if d := x.mask.cmp(&y.mask); d != 0 {
			return d
		}
		// We rely on senses having no stray (unmasked) bits.
		if d := x.senses.cmp(&y.senses); d != 0 {
			return d
		}
		return 0
	})

	// Discard empty states, or states that are stricter
	// than (and thus redundant wrt) ones we already have.
	//
	// This is quadratic in the number of analytically distinct
	// control states, which is related to the number of yield
	// calls, the number of control paths that differ in their
	// treatement of yield results, and the number of phis.
	// But n is typically tiny.
	out := ss[:0]
	for _, s := range ss {
		if !s.mask.empty() && !slices.ContainsFunc(out, s.stricter) {
			out = append(out, s)
		}
	}
	return out
}

// -- stateSet transfer (pure) functions --

// stateSet is the dataflow fact associated with each block edge.
//
// Conceptually it is a map from a specific yield call to an
// "antichain", a set of partially ordered states such that none
// subsumes another (similar to DNF). Though specialized
// representations exist (e.g. BDDs), a slice is fine in practice.
//
// The states in the set are totally ordered (somewhat arbitrarily)
// by [normalize] so that state set equality is slice equality.
type stateSet []state

// yieldCall defines the transfer function for a yield call.
func (in stateSet) yieldCall(n numbering, call *ssa.Call) (out stateSet) {
	callnum := n.number(call)

	s := state{yield: call}
	s.mask.set(callnum, true)
	s.senses.set(callnum, false) // analysis presumes each yield call returns false
	return append(slices.Clip(in), s)
}

// not defines the transfer function for a negation.
func (in stateSet) not(n numbering, not *ssa.UnOp) (out stateSet) {
	xnum := n.number(not.X)
	notnum := n.number(not)
	for _, s := range in {
		if s.mask.get(xnum) {
			s = s.update(notnum, true, !s.senses.get(xnum))
		} else {
			s = s.update(notnum, false, false) // clear stale fact
		}
		out = append(out, s)
	}
	return out
}

// phi defines the transfer function for a phi node for the incoming edge value val.
func (in stateSet) phi(n numbering, phi *ssa.Phi, val ssa.Value) (out stateSet) {
	phinum := n.number(phi)
	if c, ok := val.(*ssa.Const); ok && c.Value != nil && c.Value.Kind() == constant.Bool {
		sense := constant.BoolVal(c.Value)
		// phi's value is a constant (sense).
		for _, s := range in {
			s = s.update(phinum, true, sense)
			out = append(out, s)
		}
	} else {
		// phi's value comes from predecessor, val.
		valnum := n.number(val)
		for _, s := range in {
			if s.mask.get(valnum) {
				s = s.update(phinum, true, s.senses.get(valnum))
			} else {
				s = s.update(phinum, false, false) // clear stale fact
			}
			out = append(out, s)
		}
	}
	return out
}

// if_ defines the transfer function for a conditional branch.
func (in stateSet) if_(n numbering, cond ssa.Value, sense bool) (out stateSet) {
	// Strip off any negations to get to the root fact.
	for {
		unop, ok := cond.(*ssa.UnOp)
		if !(ok && unop.Op == token.NOT) {
			break
		}
		sense = !sense
		cond = unop.X
	}

	condnum := n.number(cond)
	for _, s := range in {
		if s.mask.get(condnum) && s.senses.get(condnum) != sense {
			// Infeasible edge; discard this state.
			continue
		}
		out = append(out, s)
	}
	return out
}

// -- state --

// state represents an execution path defined by a set of boolean conditions.
// It tracks the original yield call that could be violated if this state's conditions are met.
// Conceptually, the conditions are a mapping from ssa.Value to boolean sense.
// Concretely, SSA values are sequentially numbered (see [numbering]) as they are encountered,
// and these numbers identify values.
//
// mask is the set of map keys; senses is the boolean sense of each value.
// It is an invariant that senses is a subset of mask.
type state struct {
	yield  *ssa.Call // yield call that would be violated if the conditions are not met
	mask   bitset    // tracks which values have conditions
	senses bitset    // tracks the boolean condition (sense) of each value
}

// equal reports whether x and y are the same state.
func (x state) equal(y state) bool {
	return x.yield == y.yield &&
		x.mask.equal(&y.mask) &&
		x.senses.equalMasked(&y.senses, &x.mask)
}

// stricter reports whether x is a stricter (more specific) state than y.
func (x state) stricter(y state) bool {
	return x.yield == y.yield &&
		y.mask.subsetOf(&x.mask) &&
		y.senses.equalMasked(&x.senses, &y.mask)
}

// update returns a copy of the state with the specified value's condition updated.
func (s state) update(num int, mask, sense bool) state {
	s.mask = s.mask.clone()
	s.senses = s.senses.clone()
	s.mask.set(num, mask)
	s.senses.set(num, sense)
	return s
}

// -- SSA CFG as graph.Graph --

// fnGraph adapts an [ssa.Function] to the [graph.Graph] interface
// required by the flow analysis framework.
// Nodes are labelled by their block indices and connected by the
// successor relation.
//
// TODO(adonovan): move to ssa package?
type fnGraph struct {
	fn *ssa.Function
}

// Nodes returns an iterator over the basic block indices.
func (g fnGraph) Nodes() iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range g.fn.Blocks {
			if !yield(i) {
				return
			}
		}
	}
}

// NumNodes returns the number of basic blocks in the graph.
func (g fnGraph) NumNodes() int { return len(g.fn.Blocks) }

// Out returns an iterator over the successor block indices of a given node.
func (g fnGraph) Out(node int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for _, succ := range g.fn.Blocks[node].Succs {
			if !yield(succ.Index) {
				return
			}
		}
	}
}

// -- bitset --

// bitset is a set of non-negative integers.
// It uses space proportional to its largest element.
//
// The zero value is a ready-to-use empty set.
// Bitsets, like slices, have hybrid value/reference semantics.
// Do not mutate copies; use [bitset.clone] before [bitset.set].
//
// bitsets are comparable (see [bitset.equal]) and totally ordered (see [bitset.cmp]).
type bitset struct {
	limbs []uint64 // bit vector; last limb is nonzero
}

// empty reports whether the set is empty.
func (b *bitset) empty() bool {
	return len(b.limbs) == 0
}

// set inserts or removes i from the set.
func (b *bitset) set(i int, sense bool) {
	// Grow if needed.
	idx := int(i / 64)
	if idx >= len(b.limbs) {
		if !sense {
			return // clearing nonexistent bit
		}
		b.limbs = slices.Grow(b.limbs, idx-len(b.limbs)+1)[:idx+1]
	}

	bit := uint64(1) << (i % 64)
	if sense {
		// set
		b.limbs[idx] |= bit
	} else {
		// clear
		b.limbs[idx] &^= bit

		// Remove any trailing zero limbs.
		if b.limbs[idx] == 0 {
			n := len(b.limbs)
			for n > 0 && b.limbs[n-1] == 0 {
				n--
			}
			b.limbs = b.limbs[:n]
		}
	}
}

func (b *bitset) limb(i int) uint64 {
	if i < len(b.limbs) {
		return b.limbs[i]
	}
	return 0
}

// get reports whether the set contains i.
func (b *bitset) get(i int) bool {
	return b.limb(i/64)&(1<<(i%64)) != 0
}

// clone returns a copy of the bitset.
func (b *bitset) clone() bitset {
	return bitset{limbs: slices.Clone(b.limbs)}
}

// equal reports whether two sets contain the same elements.
func (b *bitset) equal(other *bitset) bool {
	return slices.Equal(b.limbs, other.limbs)
}

// equalMasked reports whether b&mask equals other&mask.
func (b *bitset) equalMasked(other, mask *bitset) bool {
	// Above n words, both operands when masked are effectively zero.
	n := min(len(mask.limbs), max(len(b.limbs), len(other.limbs)))
	for i, m := range mask.limbs[:n] {
		if b.limb(i)&m != other.limb(i)&m {
			return false
		}
	}
	return true
}

// cmp returns the signum of the comparison b against other.
func (b *bitset) cmp(other *bitset) int {
	if d := cmp.Compare(len(b.limbs), len(other.limbs)); d != 0 {
		return d
	}
	for i := len(b.limbs) - 1; i >= 0; i-- {
		if d := cmp.Compare(b.limbs[i], other.limbs[i]); d != 0 {
			return d
		}
	}
	return 0
}

// subsetOf reports whether other contains all of b's elements.
func (b *bitset) subsetOf(other *bitset) bool {
	for i, w1 := range b.limbs {
		if w1&other.limb(i) != w1 {
			return false
		}
	}
	return true
}

// len returns the number of elements of the set.
func (b *bitset) len() int {
	var n int
	for _, v := range b.limbs {
		n += bits.OnesCount64(v)
	}
	return n
}

// sameSlice reports whether the corresponding elements
// of two slices are identical variables.
func sameSlice[T any](x, y []T) bool {
	return len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
}
