// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/test/integration"
)

func TestPackageSymbols(t *testing.T) {
	const files = `
-- a.go --
package a

var A = "var"
type S struct{}

func (s *S) M1() {}
-- b.go --
package a

var b = 1

func (s *S) M2() {}

func (s *S) M3() {}

func F() {}
`
	integration.Run(t, files, func(t *testing.T, env *integration.Env) {
		a_uri := env.Sandbox.Workdir.URI("a.go")
		b_uri := env.Sandbox.Workdir.URI("b.go")
		args, err := command.MarshalArgs(command.PackageSymbolsArgs{
			URI: a_uri,
		})
		if err != nil {
			t.Fatalf("failed to MarshalArgs: %v", err)
		}

		var res command.PackageSymbolsResult
		env.ExecuteCommand(&protocol.ExecuteCommandParams{
			Command:   "gopls.package_symbols",
			Arguments: args,
		}, &res)

		want := command.PackageSymbolsResult{
			PackageName: "a",
			Files:       []protocol.DocumentURI{a_uri, b_uri},
			Symbols: []command.PackageSymbol{
				{
					Name: "A",
					Kind: protocol.Variable,
					File: 0,
				},
				{
					Name: "F",
					Kind: protocol.Function,
					File: 1,
				},
				{
					Name: "S",
					Kind: protocol.Struct,
					File: 0,
					Children: []command.PackageSymbol{
						{
							Name: "M1",
							Kind: protocol.Method,
							File: 0,
						},
						{
							Name: "M2",
							Kind: protocol.Method,
							File: 1,
						},
						{
							Name: "M3",
							Kind: protocol.Method,
							File: 1,
						},
					},
				},
				{
					Name: "b",
					Kind: protocol.Variable,
					File: 1,
				},
			},
		}
		if diff := cmp.Diff(want, res, cmpopts.IgnoreFields(command.PackageSymbol{}, "Range", "SelectionRange", "Detail")); diff != "" {
			t.Errorf("gopls.package_symbols returned unexpected diff (-want +got):\n%s", diff)
		}
	})
}
