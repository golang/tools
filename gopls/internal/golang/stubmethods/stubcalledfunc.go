package stubmethods

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/tools/gopls/internal/util/typesutil"
)

// CallStubInfo represents a missing method
// that a receiver type is about to generate
// which has “type X has no field or method Y" error
type CallStubInfo struct {
	Fset       *token.FileSet // the FileSet used to type-check the types below
	Receiver   *types.Named   // the method's receiver type
	pointer    bool
	args       []argument // the argument list of new methods
	methodName string
	returns    []types.Type
}

type argument struct {
	Name string
	Typ  types.Type // the type of argument, infered from CallExpr
}

// Emit generate the missing method based on type info of si.Receiver and CallExpr.
func (si *CallStubInfo) Emit(out *bytes.Buffer, qual types.Qualifier) error {
	recv := si.Receiver.Obj()
	// Pointer receiver?
	var star string
	if si.pointer {
		star = "*"
	}

	// If there are any that have named receiver, choose the first one.
	// Otherwise, use lowercase for the first letter of the object.
	rn := strings.ToLower(si.Receiver.Obj().Name()[0:1])
	for i := 0; i < si.Receiver.NumMethods(); i++ {
		if recv := si.Receiver.Method(i).Signature().Recv(); recv.Name() != "" {
			rn = recv.Name()
			break
		}
	}

	mrn := rn + " "

	// Avoid duplicated argument name
	usedNames := make(map[string]int)
	for i, arg := range si.args {
		name := arg.Name
		if count, exists := usedNames[name]; exists {
			// Name has been used before; increment the count and append it to the name
			count++
			usedNames[name] = count
			si.args[i].Name = fmt.Sprintf("%s%d", name, count)
		} else {
			usedNames[name] = 0
		}
	}

	// Avoid conflict receiver name
	for _, arg := range si.args {
		name := arg.Name
		if name == rn {
			mrn = ""
		}
	}

	signature := ""
	for i, arg := range si.args {
		signature = signature + arg.Name + " " + types.TypeString(types.Default(arg.Typ), qual)
		if i < len(si.args)-1 {
			signature = signature + ", "
		}
	}

	ret := ""
	if len(si.returns) > 1 {
		ret = "("
	}
	for i, r := range si.returns {
		ret = ret + " " + types.TypeString(types.Default(r), qual)
		if i < len(si.returns)-1 {
			ret = ret + ", "
		}
	}
	if len(si.returns) > 1 {
		ret = ret + ")"
	}

	fmt.Fprintf(out, `
func (%s%s%s%s) %s(%s) %s{
	panic("unimplemented")
}
`,
		mrn,
		star,
		recv.Name(),
		typesutil.FormatTypeParams(si.Receiver.TypeParams()),
		si.methodName,
		signature,
		ret,
	)
	return nil
}

// GetCallStubInfo extracts necessary information to generate a method definition from
// a CallExpr.
func GetCallStubInfo(fset *token.FileSet, info *types.Info, path []ast.Node, pos token.Pos) *CallStubInfo {
	for i, n := range path {
		switch n := n.(type) {
		case *ast.CallExpr:
			s, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return nil
			}

			// If recvExpr is a package name, compiler error would be
			// e.g., "undefined: http.bar", thus will not hit this code path.
			recvExpr := s.X
			recvType, pointer := concreteType(recvExpr, info)

			if recvType == nil || recvType.Obj().Pkg() == nil {
				return nil
			}

			// A function-local type cannot be stubbed
			// since there's nowhere to put the methods.
			recv := recvType.Obj()
			if recv.Parent() != recv.Pkg().Scope() {
				return nil
			}

			var args []argument
			for i, arg := range n.Args {
				typ, name, err := argInfo(arg, info, i)
				if err != nil {
					return nil
				}
				args = append(args, argument{
					Name: name,
					Typ:  typ,
				})
			}

			var rets []types.Type
			if i < len(path)-1 {
				switch parent := path[i+1].(type) {
				case *ast.AssignStmt:
					// Append all lhs's type
					if len(parent.Rhs) == 1 {
						for _, lhs := range parent.Lhs {
							if t, ok := info.Types[lhs]; ok {
								rets = append(rets, t.Type)
							}
						}
						break
					}

					// Lhs and Rhs counts do not match, give up
					if len(parent.Lhs) != len(parent.Rhs) {
						break
					}

					// Append corresponding index of lhs's type
					for i, rhs := range parent.Rhs {
						if rhs.Pos() <= pos && pos <= rhs.End() {
							left := parent.Lhs[i]
							if t, ok := info.Types[left]; ok {
								rets = append(rets, t.Type)
							}
							break
						}
					}
				case *ast.CallExpr:
					// Find argument containing pos.
					argIdx := -1
					for i, callArg := range parent.Args {
						if callArg.Pos() <= pos && pos <= callArg.End() {
							argIdx = i
							break
						}
					}
					if argIdx == -1 {
						break
					}

					var def types.Object
					switch f := parent.Fun.(type) {
					// functon call
					case *ast.Ident:
						def, ok = info.Uses[f]
						if !ok {
							break
						}
					// method call
					case *ast.SelectorExpr:
						def, ok = info.Uses[f.Sel]
						if !ok {
							break
						}
					}

					sig, ok := types.Unalias(def.Type()).(*types.Signature)
					if !ok {
						break
					}
					var paramType types.Type
					if sig.Variadic() && argIdx >= sig.Params().Len()-1 {
						v := sig.Params().At(sig.Params().Len() - 1)
						if s, _ := v.Type().(*types.Slice); s != nil {
							paramType = s.Elem()
						}
					} else if argIdx < sig.Params().Len() {
						paramType = sig.Params().At(argIdx).Type()
					}
					if paramType == nil {
						break
					}
					rets = append(rets, paramType)
				}
			}
			// A function-local type cannot be stubbed
			// since there's nowhere to put the methods.

			return &CallStubInfo{
				Fset:       fset,
				Receiver:   recvType,
				methodName: s.Sel.Name,
				pointer:    pointer,
				args:       args,
				returns:    rets,
			}
		}
	}
	return nil
}

// indentTrail find the position of the last uppercase letter,
// extract the substring from that point onward,
// and convert it to lowercase.
func identTrail(identName string) string {
	s := identName
	lastUpperIndex := -1

	for i, r := range s {
		if unicode.IsUpper(r) {
			lastUpperIndex = i
		}
	}
	if lastUpperIndex != -1 {
		last := s[lastUpperIndex:]
		return strings.ToLower(last)
	} else {
		return identName
	}
}

// argInfo generate placeholder name heuristically for a function argument.
func argInfo(e ast.Expr, info *types.Info, i int) (types.Type, string, error) {
	tv, ok := info.Types[e]
	if !ok {
		return nil, "", fmt.Errorf("no type info")
	}

	// uses the identifier's name as the argument name.
	switch t := e.(type) {
	case *ast.Ident:
		return tv.Type, identTrail(t.Name), nil
	case *ast.SelectorExpr:
		return tv.Type, identTrail(t.Sel.Name), nil
	}

	typ := tv.Type
	ptr, isPtr := types.Unalias(typ).(*types.Pointer)
	if isPtr {
		typ = ptr.Elem()
	}

	// Uses the first character of the type name as the argument name for builtin types
	switch t := types.Default(typ).(type) {
	case *types.Basic:
		return tv.Type, t.Name()[0:1], nil
	case *types.Signature:
		return tv.Type, "f", nil
	case *types.Map:
		return tv.Type, "m", nil
	case *types.Chan:
		return tv.Type, "ch", nil
	case *types.Named:
		n := t.Obj().Name()
		return tv.Type, identTrail(n), nil
	default:
		return tv.Type, "args" + strconv.Itoa(i), nil
	}
}
