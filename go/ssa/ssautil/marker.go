package ssautil

import (
	"go/ast"

	"golang.org/x/tools/go/ssa"
)

// IsMarkerMethod returns true if the function is a method that implements a marker interface.
// A marker interface method is defined by the following properties:
// - Is a method (i.e. has a receiver)
// - Is unexported
// - Has no params (other than the receiver) and no results
// - Has an empty function body
func IsMarkerMethod(fn *ssa.Function) bool {
	var sig = fn.Signature

	if isMethod := sig.Recv() != nil; !isMethod {
		return false
	}
	if isUnexported := !ast.IsExported(fn.Name()); !isUnexported {
		return false
	}
	if hasNoParams := sig.Params() == nil; !hasNoParams {
		return false
	}
	if hasNoResults := sig.Results() == nil; !hasNoResults {
		return false
	}
	if isEmpty := isFunctionEmpty(fn); !isEmpty {
		return false
	}

	return true
}

func isFunctionEmpty(fun *ssa.Function) bool {
	// SSA analyzes the source code
	// if blocks is nil, it means it's an external (imported) function. This shouldn't be flagged as a marker method
	if fun.Blocks == nil {
		return false
	}

	if len(fun.Blocks) != 1 {
		return false
	}

	blk := fun.Blocks[0]
	if len(blk.Instrs) > 1 {
		return false
	}

	instr := blk.Instrs[0]
	if _, ok := instr.(*ssa.Return); !ok {
		return false
	}

	return true
}
