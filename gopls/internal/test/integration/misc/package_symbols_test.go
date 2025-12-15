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
-- go.mod --
module example.com

go 1.20

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
-- unloaded.go --
//go:build unloaded

package a

var Unloaded int
`
	integration.Run(t, files, func(t *testing.T, env *integration.Env) {
		aURI := env.Sandbox.Workdir.URI("a.go")
		bURI := env.Sandbox.Workdir.URI("b.go")
		args, err := command.MarshalArgs(command.PackageSymbolsArgs{
			URI: aURI,
		})
		if err != nil {
			t.Fatal(err)
		}

		var res command.PackageSymbolsResult
		env.ExecuteCommand(&protocol.ExecuteCommandParams{
			Command:   command.PackageSymbols.String(),
			Arguments: args,
		}, &res)

		want := command.PackageSymbolsResult{
			PackageName: "a",
			Files:       []protocol.DocumentURI{aURI, bURI},
			Symbols: []command.PackageSymbol{
				{Name: "A", Kind: protocol.Variable, File: 0},
				{Name: "F", Kind: protocol.Function, File: 1},
				{Name: "S", Kind: protocol.Struct, File: 0, Children: []command.PackageSymbol{
					{Name: "M1", Kind: protocol.Method, File: 0},
					{Name: "M2", Kind: protocol.Method, File: 1},
					{Name: "M3", Kind: protocol.Method, File: 1},
				}},
				{Name: "b", Kind: protocol.Variable, File: 1},
			},
		}
		ignore := cmpopts.IgnoreFields(command.PackageSymbol{}, "Range", "SelectionRange", "Detail")
		if diff := cmp.Diff(want, res, ignore); diff != "" {
			t.Errorf("package_symbols returned unexpected diff (-want +got):\n%s", diff)
		}

		for file, want := range map[string]command.PackageSymbolsResult{
			"go.mod": {},
			"unloaded.go": {
				PackageName: "a",
				Files:       []protocol.DocumentURI{env.Sandbox.Workdir.URI("unloaded.go")},
				Symbols: []command.PackageSymbol{
					{Name: "Unloaded", Kind: protocol.Variable, File: 0},
				},
			},
		} {
			uri := env.Sandbox.Workdir.URI(file)
			args, err := command.MarshalArgs(command.PackageSymbolsArgs{
				URI: uri,
			})
			if err != nil {
				t.Fatal(err)
			}
			var res command.PackageSymbolsResult
			env.ExecuteCommand(&protocol.ExecuteCommandParams{
				Command:   command.PackageSymbols.String(),
				Arguments: args,
			}, &res)

			if diff := cmp.Diff(want, res, ignore); diff != "" {
				t.Errorf("package_symbols returned unexpected diff (-want +got):\n%s", diff)
			}
		}
	})
}
