// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/protocol"
	protocolcommand "golang.org/x/tools/gopls/internal/protocol/command"
)

// TargetQueryFlags defines the set of flags for subcommands that take a target query param
type TargetQueryFlags struct {
	Pkg string `flag:"pkg" help:"scopes the query to a specific package, resolving ambiguities in versioned package paths (for example, gopkg.in/yaml.v3) or vanity domains."`
	Pos string `flag:"pos" help:"targets a physical coordinate range (line and column are optional)"`
}

type target struct {
	pkg  string
	name string
	loc  protocol.Location
}

func (t *target) qualifiedName() string {
	return fmt.Sprintf("%s.%s", t.pkg, t.name)
}

func (t *target) span(ctx context.Context, cli *client) (span, error) {
	file, err := cli.openFile(ctx, t.loc.URI)
	if err != nil {
		return span{}, err
	}
	return file.locationSpan(t.loc)
}

// resolveSingleTarget calls gopls.resolve_target and validates that only a single target was found.
func resolveSingleTarget(ctx context.Context, cli *client, flags TargetQueryFlags, dir, targetQuery string) (target, error) {
	targets, err := resolveTarget(ctx, cli, flags, dir, targetQuery)
	if err != nil {
		return target{}, err
	}
	switch len(targets) {
	case 0:
		return target{}, fmt.Errorf("no targets found for query: %s", targetQuery)
	case 1:
		return targets[0], nil
	default:
		return target{}, fmt.Errorf("more than one target found for query %s: %v", targetQuery, targets)
	}
}

// resolveTarget calls gopls.resolve_target
func resolveTarget(ctx context.Context, cli *client, flags TargetQueryFlags, dir, targetQuery string) ([]target, error) {

	var (
		searchScope = protocol.URIFromPath(dir) // default to the passed in directory
		searchRange protocol.Range
	)
	if len(flags.Pos) > 0 {
		posSpan := parseSpan(flags.Pos)
		if posSpan.IsValid() {
			searchScope = posSpan.URI() // restrict scope to span's file.
			file, err := cli.openFile(ctx, posSpan.URI())
			if err != nil {
				return nil, err
			}
			searchRange, err = file.spanRange(posSpan)
			if err != nil {
				return nil, err
			}
		}
	}

	params := protocolcommand.ResolveTargetParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: searchScope},
		PkgScope:     flags.Pkg,
		Range:        searchRange,
		Target:       targetQuery,
	}
	cmd := protocolcommand.NewResolveTargetCommand("resolve target", params)
	res, err := executeCommand(ctx, cli.server, cmd)
	if err != nil {
		return nil, err
	}
	resolveTargetResult, ok := res.(protocolcommand.ResolveTargetResult)
	if !ok {
		return nil, fmt.Errorf("%T can't convert to command.ResolveTargetResult", res)
	}
	targets := make([]target, len(resolveTargetResult.Matches))
	for i, m := range resolveTargetResult.Matches {
		targets[i] = target{
			pkg:  m.Package,
			name: m.Name,
			loc:  m.Location,
		}
	}
	return targets, nil
}
