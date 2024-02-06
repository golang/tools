// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mod

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
)

// LensFuncs returns the supported lensFuncs for go.mod files.
func LensFuncs() map[command.Command]golang.LensFunc {
	return map[command.Command]golang.LensFunc{
		command.UpgradeDependency: upgradeLenses,
		command.Tidy:              tidyLens,
		command.Vendor:            vendorLens,
		command.RunGovulncheck:    vulncheckLenses,
	}
}

func upgradeLenses(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.CodeLens, error) {
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil || pm.File == nil {
		return nil, err
	}
	uri := fh.URI()
	reset, err := command.NewResetGoModDiagnosticsCommand("Reset go.mod diagnostics", command.ResetGoModDiagnosticsArgs{URIArg: command.URIArg{URI: uri}})
	if err != nil {
		return nil, err
	}
	// Put the `Reset go.mod diagnostics` codelens on the module statement.
	modrng, err := moduleStmtRange(fh, pm)
	if err != nil {
		return nil, err
	}
	lenses := []protocol.CodeLens{{Range: modrng, Command: &reset}}
	if len(pm.File.Require) == 0 {
		// Nothing to upgrade.
		return lenses, nil
	}
	var requires []string
	for _, req := range pm.File.Require {
		requires = append(requires, req.Mod.Path)
	}
	checkUpgrade, err := command.NewCheckUpgradesCommand("Check for upgrades", command.CheckUpgradesArgs{
		URI:     uri,
		Modules: requires,
	})
	if err != nil {
		return nil, err
	}
	upgradeTransitive, err := command.NewUpgradeDependencyCommand("Upgrade transitive dependencies", command.DependencyArgs{
		URI:        uri,
		AddRequire: false,
		GoCmdArgs:  []string{"-d", "-u", "-t", "./..."},
	})
	if err != nil {
		return nil, err
	}
	upgradeDirect, err := command.NewUpgradeDependencyCommand("Upgrade direct dependencies", command.DependencyArgs{
		URI:        uri,
		AddRequire: false,
		GoCmdArgs:  append([]string{"-d"}, requires...),
	})
	if err != nil {
		return nil, err
	}

	// Put the upgrade code lenses above the first require block or statement.
	rng, err := firstRequireRange(fh, pm)
	if err != nil {
		return nil, err
	}

	return append(lenses, []protocol.CodeLens{
		{Range: rng, Command: &checkUpgrade},
		{Range: rng, Command: &upgradeTransitive},
		{Range: rng, Command: &upgradeDirect},
	}...), nil
}

func tidyLens(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.CodeLens, error) {
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil || pm.File == nil {
		return nil, err
	}
	uri := fh.URI()
	cmd, err := command.NewTidyCommand("Run go mod tidy", command.URIArgs{URIs: []protocol.DocumentURI{uri}})
	if err != nil {
		return nil, err
	}
	rng, err := moduleStmtRange(fh, pm)
	if err != nil {
		return nil, err
	}
	return []protocol.CodeLens{{
		Range:   rng,
		Command: &cmd,
	}}, nil
}

func vendorLens(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.CodeLens, error) {
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil || pm.File == nil {
		return nil, err
	}
	if len(pm.File.Require) == 0 {
		// Nothing to vendor.
		return nil, nil
	}
	rng, err := moduleStmtRange(fh, pm)
	if err != nil {
		return nil, err
	}
	title := "Create vendor directory"
	uri := fh.URI()
	cmd, err := command.NewVendorCommand(title, command.URIArg{URI: uri})
	if err != nil {
		return nil, err
	}
	// Change the message depending on whether or not the module already has a
	// vendor directory.
	vendorDir := filepath.Join(filepath.Dir(fh.URI().Path()), "vendor")
	if info, _ := os.Stat(vendorDir); info != nil && info.IsDir() {
		title = "Sync vendor directory"
	}
	return []protocol.CodeLens{{Range: rng, Command: &cmd}}, nil
}

func moduleStmtRange(fh file.Handle, pm *cache.ParsedModule) (protocol.Range, error) {
	if pm.File == nil || pm.File.Module == nil || pm.File.Module.Syntax == nil {
		return protocol.Range{}, fmt.Errorf("no module statement in %s", fh.URI())
	}
	syntax := pm.File.Module.Syntax
	return pm.Mapper.OffsetRange(syntax.Start.Byte, syntax.End.Byte)
}

// firstRequireRange returns the range for the first "require" in the given
// go.mod file. This is either a require block or an individual require line.
func firstRequireRange(fh file.Handle, pm *cache.ParsedModule) (protocol.Range, error) {
	if len(pm.File.Require) == 0 {
		return protocol.Range{}, fmt.Errorf("no requires in the file %s", fh.URI())
	}
	var start, end modfile.Position
	for _, stmt := range pm.File.Syntax.Stmt {
		if b, ok := stmt.(*modfile.LineBlock); ok && len(b.Token) == 1 && b.Token[0] == "require" {
			start, end = b.Span()
			break
		}
	}

	firstRequire := pm.File.Require[0].Syntax
	if start.Byte == 0 || firstRequire.Start.Byte < start.Byte {
		start, end = firstRequire.Start, firstRequire.End
	}
	return pm.Mapper.OffsetRange(start.Byte, end.Byte)
}

func vulncheckLenses(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]protocol.CodeLens, error) {
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil || pm.File == nil {
		return nil, err
	}
	// Place the codelenses near the module statement.
	// A module may not have the require block,
	// but vulnerabilities can exist in standard libraries.
	uri := fh.URI()
	rng, err := moduleStmtRange(fh, pm)
	if err != nil {
		return nil, err
	}

	vulncheck, err := command.NewRunGovulncheckCommand("Run govulncheck", command.VulncheckArgs{
		URI:     uri,
		Pattern: "./...",
	})
	if err != nil {
		return nil, err
	}
	return []protocol.CodeLens{
		{Range: rng, Command: &vulncheck},
	}, nil
}
