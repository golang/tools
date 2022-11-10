// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mod

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/gopls/internal/govulncheck"
	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/internal/event"
)

func Hover(ctx context.Context, snapshot source.Snapshot, fh source.FileHandle, position protocol.Position) (*protocol.Hover, error) {
	var found bool
	for _, uri := range snapshot.ModFiles() {
		if fh.URI() == uri {
			found = true
			break
		}
	}

	// We only provide hover information for the view's go.mod files.
	if !found {
		return nil, nil
	}

	ctx, done := event.Start(ctx, "mod.Hover")
	defer done()

	// Get the position of the cursor.
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil {
		return nil, fmt.Errorf("getting modfile handle: %w", err)
	}
	offset, err := pm.Mapper.Offset(position)
	if err != nil {
		return nil, fmt.Errorf("computing cursor position: %w", err)
	}

	// Confirm that the cursor is at the position of a require statement.
	var req *modfile.Require
	var startPos, endPos int
	for _, r := range pm.File.Require {
		dep := []byte(r.Mod.Path)
		s, e := r.Syntax.Start.Byte, r.Syntax.End.Byte
		i := bytes.Index(pm.Mapper.Content[s:e], dep)
		if i == -1 {
			continue
		}
		// Shift the start position to the location of the
		// dependency within the require statement.
		startPos, endPos = s+i, e
		if startPos <= offset && offset <= endPos {
			req = r
			break
		}
	}
	// TODO(hyangah): find position for info about vulnerabilities in Go

	// The cursor position is not on a require statement.
	if req == nil {
		return nil, nil
	}

	// Get the vulnerability info.
	affecting, nonaffecting := lookupVulns(snapshot.View().Vulnerabilities(fh.URI()), req)

	// Get the `go mod why` results for the given file.
	why, err := snapshot.ModWhy(ctx, fh)
	if err != nil {
		return nil, err
	}
	explanation, ok := why[req.Mod.Path]
	if !ok {
		return nil, nil
	}

	// Get the range to highlight for the hover.
	// TODO(hyangah): adjust the hover range to include the version number
	// to match the diagnostics' range.
	rng, err := pm.Mapper.OffsetRange(startPos, endPos)
	if err != nil {
		return nil, err
	}
	options := snapshot.View().Options()
	isPrivate := snapshot.View().IsGoPrivatePath(req.Mod.Path)
	header := formatHeader(req.Mod.Path, options)
	explanation = formatExplanation(explanation, req, options, isPrivate)
	vulns := formatVulnerabilities(affecting, nonaffecting, options)

	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  options.PreferredContentFormat,
			Value: header + vulns + explanation,
		},
		Range: rng,
	}, nil
}

func formatHeader(modpath string, options *source.Options) string {
	var b strings.Builder
	// Write the heading as an H3.
	b.WriteString("#### " + modpath)
	if options.PreferredContentFormat == protocol.Markdown {
		b.WriteString("\n\n")
	} else {
		b.WriteRune('\n')
	}
	return b.String()
}

func compareVuln(i, j govulncheck.Vuln) bool {
	if i.OSV.ID == j.OSV.ID {
		return i.PkgPath < j.PkgPath
	}
	return i.OSV.ID < j.OSV.ID
}

func lookupVulns(vulns []govulncheck.Vuln, req *modfile.Require) (affecting, nonaffecting []govulncheck.Vuln) {
	modpath, modversion := req.Mod.Path, req.Mod.Version

	var info, warning []govulncheck.Vuln
	for _, vuln := range vulns {
		if vuln.ModPath != modpath || vuln.FoundIn != modversion {
			continue
		}
		if len(vuln.Trace) == 0 {
			info = append(info, vuln)
		} else {
			warning = append(warning, vuln)
		}
	}
	sort.Slice(info, func(i, j int) bool { return compareVuln(info[i], info[j]) })
	sort.Slice(warning, func(i, j int) bool { return compareVuln(warning[i], warning[j]) })
	return warning, info
}

func formatVulnerabilities(affecting, nonaffecting []govulncheck.Vuln, options *source.Options) string {
	if len(affecting) == 0 && len(nonaffecting) == 0 {
		return ""
	}

	// TODO(hyangah): can we use go templates to generate hover messages?
	// Then, we can use a different template for markdown case.
	useMarkdown := options.PreferredContentFormat == protocol.Markdown

	var b strings.Builder

	if len(affecting) > 0 {
		// TODO(hyangah): make the message more eyecatching (icon/codicon/color)
		if len(affecting) == 1 {
			b.WriteString(fmt.Sprintf("\n**WARNING:** Found %d reachable vulnerability.\n", len(affecting)))
		} else {
			b.WriteString(fmt.Sprintf("\n**WARNING:** Found %d reachable vulnerabilities.\n", len(affecting)))
		}
	}
	for _, v := range affecting {
		fix := "No fix is available."
		if v.FixedIn != "" {
			fix = "Fixed in " + v.FixedIn + "."
		}

		if useMarkdown {
			fmt.Fprintf(&b, "- [**%v**](%v) %v %v\n", v.OSV.ID, href(v.OSV), formatMessage(v), fix)
		} else {
			fmt.Fprintf(&b, "  - [%v] %v (%v) %v\n", v.OSV.ID, formatMessage(v), href(v.OSV), fix)
		}
	}
	if len(nonaffecting) > 0 {
		fmt.Fprintf(&b, "\n**FYI:** The project imports packages with known vulnerabilities, but does not call the vulnerable code.\n")
	}
	for _, v := range nonaffecting {
		fix := "No fix is available."
		if v.FixedIn != "" {
			fix = "Fixed in " + v.FixedIn + "."
		}
		if useMarkdown {
			fmt.Fprintf(&b, "- [%v](%v) %v %v\n", v.OSV.ID, href(v.OSV), formatMessage(v), fix)
		} else {
			fmt.Fprintf(&b, "  - [%v] %v %v (%v)\n", v.OSV.ID, formatMessage(v), fix, href(v.OSV))
		}
	}
	b.WriteString("\n")
	return b.String()
}

func formatExplanation(text string, req *modfile.Require, options *source.Options, isPrivate bool) string {
	text = strings.TrimSuffix(text, "\n")
	splt := strings.Split(text, "\n")
	length := len(splt)

	var b strings.Builder

	// If the explanation is 2 lines, then it is of the form:
	// # golang.org/x/text/encoding
	// (main module does not need package golang.org/x/text/encoding)
	if length == 2 {
		b.WriteString(splt[1])
		return b.String()
	}

	imp := splt[length-1] // import path
	reference := imp
	// See golang/go#36998: don't link to modules matching GOPRIVATE.
	if !isPrivate && options.PreferredContentFormat == protocol.Markdown {
		target := imp
		if strings.ToLower(options.LinkTarget) == "pkg.go.dev" {
			target = strings.Replace(target, req.Mod.Path, req.Mod.String(), 1)
		}
		reference = fmt.Sprintf("[%s](%s)", imp, source.BuildLink(options.LinkTarget, target, ""))
	}
	b.WriteString("This module is necessary because " + reference + " is imported in")

	// If the explanation is 3 lines, then it is of the form:
	// # golang.org/x/tools
	// modtest
	// golang.org/x/tools/go/packages
	if length == 3 {
		msg := fmt.Sprintf(" `%s`.", splt[1])
		b.WriteString(msg)
		return b.String()
	}

	// If the explanation is more than 3 lines, then it is of the form:
	// # golang.org/x/text/language
	// rsc.io/quote
	// rsc.io/sampler
	// golang.org/x/text/language
	b.WriteString(":\n```text")
	dash := ""
	for _, imp := range splt[1 : length-1] {
		dash += "-"
		b.WriteString("\n" + dash + " " + imp)
	}
	b.WriteString("\n```")
	return b.String()
}
