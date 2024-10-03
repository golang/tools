// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aliases

import (
	"go/build"
	"go/token"
	"go/types"
	"os"
	"slices"
	"strings"
	"sync"
)

// Package aliases defines backward compatible shims
// for the types.Alias type representation added in 1.22.
// This defines placeholders for x/tools until 1.26.

// NewAlias creates a new TypeName in Package pkg that
// is an alias for the type rhs.
//
// The enabled parameter determines whether the resulting [TypeName]'s
// type is an [types.Alias]. Its value must be the result of a call to
// [Enabled], which computes the effective value of
// GODEBUG=gotypesalias=... by invoking the type checker. The Enabled
// function is expensive and should be called once per task (e.g.
// package import), not once per call to NewAlias.
//
// Precondition: enabled || len(tparams)==0.
// If materialized aliases are disabled, there must not be any type parameters.
func NewAlias(enabled bool, pos token.Pos, pkg *types.Package, name string, rhs types.Type, tparams []*types.TypeParam) *types.TypeName {
	if enabled {
		tname := types.NewTypeName(pos, pkg, name, nil)
		SetTypeParams(types.NewAlias(tname, rhs), tparams)
		return tname
	}
	if len(tparams) > 0 {
		panic("cannot create an alias with type parameters when gotypesalias is not enabled")
	}
	return types.NewTypeName(pos, pkg, name, rhs)
}

// ConditionallyEnableGoTypesAlias enables the gotypesalias GODEBUG setting if
// * the version of go used to compile the program is between 1.23 and 1.26,
// * gotypesalias are not already enabled, and
// * gotypesalias is not set in GODEBUG already
// exactly once. Otherwise it does nothing.
//
// Recommended usage is to do the following within a main package:
//
//	func init() { ConditionallyEnableGoTypesAlias() }
//
// within a module with go version 1.22 or in GOPATH mode.
func ConditionallyEnableGoTypesAlias() { conditionallyEnableGoTypesAliasOnce() }

var conditionallyEnableGoTypesAliasOnce = sync.OnceFunc(conditionallyEnableGoTypesAlias)

func conditionallyEnableGoTypesAlias() {
	// Let R be the version of go the program was compiled with. Then
	// if R < 1.22, do nothing as gotypesalias is meaningless,
	// if R == 1.22, do not turn on gotypesalias (latent bugs),
	// if 1.23 <= R && R <= 1.26, turn on gotypesalias, and
	// if R >= 1.27, this is a guaranteed no-op.

	if !slices.Contains(build.Default.ReleaseTags, "go1.23") {
		return // R < 1.23 (do nothing)
	}
	if slices.Contains(build.Default.ReleaseTags, "go1.27") {
		return // R >= 1.27 (do nothing)
	}
	// 1.23 <= R <= 1.26
	_, anyIsAlias := types.Universe.Lookup("any").Type().(*types.Alias)
	if anyIsAlias {
		return // gotypesalias are already enabled.
	}

	// Does GODEBUG have gotypesalias set already?
	godebugs := strings.Split(os.Getenv("GODEBUG"), ",")
	for _, p := range godebugs {
		if strings.HasPrefix(strings.TrimSpace(p), "gotypesalias=") {
			// gotypesalias is set in GODEBUG already.
			// Do not override this setting.
			return
		}
	}

	// Enable gotypesalias.
	godebugs = append(godebugs, "gotypesalias=1")
	os.Setenv("GODEBUG", strings.Join(godebugs, ","))
}
