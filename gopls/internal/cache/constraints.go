// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"iter"
	"slices"
)

// isStandaloneFile reports whether a file with the given contents should be
// considered a 'standalone main file', meaning a package that consists of only
// a single file.
func isStandaloneFile(src []byte, standaloneTags []string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		return false
	}

	if f.Name == nil || f.Name.Name != "main" {
		return false
	}

	// Extract the file's effective build constraint expression.
	var expr constraint.Expr
	for expr = range buildConstraints(f) {
		break
	}

	// value is a three-valued domain: false, unknown, or true.
	type value int8
	const (
		falseValue value = -1
		unknown    value = 0
		trueValue  value = 1
	)
	var eval func(constraint.Expr, value) value
	eval = func(expr constraint.Expr, standaloneValue value) value {
		switch expr := expr.(type) {
		case *constraint.TagExpr:
			if slices.Contains(standaloneTags, expr.Tag) {
				return standaloneValue
			}
			return unknown
		case *constraint.NotExpr:
			return -eval(expr.X, standaloneValue)
		case *constraint.AndExpr:
			return min(eval(expr.X, standaloneValue), eval(expr.Y, standaloneValue))
		case *constraint.OrExpr:
			return max(eval(expr.X, standaloneValue), eval(expr.Y, standaloneValue))
		default:
			panic("unexpected constraint expression type")
		}
	}
	// A file is standalone if its build constraints are
	// - false when the standalone tags are false, and
	// - possible to satisfy when the standalone tags are true.
	return expr != nil &&
		eval(expr, falseValue) == falseValue &&
		eval(expr, trueValue) != falseValue
}

// buildConstraints returns the file's effective build constraint expression.
func buildConstraints(file *ast.File) iter.Seq[constraint.Expr] {
	return func(yield func(constraint.Expr) bool) {
		var goBuild, plusBuild constraint.Expr
		for _, cg := range file.Comments {
			// Even with PackageClauseOnly the parser consumes the semicolon following
			// the package clause, so we must guard against comments that come after
			// the package name.
			if cg.Pos() > file.Name.Pos() {
				continue
			}
			for _, comment := range cg.List {
				if c, err := constraint.Parse(comment.Text); err == nil {
					if constraint.IsPlusBuild(comment.Text) {
						if plusBuild == nil {
							plusBuild = c
						} else {
							plusBuild = &constraint.AndExpr{X: plusBuild, Y: c}
						}
					} else {
						goBuild = c
					}
				}
			}
		}
		expr := goBuild
		if expr == nil {
			expr = plusBuild
		}
		if expr != nil {
			yield(expr)
		}
	}
}
