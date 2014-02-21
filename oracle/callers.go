// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package oracle

import (
	"fmt"
	"go/token"

	"code.google.com/p/go.tools/go/callgraph"
	"code.google.com/p/go.tools/go/ssa"
	"code.google.com/p/go.tools/oracle/serial"
)

// Callers reports the possible callers of the function
// immediately enclosing the specified source location.
//
func callers(o *Oracle, qpos *QueryPos) (queryResult, error) {
	pkg := o.prog.Package(qpos.info.Pkg)
	if pkg == nil {
		return nil, fmt.Errorf("no SSA package")
	}
	if !ssa.HasEnclosingFunction(pkg, qpos.path) {
		return nil, fmt.Errorf("this position is not inside a function")
	}

	buildSSA(o)

	target := ssa.EnclosingFunction(pkg, qpos.path)
	if target == nil {
		return nil, fmt.Errorf("no SSA function built for this location (dead code?)")
	}

	// Run the pointer analysis, recording each
	// call found to originate from target.
	o.ptaConfig.BuildCallGraph = true
	cg := ptrAnalysis(o).CallGraph
	cg.DeleteSyntheticNodes()
	edges := cg.CreateNode(target).In
	// TODO(adonovan): sort + dedup calls to ensure test determinism.

	return &callersResult{
		target:    target,
		callgraph: cg,
		edges:     edges,
	}, nil
}

type callersResult struct {
	target    *ssa.Function
	callgraph *callgraph.Graph
	edges     []*callgraph.Edge
}

func (r *callersResult) display(printf printfFunc) {
	root := r.callgraph.Root
	if r.edges == nil {
		printf(r.target, "%s is not reachable in this program.", r.target)
	} else {
		printf(r.target, "%s is called from these %d sites:", r.target, len(r.edges))
		for _, edge := range r.edges {
			if edge.Caller == root {
				printf(r.target, "the root of the call graph")
			} else {
				printf(edge.Site, "\t%s from %s", edge.Site.Common().Description(), edge.Caller.Func)
			}
		}
	}
}

func (r *callersResult) toSerial(res *serial.Result, fset *token.FileSet) {
	root := r.callgraph.Root
	var callers []serial.Caller
	for _, edge := range r.edges {
		var c serial.Caller
		c.Caller = edge.Caller.Func.String()
		if edge.Caller == root {
			c.Desc = "synthetic call"
		} else {
			c.Pos = fset.Position(edge.Site.Pos()).String()
			c.Desc = edge.Site.Common().Description()
		}
		callers = append(callers, c)
	}
	res.Callers = callers
}
