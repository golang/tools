//go:build ignore
// +build ignore

package main

import (
	"golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg"
)

func use(interface{})

// Test of interface calls.

func main() {
	use(subpkg.A(0))
	use(new(subpkg.B))
	use(subpkg.B2(0))

	var i interface {
		F()
	}

	// assign an interface type with a function return interface value
	i = subpkg.NewInterfaceF()

	i.F()
}

func dead() {
	use(subpkg.D(0))
}

// WANT:
//
// edge (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.A).F --static method call--> (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.A).F
// edge (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B2).F --static method call--> (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B2).F
// edge (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.C).F --static method call--> (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.C).F
// edge init --static function call--> golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.init
// edge main --dynamic method call--> (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.A).F
// edge main --dynamic method call--> (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B).F
// edge main --dynamic method call--> (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B2).F
// edge main --dynamic method call--> (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.C).F
// edge main --dynamic method call--> (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.A).F
// edge main --dynamic method call--> (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B2).F
// edge main --dynamic method call--> (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.C).F
// edge main --static function call--> golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.NewInterfaceF
// edge main --static function call--> use
//
// reachable (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.A).F
// reachable (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B).F
// reachable (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B2).F
// reachable (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.C).F
// reachable (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.A).F
// !reachable (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B).F
// reachable (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B2).F
// reachable (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.C).F
// reachable golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.NewInterfaceF
// reachable golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.init
// !reachable (*golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.D).F
// !reachable (golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.D).F
// reachable init
// reachable main
// reachable use
//
// rtype *golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.A
// rtype *golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B
// rtype *golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B2
// rtype *golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.C
// rtype golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B
// rtype golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.A
// rtype golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.B2
// rtype golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.C
// !rtype golang.org/x/tools/go/callgraph/rta/testdata/pkg/subpkg.D
