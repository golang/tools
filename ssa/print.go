package ssa

// This file implements the String() methods for all Value and
// Instruction types.

import (
	"bytes"
	"fmt"
	"go/ast"
	"io"
	"sort"

	"code.google.com/p/go.tools/go/types"
)

func (id Id) String() string {
	if id.Pkg == nil {
		return id.Name
	}
	return fmt.Sprintf("%s/%s", id.Pkg.Path(), id.Name)
}

// relName returns the name of v relative to i.
// In most cases, this is identical to v.Name(), but for references to
// Functions (including methods) and Globals, the FullName is used
// instead, explicitly package-qualified for cross-package references.
//
func relName(v Value, i Instruction) string {
	switch v := v.(type) {
	case *Global:
		if i != nil && v.Pkg == i.Parent().Pkg {
			return v.Name()
		}
		return v.FullName()
	case *Function:
		var pkg *Package
		if i != nil {
			pkg = i.Parent().Pkg
		}
		return v.fullName(pkg)
	}
	return v.Name()
}

// Value.String()
//
// This method is provided only for debugging.
// It never appears in disassembly, which uses Value.Name().

func (v *Literal) String() string {
	return v.Name()
}

func (v *Parameter) String() string {
	return fmt.Sprintf("parameter %s : %s", v.Name(), v.Type())
}

func (v *Capture) String() string {
	return fmt.Sprintf("capture %s : %s", v.Name(), v.Type())
}

func (v *Global) String() string {
	return v.FullName()
}

func (v *Builtin) String() string {
	return fmt.Sprintf("builtin %s", v.Name())
}

func (v *Function) String() string {
	return v.FullName()
}

// FullName returns g's package-qualified name.
func (g *Global) FullName() string {
	return fmt.Sprintf("%s.%s", g.Pkg.Types.Path(), g.name)
}

// Instruction.String()

func (v *Alloc) String() string {
	op := "local"
	if v.Heap {
		op = "new"
	}
	return fmt.Sprintf("%s %s", op, v.Type().Deref())
}

func (v *Phi) String() string {
	var b bytes.Buffer
	b.WriteString("phi [")
	for i, edge := range v.Edges {
		if i > 0 {
			b.WriteString(", ")
		}
		// Be robust against malformed CFG.
		blockname := "?"
		if v.block != nil && i < len(v.block.Preds) {
			blockname = v.block.Preds[i].String()
		}
		b.WriteString(blockname)
		b.WriteString(": ")
		edgeVal := "<nil>" // be robust
		if edge != nil {
			edgeVal = relName(edge, v)
		}
		b.WriteString(edgeVal)
	}
	b.WriteString("]")
	if v.Comment != "" {
		b.WriteString(" #")
		b.WriteString(v.Comment)
	}
	return b.String()
}

func printCall(v *CallCommon, prefix string, instr Instruction) string {
	var b bytes.Buffer
	b.WriteString(prefix)
	if !v.IsInvoke() {
		b.WriteString(relName(v.Func, instr))
	} else {
		name := v.Recv.Type().Underlying().(*types.Interface).Method(v.Method).Name()
		fmt.Fprintf(&b, "invoke %s.%s [#%d]", relName(v.Recv, instr), name, v.Method)
	}
	b.WriteString("(")
	for i, arg := range v.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(relName(arg, instr))
	}
	if v.HasEllipsis {
		b.WriteString("...")
	}
	b.WriteString(")")
	return b.String()
}

func (c *CallCommon) String() string {
	return printCall(c, "", nil)
}

func (v *Call) String() string {
	return printCall(&v.Call, "", v)
}

func (v *ChangeType) String() string {
	return fmt.Sprintf("changetype %s <- %s (%s)", v.Type(), v.X.Type(), relName(v.X, v))
}

func (v *BinOp) String() string {
	return fmt.Sprintf("%s %s %s", relName(v.X, v), v.Op.String(), relName(v.Y, v))
}

func (v *UnOp) String() string {
	return fmt.Sprintf("%s%s%s", v.Op, relName(v.X, v), commaOk(v.CommaOk))
}

func (v *Convert) String() string {
	return fmt.Sprintf("convert %s <- %s (%s)", v.Type(), v.X.Type(), relName(v.X, v))
}

func (v *ChangeInterface) String() string {
	return fmt.Sprintf("change interface %s <- %s (%s)", v.Type(), v.X.Type(), relName(v.X, v))
}

func (v *MakeInterface) String() string {
	return fmt.Sprintf("make %s <- %s (%s)", v.Type(), v.X.Type(), relName(v.X, v))
}

func (v *MakeClosure) String() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "make closure %s", relName(v.Fn, v))
	if v.Bindings != nil {
		b.WriteString(" [")
		for i, c := range v.Bindings {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(relName(c, v))
		}
		b.WriteString("]")
	}
	return b.String()
}

func (v *MakeSlice) String() string {
	var b bytes.Buffer
	b.WriteString("make ")
	b.WriteString(v.Type().String())
	b.WriteString(" ")
	b.WriteString(relName(v.Len, v))
	b.WriteString(" ")
	b.WriteString(relName(v.Cap, v))
	return b.String()
}

func (v *Slice) String() string {
	var b bytes.Buffer
	b.WriteString("slice ")
	b.WriteString(relName(v.X, v))
	b.WriteString("[")
	if v.Low != nil {
		b.WriteString(relName(v.Low, v))
	}
	b.WriteString(":")
	if v.High != nil {
		b.WriteString(relName(v.High, v))
	}
	b.WriteString("]")
	return b.String()
}

func (v *MakeMap) String() string {
	res := ""
	if v.Reserve != nil {
		res = relName(v.Reserve, v)
	}
	return fmt.Sprintf("make %s %s", v.Type(), res)
}

func (v *MakeChan) String() string {
	return fmt.Sprintf("make %s %s", v.Type(), relName(v.Size, v))
}

func (v *FieldAddr) String() string {
	st := v.X.Type().Deref().Underlying().(*types.Struct)
	// Be robust against a bad index.
	name := "?"
	if 0 <= v.Field && v.Field < st.NumFields() {
		name = st.Field(v.Field).Name()
	}
	return fmt.Sprintf("&%s.%s [#%d]", relName(v.X, v), name, v.Field)
}

func (v *Field) String() string {
	st := v.X.Type().Underlying().(*types.Struct)
	// Be robust against a bad index.
	name := "?"
	if 0 <= v.Field && v.Field < st.NumFields() {
		name = st.Field(v.Field).Name()
	}
	return fmt.Sprintf("%s.%s [#%d]", relName(v.X, v), name, v.Field)
}

func (v *IndexAddr) String() string {
	return fmt.Sprintf("&%s[%s]", relName(v.X, v), relName(v.Index, v))
}

func (v *Index) String() string {
	return fmt.Sprintf("%s[%s]", relName(v.X, v), relName(v.Index, v))
}

func (v *Lookup) String() string {
	return fmt.Sprintf("%s[%s]%s", relName(v.X, v), relName(v.Index, v), commaOk(v.CommaOk))
}

func (v *Range) String() string {
	return "range " + relName(v.X, v)
}

func (v *Next) String() string {
	return "next " + relName(v.Iter, v)
}

func (v *TypeAssert) String() string {
	return fmt.Sprintf("typeassert%s %s.(%s)", commaOk(v.CommaOk), relName(v.X, v), v.AssertedType)
}

func (v *Extract) String() string {
	return fmt.Sprintf("extract %s #%d", relName(v.Tuple, v), v.Index)
}

func (s *Jump) String() string {
	// Be robust against malformed CFG.
	blockname := "?"
	if s.block != nil && len(s.block.Succs) == 1 {
		blockname = s.block.Succs[0].String()
	}
	return fmt.Sprintf("jump %s", blockname)
}

func (s *If) String() string {
	// Be robust against malformed CFG.
	tblockname, fblockname := "?", "?"
	if s.block != nil && len(s.block.Succs) == 2 {
		tblockname = s.block.Succs[0].String()
		fblockname = s.block.Succs[1].String()
	}
	return fmt.Sprintf("if %s goto %s else %s", relName(s.Cond, s), tblockname, fblockname)
}

func (s *Go) String() string {
	return printCall(&s.Call, "go ", s)
}

func (s *Panic) String() string {
	return "panic " + relName(s.X, s)
}

func (s *Ret) String() string {
	var b bytes.Buffer
	b.WriteString("ret")
	for i, r := range s.Results {
		if i == 0 {
			b.WriteString(" ")
		} else {
			b.WriteString(", ")
		}
		b.WriteString(relName(r, s))
	}
	return b.String()
}

func (*RunDefers) String() string {
	return "rundefers"
}

func (s *Send) String() string {
	return fmt.Sprintf("send %s <- %s", relName(s.Chan, s), relName(s.X, s))
}

func (s *Defer) String() string {
	return printCall(&s.Call, "defer ", s)
}

func (s *Select) String() string {
	var b bytes.Buffer
	for i, st := range s.States {
		if i > 0 {
			b.WriteString(", ")
		}
		if st.Dir == ast.RECV {
			b.WriteString("<-")
			b.WriteString(relName(st.Chan, s))
		} else {
			b.WriteString(relName(st.Chan, s))
			b.WriteString("<-")
			b.WriteString(relName(st.Send, s))
		}
	}
	non := ""
	if !s.Blocking {
		non = "non"
	}
	return fmt.Sprintf("select %sblocking [%s]", non, b.String())
}

func (s *Store) String() string {
	return fmt.Sprintf("*%s = %s", relName(s.Addr, s), relName(s.Val, s))
}

func (s *MapUpdate) String() string {
	return fmt.Sprintf("%s[%s] = %s", relName(s.Map, s), relName(s.Key, s), relName(s.Value, s))
}

func (p *Package) String() string {
	return "package " + p.Types.Path()
}

func (p *Package) DumpTo(w io.Writer) {
	fmt.Fprintf(w, "%s:\n", p)

	var names []string
	maxname := 0
	for name := range p.Members {
		if l := len(name); l > maxname {
			maxname = l
		}
		names = append(names, name)
	}

	sort.Strings(names)
	for _, name := range names {
		switch mem := p.Members[name].(type) {
		case *Constant:
			fmt.Fprintf(w, "  const %-*s %s = %s\n", maxname, name, mem.Name(), mem.Value.Name())

		case *Function:
			fmt.Fprintf(w, "  func  %-*s %s\n", maxname, name, mem.Type())

		case *Type:
			fmt.Fprintf(w, "  type  %-*s %s\n", maxname, name, mem.Type().Underlying())
			// We display only mset(*T) since its keys
			// are a superset of mset(T)'s keys, though the
			// methods themselves may differ,
			// e.g. promotion wrappers.
			// NB: if mem.Type() is a pointer, mset is empty.
			mset := p.Prog.MethodSet(pointer(mem.Type()))
			var keys ids
			for id := range mset {
				keys = append(keys, id)
			}
			sort.Sort(keys)
			for _, id := range keys {
				method := mset[id]
				// TODO(adonovan): show pointerness of receiver of declared method, not the index

				fmt.Fprintf(w, "    method %s %s\n", id, method.Signature)
			}

		case *Global:
			fmt.Fprintf(w, "  var   %-*s %s\n", maxname, name, mem.Type())

		}
	}
}

func commaOk(x bool) string {
	if x {
		return ",ok"
	}
	return ""
}
