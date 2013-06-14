package ssa

// This file implements the Function and BasicBlock types.

import (
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"os"
	"strings"

	"code.google.com/p/go.tools/go/types"
)

// addEdge adds a control-flow graph edge from from to to.
func addEdge(from, to *BasicBlock) {
	from.Succs = append(from.Succs, to)
	to.Preds = append(to.Preds, from)
}

// Parent returns the function that contains block b.
func (b *BasicBlock) Parent() *Function { return b.parent }

// String returns a human-readable label of this block.
// It is not guaranteed unique within the function.
//
func (b *BasicBlock) String() string {
	return fmt.Sprintf("%d.%s", b.Index, b.Comment)
}

// emit appends an instruction to the current basic block.
// If the instruction defines a Value, it is returned.
//
func (b *BasicBlock) emit(i Instruction) Value {
	i.SetBlock(b)
	b.Instrs = append(b.Instrs, i)
	v, _ := i.(Value)
	return v
}

// predIndex returns the i such that b.Preds[i] == c or panics if
// there is none.
func (b *BasicBlock) predIndex(c *BasicBlock) int {
	for i, pred := range b.Preds {
		if pred == c {
			return i
		}
	}
	panic(fmt.Sprintf("no edge %s -> %s", c, b))
}

// hasPhi returns true if b.Instrs contains φ-nodes.
func (b *BasicBlock) hasPhi() bool {
	_, ok := b.Instrs[0].(*Phi)
	return ok
}

// phis returns the prefix of b.Instrs containing all the block's φ-nodes.
func (b *BasicBlock) phis() []Instruction {
	for i, instr := range b.Instrs {
		if _, ok := instr.(*Phi); !ok {
			return b.Instrs[:i]
		}
	}
	return nil // unreachable in well-formed blocks
}

// replacePred replaces all occurrences of p in b's predecessor list with q.
// Ordinarily there should be at most one.
//
func (b *BasicBlock) replacePred(p, q *BasicBlock) {
	for i, pred := range b.Preds {
		if pred == p {
			b.Preds[i] = q
		}
	}
}

// replaceSucc replaces all occurrences of p in b's successor list with q.
// Ordinarily there should be at most one.
//
func (b *BasicBlock) replaceSucc(p, q *BasicBlock) {
	for i, succ := range b.Succs {
		if succ == p {
			b.Succs[i] = q
		}
	}
}

// removePred removes all occurrences of p in b's
// predecessor list and φ-nodes.
// Ordinarily there should be at most one.
//
func (b *BasicBlock) removePred(p *BasicBlock) {
	phis := b.phis()

	// We must preserve edge order for φ-nodes.
	j := 0
	for i, pred := range b.Preds {
		if pred != p {
			b.Preds[j] = b.Preds[i]
			// Strike out φ-edge too.
			for _, instr := range phis {
				phi := instr.(*Phi)
				phi.Edges[j] = phi.Edges[i]
			}
			j++
		}
	}
	// Nil out b.Preds[j:] and φ-edges[j:] to aid GC.
	for i := j; i < len(b.Preds); i++ {
		b.Preds[i] = nil
		for _, instr := range phis {
			instr.(*Phi).Edges[i] = nil
		}
	}
	b.Preds = b.Preds[:j]
	for _, instr := range phis {
		phi := instr.(*Phi)
		phi.Edges = phi.Edges[:j]
	}
}

// Destinations associated with unlabelled for/switch/select stmts.
// We push/pop one of these as we enter/leave each construct and for
// each BranchStmt we scan for the innermost target of the right type.
//
type targets struct {
	tail         *targets // rest of stack
	_break       *BasicBlock
	_continue    *BasicBlock
	_fallthrough *BasicBlock
}

// Destinations associated with a labelled block.
// We populate these as labels are encountered in forward gotos or
// labelled statements.
//
type lblock struct {
	_goto     *BasicBlock
	_break    *BasicBlock
	_continue *BasicBlock
}

// funcSyntax holds the syntax tree for the function declaration and body.
type funcSyntax struct {
	recvField    *ast.FieldList
	paramFields  *ast.FieldList
	resultFields *ast.FieldList
	body         *ast.BlockStmt
}

// labelledBlock returns the branch target associated with the
// specified label, creating it if needed.
//
func (f *Function) labelledBlock(label *ast.Ident) *lblock {
	lb := f.lblocks[label.Obj]
	if lb == nil {
		lb = &lblock{_goto: f.newBasicBlock(label.Name)}
		if f.lblocks == nil {
			f.lblocks = make(map[*ast.Object]*lblock)
		}
		f.lblocks[label.Obj] = lb
	}
	return lb
}

// addParam adds a (non-escaping) parameter to f.Params of the
// specified name, type and source position.
//
func (f *Function) addParam(name string, typ types.Type, pos token.Pos) *Parameter {
	v := &Parameter{
		name:   name,
		typ:    typ,
		pos:    pos,
		parent: f,
	}
	f.Params = append(f.Params, v)
	return v
}

func (f *Function) addParamObj(obj types.Object) *Parameter {
	name := obj.Name()
	if name == "" {
		name = fmt.Sprintf("arg%d", len(f.Params))
	}
	return f.addParam(name, obj.Type(), obj.Pos())
}

// addSpilledParam declares a parameter that is pre-spilled to the
// stack; the function body will load/store the spilled location.
// Subsequent lifting will eliminate spills where possible.
//
func (f *Function) addSpilledParam(obj types.Object) {
	param := f.addParamObj(obj)
	spill := &Alloc{
		name: obj.Name() + "~", // "~" means "spilled"
		typ:  pointer(obj.Type()),
		pos:  obj.Pos(),
	}
	f.objects[obj] = spill
	f.Locals = append(f.Locals, spill)
	f.emit(spill)
	f.emit(&Store{Addr: spill, Val: param})
}

// startBody initializes the function prior to generating SSA code for its body.
// Precondition: f.Type() already set.
//
func (f *Function) startBody() {
	f.currentBlock = f.newBasicBlock("entry")
	f.objects = make(map[types.Object]Value) // needed for some synthetics, e.g. init
}

// createSyntacticParams populates f.Params and generates code (spills
// and named result locals) for all the parameters declared in the
// syntax.  In addition it populates the f.objects mapping.
//
// Preconditions:
// f.syntax != nil, i.e. this is a Go source function.
// f.startBody() was called.
// Postcondition:
// len(f.Params) == len(f.Signature.Params) + (f.Signature.Recv() ? 1 : 0)
//
func (f *Function) createSyntacticParams() {
	// Receiver (at most one inner iteration).
	if f.syntax.recvField != nil {
		for _, field := range f.syntax.recvField.List {
			for _, n := range field.Names {
				f.addSpilledParam(f.Pkg.objectOf(n))
			}
			// Anonymous receiver?  No need to spill.
			if field.Names == nil {
				f.addParamObj(f.Signature.Recv())
			}
		}
	}

	// Parameters.
	if f.syntax.paramFields != nil {
		n := len(f.Params) // 1 if has recv, 0 otherwise
		for _, field := range f.syntax.paramFields.List {
			for _, n := range field.Names {
				f.addSpilledParam(f.Pkg.objectOf(n))
			}
			// Anonymous parameter?  No need to spill.
			if field.Names == nil {
				f.addParamObj(f.Signature.Params().At(len(f.Params) - n))
			}
		}
	}

	// Named results.
	if f.syntax.resultFields != nil {
		for _, field := range f.syntax.resultFields.List {
			// Implicit "var" decl of locals for named results.
			for _, n := range field.Names {
				f.namedResults = append(f.namedResults, f.addNamedLocal(f.Pkg.objectOf(n)))
			}
		}
	}
}

// numberRegisters assigns numbers to all SSA registers
// (value-defining Instructions) in f, to aid debugging.
// (Non-Instruction Values are named at construction.)
// NB: named Allocs retain their existing name.
// TODO(adonovan): when we have source position info,
// preserve names only for source locals.
//
func numberRegisters(f *Function) {
	a, v := 0, 0
	for _, b := range f.Blocks {
		for _, instr := range b.Instrs {
			switch instr := instr.(type) {
			case *Alloc:
				// Allocs may be named at birth.
				if instr.name == "" {
					instr.name = fmt.Sprintf("a%d", a)
					a++
				}
			case Value:
				instr.(interface {
					setNum(int)
				}).setNum(v)
				v++
			}
		}
	}
}

// buildReferrers populates the def/use information in all non-nil
// Value.Referrers slice.
// Precondition: all such slices are initially empty.
func buildReferrers(f *Function) {
	var rands []*Value
	for _, b := range f.Blocks {
		for _, instr := range b.Instrs {
			rands = instr.Operands(rands[:0]) // recycle storage
			for _, rand := range rands {
				if r := *rand; r != nil {
					if ref := r.Referrers(); ref != nil {
						*ref = append(*ref, instr)
					}
				}
			}
		}
	}
}

// finishBody() finalizes the function after SSA code generation of its body.
func (f *Function) finishBody() {
	f.objects = nil
	f.namedResults = nil
	f.currentBlock = nil
	f.lblocks = nil
	f.syntax = nil

	// Remove any f.Locals that are now heap-allocated.
	j := 0
	for _, l := range f.Locals {
		if !l.Heap {
			f.Locals[j] = l
			j++
		}
	}
	// Nil out f.Locals[j:] to aid GC.
	for i := j; i < len(f.Locals); i++ {
		f.Locals[i] = nil
	}
	f.Locals = f.Locals[:j]

	optimizeBlocks(f)

	buildReferrers(f)

	if f.Prog.mode&NaiveForm == 0 {
		// For debugging pre-state of lifting pass:
		// numberRegisters(f)
		// f.DumpTo(os.Stderr)

		lift(f)
	}

	numberRegisters(f)

	if f.Prog.mode&LogFunctions != 0 {
		f.DumpTo(os.Stderr)
	}

	if f.Prog.mode&SanityCheckFunctions != 0 {
		MustSanityCheck(f, nil)
	}
}

// removeNilBlocks eliminates nils from f.Blocks and updates each
// BasicBlock.Index.  Use this after any pass that may delete blocks.
//
func (f *Function) removeNilBlocks() {
	j := 0
	for _, b := range f.Blocks {
		if b != nil {
			b.Index = j
			f.Blocks[j] = b
			j++
		}
	}
	// Nil out f.Blocks[j:] to aid GC.
	for i := j; i < len(f.Blocks); i++ {
		f.Blocks[i] = nil
	}
	f.Blocks = f.Blocks[:j]
}

// addNamedLocal creates a local variable, adds it to function f and
// returns it.  Its name and type are taken from obj.  Subsequent
// calls to f.lookup(obj) will return the same local.
//
// Precondition: f.syntax != nil (i.e. a Go source function).
//
func (f *Function) addNamedLocal(obj types.Object) *Alloc {
	l := f.addLocal(obj.Type(), obj.Pos())
	l.name = obj.Name()
	f.objects[obj] = l
	return l
}

// addLocal creates an anonymous local variable of type typ, adds it
// to function f and returns it.  pos is the optional source location.
//
func (f *Function) addLocal(typ types.Type, pos token.Pos) *Alloc {
	v := &Alloc{typ: pointer(typ), pos: pos}
	f.Locals = append(f.Locals, v)
	f.emit(v)
	return v
}

// lookup returns the address of the named variable identified by obj
// that is local to function f or one of its enclosing functions.
// If escaping, the reference comes from a potentially escaping pointer
// expression and the referent must be heap-allocated.
//
func (f *Function) lookup(obj types.Object, escaping bool) Value {
	if v, ok := f.objects[obj]; ok {
		if alloc, ok := v.(*Alloc); ok && escaping {
			alloc.Heap = true
		}
		return v // function-local var (address)
	}

	// Definition must be in an enclosing function;
	// plumb it through intervening closures.
	if f.Enclosing == nil {
		panic("no Value for type.Object " + obj.Name())
	}
	outer := f.Enclosing.lookup(obj, true) // escaping
	v := &Capture{
		name:   outer.Name(),
		typ:    outer.Type(),
		pos:    outer.Pos(),
		outer:  outer,
		parent: f,
	}
	f.objects[obj] = v
	f.FreeVars = append(f.FreeVars, v)
	return v
}

// emit emits the specified instruction to function f, updating the
// control-flow graph if required.
//
func (f *Function) emit(instr Instruction) Value {
	return f.currentBlock.emit(instr)
}

// FullName returns the full name of this function, qualified by
// package name, receiver type, etc.
//
// The specific formatting rules are not guaranteed and may change.
//
// Examples:
//      "math.IsNaN"                // a package-level function
//      "IsNaN"                     // intra-package reference to same
//      "(*sync.WaitGroup).Add"     // a declared method
//      "(*exp/ssa.Ret).Block"      // a promotion wrapper method
//      "(ssa.Instruction).Block"   // an interface method wrapper
//      "func@5.32"                 // an anonymous function
//      "bound$(*T).f"              // a bound method wrapper
//
func (f *Function) FullName() string {
	return f.fullName(nil)
}

// Like FullName, but if from==f.Pkg, suppress package qualification.
func (f *Function) fullName(from *Package) string {
	// TODO(adonovan): expose less fragile case discrimination.

	// Anonymous?
	if f.Enclosing != nil {
		return f.name
	}

	recv := f.Signature.Recv()

	// Synthetic?
	if f.Pkg == nil {
		var recvType types.Type
		if recv != nil {
			recvType = recv.Type() // promotion wrapper
		} else if strings.HasPrefix(f.name, "bound$") {
			return f.name // bound method wrapper
		} else {
			recvType = f.Params[0].Type() // interface method wrapper
		}
		return fmt.Sprintf("(%s).%s", recvType, f.name)
	}

	// Declared method?
	if recv != nil {
		return fmt.Sprintf("(%s).%s", recv.Type(), f.name)
	}

	// Package-level function.
	// Prefix with package name for cross-package references only.
	if from != f.Pkg {
		return fmt.Sprintf("%s.%s", f.Pkg.Types.Path(), f.name)
	}
	return f.name
}

// writeSignature writes to w the signature sig in declaration syntax.
// Derived from types.Signature.String().
//
func writeSignature(w io.Writer, name string, sig *types.Signature, params []*Parameter) {
	io.WriteString(w, "func ")
	if recv := sig.Recv(); recv != nil {
		io.WriteString(w, "(")
		if n := params[0].Name(); n != "" {
			io.WriteString(w, n)
			io.WriteString(w, " ")
		}
		io.WriteString(w, params[0].Type().String())
		io.WriteString(w, ") ")
		params = params[1:]
	}
	io.WriteString(w, name)
	io.WriteString(w, "(")
	for i, v := range params {
		if i > 0 {
			io.WriteString(w, ", ")
		}
		io.WriteString(w, v.Name())
		io.WriteString(w, " ")
		if sig.IsVariadic() && i == len(params)-1 {
			io.WriteString(w, "...")
			io.WriteString(w, v.Type().Underlying().(*types.Slice).Elem().String())
		} else {
			io.WriteString(w, v.Type().String())
		}
	}
	io.WriteString(w, ")")
	if n := sig.Results().Len(); n > 0 {
		io.WriteString(w, " ")
		r := sig.Results()
		if n == 1 && r.At(0).Name() == "" {
			io.WriteString(w, r.At(0).Type().String())
		} else {
			io.WriteString(w, r.String())
		}
	}
}

// DumpTo prints to w a human readable "disassembly" of the SSA code of
// all basic blocks of function f.
//
func (f *Function) DumpTo(w io.Writer) {
	fmt.Fprintf(w, "# Name: %s\n", f.FullName())
	if pos := f.Pos(); pos.IsValid() {
		fmt.Fprintf(w, "# Declared at %s\n", f.Prog.Files.Position(pos))
	} else {
		fmt.Fprintln(w, "# Synthetic")
	}

	if f.Enclosing != nil {
		fmt.Fprintf(w, "# Parent: %s\n", f.Enclosing.Name())
	}

	if f.FreeVars != nil {
		io.WriteString(w, "# Free variables:\n")
		for i, fv := range f.FreeVars {
			fmt.Fprintf(w, "# % 3d:\t%s %s\n", i, fv.Name(), fv.Type())
		}
	}

	if len(f.Locals) > 0 {
		io.WriteString(w, "# Locals:\n")
		for i, l := range f.Locals {
			fmt.Fprintf(w, "# % 3d:\t%s %s\n", i, l.Name(), l.Type().Deref())
		}
	}

	writeSignature(w, f.Name(), f.Signature, f.Params)
	io.WriteString(w, ":\n")

	if f.Blocks == nil {
		io.WriteString(w, "\t(external)\n")
	}

	// NB. column calculations are confused by non-ASCII characters.
	const punchcard = 80 // for old time's sake.
	for _, b := range f.Blocks {
		if b == nil {
			// Corrupt CFG.
			fmt.Fprintf(w, ".nil:\n")
			continue
		}
		n, _ := fmt.Fprintf(w, ".%s:", b)
		fmt.Fprintf(w, "%*sP:%d S:%d\n", punchcard-1-n-len("P:n S:n"), "", len(b.Preds), len(b.Succs))

		if false { // CFG debugging
			fmt.Fprintf(w, "\t# CFG: %s --> %s --> %s\n", b.Preds, b, b.Succs)
		}
		for _, instr := range b.Instrs {
			io.WriteString(w, "\t")
			switch v := instr.(type) {
			case Value:
				l := punchcard
				// Left-align the instruction.
				if name := v.Name(); name != "" {
					n, _ := fmt.Fprintf(w, "%s = ", name)
					l -= n
				}
				n, _ := io.WriteString(w, instr.String())
				l -= n
				// Right-align the type.
				if t := v.Type(); t != nil {
					fmt.Fprintf(w, " %*s", l-10, t)
				}
			case nil:
				// Be robust against bad transforms.
				io.WriteString(w, "<deleted>")
			default:
				io.WriteString(w, instr.String())
			}
			io.WriteString(w, "\n")
		}
	}
	fmt.Fprintf(w, "\n")
}

// newBasicBlock adds to f a new basic block and returns it.  It does
// not automatically become the current block for subsequent calls to emit.
// comment is an optional string for more readable debugging output.
//
func (f *Function) newBasicBlock(comment string) *BasicBlock {
	b := &BasicBlock{
		Index:   len(f.Blocks),
		Comment: comment,
		parent:  f,
	}
	b.Succs = b.succs2[:0]
	f.Blocks = append(f.Blocks, b)
	return b
}

// NewFunction returns a new synthetic Function instance with its name
// and signature fields set as specified.
//
// The caller is responsible for initializing the remaining fields of
// the function object, e.g. Pkg, Prog, Params, Blocks.
//
// It is practically impossible for clients to construct well-formed
// SSA functions/packages/programs directly, so we assume this is the
// job of the Builder alone.  NewFunction exists to provide clients a
// little flexibility.  For example, analysis tools may wish to
// construct fake Functions for the root of the callgraph, a fake
// "reflect" package, etc.
//
// TODO(adonovan): think harder about the API here.
//
func NewFunction(name string, sig *types.Signature) *Function {
	return &Function{name: name, Signature: sig}
}
