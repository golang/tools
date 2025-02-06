// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/test/compare"
	"golang.org/x/tools/gopls/internal/test/integration"
)

func TestModifyTags(t *testing.T) {
	const files = `
-- go.mod --
module example.com

go 1.20

-- a.go --
package a

type A struct {
	B string
	C int
	D bool
	E string
}

-- b.go --
package b

type B struct {
	B string ` + "`json:\"b,omitempty\"`" + `
	C int    ` + "`json:\"c,omitempty\"`" + `
	D bool   ` + "`json:\"d,omitempty\"`" + `
	E string ` + "`json:\"e,omitempty\"`" + `
}

-- c.go --
package c

type C struct {
	B string
	C int
	D bool ` + "`json:\"d,omitempty\"`" + `
	E string
}
`

	const wantAddTagsEntireStruct = `package a

type A struct {
	B string ` + "`json:\"b,omitempty\"`" + `
	C int    ` + "`json:\"c,omitempty\"`" + `
	D bool   ` + "`json:\"d,omitempty\"`" + `
	E string ` + "`json:\"e,omitempty\"`" + `
}
`

	const wantRemoveTags = `package b

type B struct {
	B string
	C int
	D bool   ` + "`json:\"d,omitempty\"`" + `
	E string ` + "`json:\"e,omitempty\"`" + `
}
`

	const wantAddTagsSingleLine = `package a

type A struct {
	B string
	C int
	D bool ` + "`json:\"d,omitempty\"`" + `
	E string
}
`

	const wantRemoveOptions = `package c

type C struct {
	B string
	C int
	D bool ` + "`json:\"d\"`" + `
	E string
}
`

	tests := []struct {
		file string
		args command.ModifyTagsArgs
		want string
	}{
		{file: "a.go", args: command.ModifyTagsArgs{
			Range: protocol.Range{
				Start: protocol.Position{Line: 2, Character: 0},
				End:   protocol.Position{Line: 8, Character: 0},
			},
			Add:        "json",
			AddOptions: "json=omitempty",
		}, want: wantAddTagsEntireStruct},
		{file: "b.go", args: command.ModifyTagsArgs{
			Range: protocol.Range{
				Start: protocol.Position{Line: 3, Character: 2},
				End:   protocol.Position{Line: 4, Character: 6},
			},
			Remove: "json",
		}, want: wantRemoveTags},
		{file: "a.go", args: command.ModifyTagsArgs{
			Range: protocol.Range{
				Start: protocol.Position{Line: 5, Character: 0},
				End:   protocol.Position{Line: 5, Character: 7},
			},
			Add:        "json",
			AddOptions: "json=omitempty",
		}, want: wantAddTagsSingleLine},
		{file: "c.go", args: command.ModifyTagsArgs{
			Range: protocol.Range{
				Start: protocol.Position{Line: 3, Character: 0},
				End:   protocol.Position{Line: 7, Character: 0},
			},
			RemoveOptions: "json=omitempty",
		}, want: wantRemoveOptions},
	}

	for _, test := range tests {
		integration.Run(t, files, func(t *testing.T, env *integration.Env) {
			uri := env.Sandbox.Workdir.URI(test.file)
			args, err := command.MarshalArgs(
				command.ModifyTagsArgs{
					URI:           uri,
					Range:         test.args.Range,
					Add:           test.args.Add,
					AddOptions:    test.args.AddOptions,
					Remove:        test.args.Remove,
					RemoveOptions: test.args.RemoveOptions,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			var res any
			env.ExecuteCommand(&protocol.ExecuteCommandParams{
				Command:   command.ModifyTags.String(),
				Arguments: args,
			}, &res)
			// Wait until we finish writing to the file.
			env.AfterChange()
			if got := env.BufferText(test.file); got != test.want {
				t.Errorf("modify_tags returned unexpected diff (-want +got):\n%s", compare.Text(test.want, got))
			}
		})
	}
}
