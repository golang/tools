// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ssa

// An optional pass for sanity-checking invariants of the SSA representation.
// Currently it checks CFG invariants but little at the instruction level.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/types"
	"io"
	"os"
	"strings"
)

type sanity struct {
	reporter io.Writer
	fn       *Function
	block    *BasicBlock
	instrs   map[Instruction]unit
	insane   bool
}

// sanityCheck performs integrity checking of the SSA representation
// of the function fn and returns true if it was valid.  Diagnostics
// are written to reporter if non-nil, os.Stderr otherwise.  Some
// diagnostics are only warnings and do not imply a negative result.
//
// Sanity-checking is intended to facilitate the debugging of code
// transformation passes.
func sanityCheck(fn *Function, reporter io.Writer) bool {
	if reporter == nil {
		reporter = os.Stderr
	}
	return (&sanity{reporter: reporter}).checkFunction(fn)
}

// mustSanityCheck is like sanityCheck but panics instead of returning
// a negative result.
func mustSanityCheck(fn *Function, reporter io.Writer) {
	if !sanityCheck(fn, reporter) {
		fn.WriteTo(os.Stderr)
		panic("SanityCheck failed")
	}
}

func (s *sanity) diagnostic(prefix, format string, args ...interface{}) {
	fmt.Fprintf(s.reporter, "%s: function %s", prefix, s.fn)
	if s.block != nil {
		fmt.Fprintf(s.reporter, ", block %s", s.block)
	}
	io.WriteString(s.reporter, ": ")
	fmt.Fprintf(s.reporter, format, args...)
	io.WriteString(s.reporter, "\n")
}

func (s *sanity) errorf(format string, args ...interface{}) {
	s.insane = true
	s.diagnostic("Error", format, args...)
}

func (s *sanity) warnf(format string, args ...interface{}) {
	s.diagnostic("Warning", format, args...)
}

// findDuplicate returns an arbitrary basic block that appeared more
// than once in blocks, or nil if all were unique.
func findDuplicate(blocks []*BasicBlock) *BasicBlock {
	if len(blocks) < 2 {
		return nil
	}
	if blocks[0] == blocks[1] {
		return blocks[0]
	}
	// Slow path:
	m := make(map[*BasicBlock]bool)
	for _, b := range blocks {
		if m[b] {
			return b
		}
		m[b] = true
	}
	return nil
}

func (s *sanity) checkInstr(idx int, instr Instruction) {
	switch instr := instr.(type) {
	case *If, *Jump, *Return, *Panic:
		s.errorf("control flow instruction not at end of block")
	case *Phi:
		if idx == 0 {
			// It suffices to apply this check to just the first phi node.
			if dup := findDuplicate(s.block.Preds); dup != nil {
				s.errorf("phi node in block with duplicate predecessor %s", dup)
			}
		} else {
			prev := s.block.Instrs[idx-1]
			if _, ok := prev.(*Phi); !ok {
				s.errorf("Phi instruction follows a non-Phi: %T", prev)
			}
		}
		if ne, np := len(instr.Edges), len(s.block.Preds); ne != np {
			s.errorf("phi node has %d edges but %d predecessors", ne, np)

		} else {
			for i, e := range instr.Edges {
				if e == nil {
					s.errorf("phi node '%s' has no value for edge #%d from %s", instr.Comment, i, s.block.Preds[i])
				} else if !types.Identical(instr.typ, e.Type()) {
					s.errorf("phi node '%s' has a different type (%s) for edge #%d from %s (%s)",
						instr.Comment, instr.Type(), i, s.block.Preds[i], e.Type())
				}
			}
		}

	case *Alloc:
		if !instr.Heap {
			found := false
			for _, l := range s.fn.Locals {
				if l == instr {
					found = true
					break
				}
			}
			if !found {
				s.errorf("local alloc %s = %s does not appear in Function.Locals", instr.Name(), instr)
			}
		}

	case *BinOp:
	case *Call:
		if common := instr.Call; common.IsInvoke() {
			if !types.IsInterface(common.Value.Type()) {
				s.errorf("invoke on %s (%s) which is not an interface type (or type param)", common.Value, common.Value.Type())
			}
		}
	case *ChangeInterface:
	case *ChangeType:
	case *SliceToArrayPointer:
	case *Convert:
		if from := instr.X.Type(); !isBasicConvTypes(typeSetOf(from)) {
			if to := instr.Type(); !isBasicConvTypes(typeSetOf(to)) {
				s.errorf("convert %s -> %s: at least one type must be basic (or all basic, []byte, or []rune)", from, to)
			}
		}
	case *MultiConvert:
	case *Defer:
	case *Extract:
	case *Field:
	case *FieldAddr:
	case *Go:
	case *Index:
	case *IndexAddr:
	case *Lookup:
	case *MakeChan:
	case *MakeClosure:
		numFree := len(instr.Fn.(*Function).FreeVars)
		numBind := len(instr.Bindings)
		if numFree != numBind {
			s.errorf("MakeClosure has %d Bindings for function %s with %d free vars",
				numBind, instr.Fn, numFree)

		}
		if recv := instr.Type().(*types.Signature).Recv(); recv != nil {
			s.errorf("MakeClosure's type includes receiver %s", recv.Type())
		}

	case *MakeInterface:
	case *MakeMap:
	case *MakeSlice:
	case *MapUpdate:
	case *Next:
	case *Range:
	case *RunDefers:
	case *Select:
	case *Send:
	case *Slice:
	case *Store:
	case *TypeAssert:
	case *UnOp:
	case *DebugRef:
		// TODO(adonovan): implement checks.
	default:
		panic(fmt.Sprintf("Unknown instruction type: %T", instr))
	}

	if call, ok := instr.(CallInstruction); ok {
		if call.Common().Signature() == nil {
			s.errorf("nil signature: %s", call)
		}
	}

	// Check that value-defining instructions have valid types
	// and a valid referrer list.
	if v, ok := instr.(Value); ok {
		t := v.Type()
		if t == nil {
			s.errorf("no type: %s = %s", v.Name(), v)
		} else if t == tRangeIter || t == tDeferStack {
			// not a proper type; ignore.
		} else if b, ok := t.Underlying().(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
			s.errorf("instruction has 'untyped' result: %s = %s : %s", v.Name(), v, t)
		}
		s.checkReferrerList(v)
	}

	// Untyped constants are legal as instruction Operands(),
	// for example:
	//   _ = "foo"[0]
	// or:
	//   if wordsize==64 {...}

	// All other non-Instruction Values can be found via their
	// enclosing Function or Package.
}

func (s *sanity) checkFinalInstr(instr Instruction) {
	switch instr := instr.(type) {
	case *If:
		if nsuccs := len(s.block.Succs); nsuccs != 2 {
			s.errorf("If-terminated block has %d successors; expected 2", nsuccs)
			return
		}
		if s.block.Succs[0] == s.block.Succs[1] {
			s.errorf("If-instruction has same True, False target blocks: %s", s.block.Succs[0])
			return
		}

	case *Jump:
		if nsuccs := len(s.block.Succs); nsuccs != 1 {
			s.errorf("Jump-terminated block has %d successors; expected 1", nsuccs)
			return
		}

	case *Return:
		if nsuccs := len(s.block.Succs); nsuccs != 0 {
			s.errorf("Return-terminated block has %d successors; expected none", nsuccs)
			return
		}
		if na, nf := len(instr.Results), s.fn.Signature.Results().Len(); nf != na {
			s.errorf("%d-ary return in %d-ary function", na, nf)
		}

	case *Panic:
		if nsuccs := len(s.block.Succs); nsuccs != 0 {
			s.errorf("Panic-terminated block has %d successors; expected none", nsuccs)
			return
		}

	default:
		s.errorf("non-control flow instruction at end of block")
	}
}

func (s *sanity) checkBlock(b *BasicBlock, index int) {
	s.block = b

	if b.Index != index {
		s.errorf("block has incorrect Index %d", b.Index)
	}
	if b.parent != s.fn {
		s.errorf("block has incorrect parent %s", b.parent)
	}

	// Check all blocks are reachable.
	// (The entry block is always implicitly reachable,
	// as is the Recover block, if any.)
	if (index > 0 && b != b.parent.Recover) && len(b.Preds) == 0 {
		s.warnf("unreachable block")
		if b.Instrs == nil {
			// Since this block is about to be pruned,
			// tolerating transient problems in it
			// simplifies other optimizations.
			return
		}
	}

	// Check predecessor and successor relations are dual,
	// and that all blocks in CFG belong to same function.
	for _, a := range b.Preds {
		found := false
		for _, bb := range a.Succs {
			if bb == b {
				found = true
				break
			}
		}
		if !found {
			s.errorf("expected successor edge in predecessor %s; found only: %s", a, a.Succs)
		}
		if a.parent != s.fn {
			s.errorf("predecessor %s belongs to different function %s", a, a.parent)
		}
	}
	for _, c := range b.Succs {
		found := false
		for _, bb := range c.Preds {
			if bb == b {
				found = true
				break
			}
		}
		if !found {
			s.errorf("expected predecessor edge in successor %s; found only: %s", c, c.Preds)
		}
		if c.parent != s.fn {
			s.errorf("successor %s belongs to different function %s", c, c.parent)
		}
	}

	// Check each instruction is sane.
	n := len(b.Instrs)
	if n == 0 {
		s.errorf("basic block contains no instructions")
	}
	var rands [10]*Value // reuse storage
	for j, instr := range b.Instrs {
		if instr == nil {
			s.errorf("nil instruction at index %d", j)
			continue
		}
		if b2 := instr.Block(); b2 == nil {
			s.errorf("nil Block() for instruction at index %d", j)
			continue
		} else if b2 != b {
			s.errorf("wrong Block() (%s) for instruction at index %d ", b2, j)
			continue
		}
		if j < n-1 {
			s.checkInstr(j, instr)
		} else {
			s.checkFinalInstr(instr)
		}

		// Check Instruction.Operands.
	operands:
		for i, op := range instr.Operands(rands[:0]) {
			if op == nil {
				s.errorf("nil operand pointer %d of %s", i, instr)
				continue
			}
			val := *op
			if val == nil {
				continue // a nil operand is ok
			}

			// Check that "untyped" types only appear on constant operands.
			if _, ok := (*op).(*Const); !ok {
				if basic, ok := (*op).Type().Underlying().(*types.Basic); ok {
					if basic.Info()&types.IsUntyped != 0 {
						s.errorf("operand #%d of %s is untyped: %s", i, instr, basic)
					}
				}
			}

			// Check that Operands that are also Instructions belong to same function.
			// TODO(adonovan): also check their block dominates block b.
			if val, ok := val.(Instruction); ok {
				if val.Block() == nil {
					s.errorf("operand %d of %s is an instruction (%s) that belongs to no block", i, instr, val)
				} else if val.Parent() != s.fn {
					s.errorf("operand %d of %s is an instruction (%s) from function %s", i, instr, val, val.Parent())
				}
			}

			// Check that each function-local operand of
			// instr refers back to instr.  (NB: quadratic)
			switch val := val.(type) {
			case *Const, *Global, *Builtin:
				continue // not local
			case *Function:
				if val.parent == nil {
					continue // only anon functions are local
				}
			}

			// TODO(adonovan): check val.Parent() != nil <=> val.Referrers() is defined.

			if refs := val.Referrers(); refs != nil {
				for _, ref := range *refs {
					if ref == instr {
						continue operands
					}
				}
				s.errorf("operand %d of %s (%s) does not refer to us", i, instr, val)
			} else {
				s.errorf("operand %d of %s (%s) has no referrers", i, instr, val)
			}
		}
	}
}

func (s *sanity) checkReferrerList(v Value) {
	refs := v.Referrers()
	if refs == nil {
		s.errorf("%s has missing referrer list", v.Name())
		return
	}
	for i, ref := range *refs {
		if _, ok := s.instrs[ref]; !ok {
			s.errorf("%s.Referrers()[%d] = %s is not an instruction belonging to this function", v.Name(), i, ref)
		}
	}
}

func (s *sanity) checkFunctionParams() {
	signature := s.fn.Signature
	params := s.fn.Params

	// startSigParams is the start of signature.Params() within params.
	startSigParams := 0
	if signature.Recv() != nil {
		startSigParams = 1
	}

	if startSigParams+signature.Params().Len() != len(params) {
		s.errorf("function has %d parameters in signature but has %d after building",
			startSigParams+signature.Params().Len(), len(params))
		return
	}

	for i, param := range params {
		var sigType types.Type
		si := i - startSigParams
		if si < 0 {
			sigType = signature.Recv().Type()
		} else {
			sigType = signature.Params().At(si).Type()
		}

		if !types.Identical(sigType, param.Type()) {
			s.errorf("expect type %s in signature but got type %s in param %d", param.Type(), sigType, i)
		}
	}
}

// checkTransientFields checks whether all transient fields of Function are cleared.
func (s *sanity) checkTransientFields() {
	fn := s.fn
	if fn.build != nil {
		s.errorf("function transient field 'build' is not nil")
	}
	if fn.currentBlock != nil {
		s.errorf("function transient field 'currentBlock' is not nil")
	}
	if fn.vars != nil {
		s.errorf("function transient field 'vars' is not nil")
	}
	if fn.results != nil {
		s.errorf("function transient field 'results' is not nil")
	}
	if fn.returnVars != nil {
		s.errorf("function transient field 'returnVars' is not nil")
	}
	if fn.targets != nil {
		s.errorf("function transient field 'targets' is not nil")
	}
	if fn.lblocks != nil {
		s.errorf("function transient field 'lblocks' is not nil")
	}
	if fn.subst != nil {
		s.errorf("function transient field 'subst' is not nil")
	}
	if fn.jump != nil {
		s.errorf("function transient field 'jump' is not nil")
	}
	if fn.deferstack != nil {
		s.errorf("function transient field 'deferstack' is not nil")
	}
	if fn.source != nil {
		s.errorf("function transient field 'source' is not nil")
	}
	if fn.exits != nil {
		s.errorf("function transient field 'exits' is not nil")
	}
	if fn.uniq != 0 {
		s.errorf("function transient field 'uniq' is not zero")
	}
}

func (s *sanity) checkFunction(fn *Function) bool {
	s.fn = fn
	s.checkFunctionParams()
	s.checkTransientFields()

	// TODO(taking): Sanity check origin, typeparams, and typeargs.
	if fn.Prog == nil {
		s.errorf("nil Prog")
	}

	var buf bytes.Buffer
	_ = fn.String()               // must not crash
	_ = fn.RelString(fn.relPkg()) // must not crash
	WriteFunction(&buf, fn)       // must not crash

	// All functions have a package, except delegates (which are
	// shared across packages, or duplicated as weak symbols in a
	// separate-compilation model), and error.Error.
	if fn.Pkg == nil {
		if strings.HasPrefix(fn.Synthetic, "from type information (on demand)") ||
			strings.HasPrefix(fn.Synthetic, "wrapper ") ||
			strings.HasPrefix(fn.Synthetic, "bound ") ||
			strings.HasPrefix(fn.Synthetic, "thunk ") ||
			strings.HasSuffix(fn.name, "Error") ||
			strings.HasPrefix(fn.Synthetic, "instance ") ||
			strings.HasPrefix(fn.Synthetic, "instantiation ") ||
			(fn.parent != nil && len(fn.typeargs) > 0) /* anon fun in instance */ {
			// ok
		} else {
			s.errorf("nil Pkg")
		}
	}
	if src, syn := fn.Synthetic == "", fn.Syntax() != nil; src != syn {
		if len(fn.typeargs) > 0 && fn.Prog.mode&InstantiateGenerics != 0 {
			// ok (instantiation with InstantiateGenerics on)
		} else if fn.topLevelOrigin != nil && len(fn.typeargs) > 0 {
			// ok (we always have the syntax set for instantiation)
		} else if _, rng := fn.syntax.(*ast.RangeStmt); rng && fn.Synthetic == "range-over-func yield" {
			// ok (range-func-yields are both synthetic and keep syntax)
		} else {
			s.errorf("got fromSource=%t, hasSyntax=%t; want same values", src, syn)
		}
	}

	// Build the set of valid referrers.
	s.instrs = make(map[Instruction]unit)

	// TODO: switch to range-over-func when x/tools updates to 1.23.
	// instrs are the instructions that are present in the function.
	fn.instrs()(func(instr Instruction) bool {
		s.instrs[instr] = unit{}
		return true
	})

	// Check all Locals allocations appear in the function instruction.
	for i, l := range fn.Locals {
		if _, present := s.instrs[l]; !present {
			s.warnf("function doesn't contain Local alloc %s", l.Name())
		}

		if l.Parent() != fn {
			s.errorf("Local %s at index %d has wrong parent", l.Name(), i)
		}
		if l.Heap {
			s.errorf("Local %s at index %d has Heap flag set", l.Name(), i)
		}
	}
	for i, p := range fn.Params {
		if p.Parent() != fn {
			s.errorf("Param %s at index %d has wrong parent", p.Name(), i)
		}
		// Check common suffix of Signature and Params match type.
		if sig := fn.Signature; sig != nil {
			j := i - len(fn.Params) + sig.Params().Len() // index within sig.Params
			if j < 0 {
				continue
			}
			if !types.Identical(p.Type(), sig.Params().At(j).Type()) {
				s.errorf("Param %s at index %d has wrong type (%s, versus %s in Signature)", p.Name(), i, p.Type(), sig.Params().At(j).Type())

			}
		}
		s.checkReferrerList(p)
	}
	for i, fv := range fn.FreeVars {
		if fv.Parent() != fn {
			s.errorf("FreeVar %s at index %d has wrong parent", fv.Name(), i)
		}
		s.checkReferrerList(fv)
	}

	if fn.Blocks != nil && len(fn.Blocks) == 0 {
		// Function _had_ blocks (so it's not external) but
		// they were "optimized" away, even the entry block.
		s.errorf("Blocks slice is non-nil but empty")
	}
	for i, b := range fn.Blocks {
		if b == nil {
			s.warnf("nil *BasicBlock at f.Blocks[%d]", i)
			continue
		}
		s.checkBlock(b, i)
	}
	if fn.Recover != nil && fn.Blocks[fn.Recover.Index] != fn.Recover {
		s.errorf("Recover block is not in Blocks slice")
	}

	s.block = nil
	for i, anon := range fn.AnonFuncs {
		if anon.Parent() != fn {
			s.errorf("AnonFuncs[%d]=%s but %s.Parent()=%s", i, anon, anon, anon.Parent())
		}
		if i != int(anon.anonIdx) {
			s.errorf("AnonFuncs[%d]=%s but %s.anonIdx=%d", i, anon, anon, anon.anonIdx)
		}
	}
	s.fn = nil
	return !s.insane
}

// sanityCheckPackage checks invariants of packages upon creation.
// It does not require that the package is built.
// Unlike sanityCheck (for functions), it just panics at the first error.
func sanityCheckPackage(pkg *Package) {
	if pkg.Pkg == nil {
		panic(fmt.Sprintf("Package %s has no Object", pkg))
	}
	if pkg.info != nil {
		panic(fmt.Sprintf("package %s field 'info' is not cleared", pkg))
	}
	if pkg.files != nil {
		panic(fmt.Sprintf("package %s field 'files' is not cleared", pkg))
	}
	if pkg.created != nil {
		panic(fmt.Sprintf("package %s field 'created' is not cleared", pkg))
	}
	if pkg.initVersion != nil {
		panic(fmt.Sprintf("package %s field 'initVersion' is not cleared", pkg))
	}

	_ = pkg.String() // must not crash

	for name, mem := range pkg.Members {
		if name != mem.Name() {
			panic(fmt.Sprintf("%s: %T.Name() = %s, want %s",
				pkg.Pkg.Path(), mem, mem.Name(), name))
		}
		obj := mem.Object()
		if obj == nil {
			// This check is sound because fields
			// {Global,Function}.object have type
			// types.Object.  (If they were declared as
			// *types.{Var,Func}, we'd have a non-empty
			// interface containing a nil pointer.)

			continue // not all members have typechecker objects
		}
		if obj.Name() != name {
			if obj.Name() == "init" && strings.HasPrefix(mem.Name(), "init#") {
				// Ok.  The name of a declared init function varies between
				// its types.Func ("init") and its ssa.Function ("init#%d").
			} else {
				panic(fmt.Sprintf("%s: %T.Object().Name() = %s, want %s",
					pkg.Pkg.Path(), mem, obj.Name(), name))
			}
		}
		if obj.Pos() != mem.Pos() {
			panic(fmt.Sprintf("%s Pos=%d obj.Pos=%d", mem, mem.Pos(), obj.Pos()))
		}
	}
}
