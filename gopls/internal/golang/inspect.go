// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/internal/event"
)

// Inspect returns structured information about a definition location.
func Inspect(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, args command.InspectArgs) (command.InspectResult, error) {
	ctx, done := event.Start(ctx, "golang.Inspect")
	defer done()

	rng, hover, err := hover(ctx, snapshot, fh, args.Location.Range, false)
	if err != nil {
		return command.InspectResult{}, err
	}
	if rng.Empty() || hover == nil {
		return command.InspectResult{}, fmt.Errorf("no hover result found at location: %v", args.Location.Range)
	}

	var fullDocumentation string
	if args.Markdown {
		fullDocumentation = DocCommentToMarkdown(hover.FullDocumentation, snapshot.Options())
	} else {
		fullDocumentation = hover.FullDocumentation
	}
	result := command.InspectResult{
		Synopsis:          hover.Synopsis,
		FullDocumentation: fullDocumentation,
		Signature:         hover.Signature,
	}
	if args.IncludeSource {
		if err := addSrcs(ctx, snapshot, fh, args.Location, hover, &result); err != nil {
			return command.InspectResult{}, err
		}
	}
	return result, nil
}

func addSrcs(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, loc protocol.Location, hover *hoverResult, ret *command.InspectResult) error {
	// First add the items we got automatically from hoverResult
	ret.Methods = hover.methods
	ret.PromotedFields = hover.promotedFields

	// Should be a cache hit since hover already calls this.
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return err
	}
	pos, _, err := pgf.RangePos(loc.Range)
	if err != nil {
		return err
	}
	decl, spec, field, assign := findDeclInfo(pgf, pos)
	var node ast.Node
	// Choose the most specific, as `findDeclInfo` returns the real `decl` in
	// the inner most Node type, but also returns the enclosing decls.
	switch {
	case field != nil:
		node = field
	case spec != nil:
		// Only use spec if the gen decl is contained in parentheses.
		if gen, _ := decl.(*ast.GenDecl); gen != nil && gen.Lparen.IsValid() {
			node = spec
		} else {
			node = decl
		}
	case decl != nil:
		node = decl
	case assign != nil:
		node = assign
	}
	if node != nil {
		declText, err := pgf.NodeText(node)
		if err != nil {
			return err
		}
		ret.Declaration = string(declText)
	}
	return nil
}
