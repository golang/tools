// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package typeparams

import (
	"go/ast"
	"go/types"
)

// TODO(adonovan): melt the trivial functions away.

// ForTypeSpec returns n.TypeParams.
func ForTypeSpec(n *ast.TypeSpec) *ast.FieldList {
	if n == nil {
		return nil
	}
	return n.TypeParams
}

// ForFuncType returns n.TypeParams.
func ForFuncType(n *ast.FuncType) *ast.FieldList {
	if n == nil {
		return nil
	}
	return n.TypeParams
}

// NewTypeParam calls types.NewTypeParam.
func NewTypeParam(name *types.TypeName, constraint types.Type) *types.TypeParam {
	return types.NewTypeParam(name, constraint)
}

// SetTypeParamConstraint calls tparam.SetConstraint(constraint).
func SetTypeParamConstraint(tparam *types.TypeParam, constraint types.Type) {
	tparam.SetConstraint(constraint)
}

// NewSignatureType calls types.NewSignatureType.
func NewSignatureType(recv *types.Var, recvTypeParams, typeParams []*types.TypeParam, params, results *types.Tuple, variadic bool) *types.Signature {
	return types.NewSignatureType(recv, recvTypeParams, typeParams, params, results, variadic)
}

// ForSignature returns sig.TypeParams()
func ForSignature(sig *types.Signature) *types.TypeParamList {
	return sig.TypeParams()
}

// RecvTypeParams returns sig.RecvTypeParams().
func RecvTypeParams(sig *types.Signature) *types.TypeParamList {
	return sig.RecvTypeParams()
}

// IsComparable calls iface.IsComparable().
func IsComparable(iface *types.Interface) bool {
	return iface.IsComparable()
}

// IsMethodSet calls iface.IsMethodSet().
func IsMethodSet(iface *types.Interface) bool {
	return iface.IsMethodSet()
}

// IsImplicit calls iface.IsImplicit().
func IsImplicit(iface *types.Interface) bool {
	return iface.IsImplicit()
}

// MarkImplicit calls iface.MarkImplicit().
func MarkImplicit(iface *types.Interface) {
	iface.MarkImplicit()
}

// ForNamed extracts the (possibly empty) type parameter object list from
// named.
func ForNamed(named *types.Named) *types.TypeParamList {
	return named.TypeParams()
}

// SetForNamed sets the type params tparams on n. Each tparam must be of
// dynamic type *types.TypeParam.
func SetForNamed(n *types.Named, tparams []*types.TypeParam) {
	n.SetTypeParams(tparams)
}

// NamedTypeArgs returns named.TypeArgs().
func NamedTypeArgs(named *types.Named) *types.TypeList {
	return named.TypeArgs()
}

// NamedTypeOrigin returns named.Orig().
func NamedTypeOrigin(named *types.Named) *types.Named {
	return named.Origin()
}

// NewTerm calls types.NewTerm.
func NewTerm(tilde bool, typ types.Type) *types.Term {
	return types.NewTerm(tilde, typ)
}

// NewUnion calls types.NewUnion.
func NewUnion(terms []*types.Term) *types.Union {
	return types.NewUnion(terms)
}

// InitInstanceInfo initializes info to record information about type and
// function instances.
func InitInstanceInfo(info *types.Info) {
	info.Instances = make(map[*ast.Ident]types.Instance)
}

// GetInstances returns info.Instances.
func GetInstances(info *types.Info) map[*ast.Ident]types.Instance {
	return info.Instances
}

// NewContext calls types.NewContext.
func NewContext() *types.Context {
	return types.NewContext()
}

// Instantiate calls types.Instantiate.
func Instantiate(ctxt *types.Context, typ types.Type, targs []types.Type, validate bool) (types.Type, error) {
	return types.Instantiate(ctxt, typ, targs, validate)
}
