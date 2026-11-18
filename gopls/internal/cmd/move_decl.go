// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/tools/gopls/internal/protocol"
	protocolcommand "golang.org/x/tools/gopls/internal/protocol/command"
)

// moveDecl implements the move_decl verb for gopls.
type moveDecl struct {
	TargetQueryFlags
	app *application
}

func (m *moveDecl) Name() string   { return "move_decl" }
func (m *moveDecl) Parent() string { return m.app.Name() }
func (m *moveDecl) Usage() string  { return "[move_decl-flags] <target> <file>" }
func (m *moveDecl) ShortHelp() string {
	return "move the target's declaration to another file"
}
func (m *moveDecl) DetailedHelp(f *flag.FlagSet) {
	fmt.Fprint(f.Output(), `
Moves the declaration of the target (a top-level func, type, method, var, or const)
to the specified file, along with any dependent declarations that must move with
it, and updates references and imports accordingly. The destination file and its
package are created if they do not exist. Unlike other commands that
edit files, move_decl does not support the -write, -diff, and -list
flags; it always writes the edited files directly.

Examples:
	$ gopls move_decl example.com/a.Foo b/b.go
	$ gopls move_decl -pkg example.com/a Foo b/b.go

move_decl-flags:
`)
	printFlagDefaults(f)
}

// Run moves the target declaration, updating the file(s) in place.
func (m *moveDecl) Run(ctx context.Context, args ...string) error {
	if len(args) != 2 {
		return commandLineErrorf("move_decl expects 2 arguments (move target, destination file)")
	}
	m.app.editFlags = &EditFlags{Write: true}

	cli, _, err := m.app.connect(ctx)
	if err != nil {
		return err
	}
	defer cli.terminate(ctx)

	root, err := os.Getwd() // resolve relative to the user's current dir
	if err != nil {
		return fmt.Errorf("finding workdir: %v", err)
	}
	target, err := resolveSingleTarget(ctx, cli, m.TargetQueryFlags, root, args[0])
	if err != nil {
		return err
	}
	dest, err := filepath.Abs(args[1])
	if err != nil {
		return fmt.Errorf("invalid destination file %q: %v", args[1], err)
	}

	cmd := protocolcommand.NewMoveDeclarationCommand("move declaration", protocolcommand.MoveDeclarationArgs{
		Location:    target.loc,
		Destination: protocol.URIFromPath(dest),
	})
	if _, err := executeCommand(ctx, cli.server, cmd); err != nil {
		return fmt.Errorf("moving %s: %v", target.qualifiedName(), err)
	}
	return nil
}
