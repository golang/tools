// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/tools/gopls/internal/vulncheck/scan"
)

type vulncheckParams struct {
	Dir     string `json:"dir,omitempty" jsonschema:"directory to run the vulnerability check within"`
	Pattern string `json:"pattern,omitempty" jsonschema:"package pattern to check"`
}

type GroupedVulnFinding struct {
	ID               string   `json:"id"`
	Details          string   `json:"details"`
	AffectedPackages []string `json:"affectedPackages"`
}

type VulncheckResultOutput struct {
	Findings []GroupedVulnFinding `json:"findings,omitempty"`
	Logs     string               `json:"logs,omitempty"`
}

func (h *handler) vulncheckHandler(ctx context.Context, req *mcp.CallToolRequest, params *vulncheckParams) (*mcp.CallToolResult, *VulncheckResultOutput, error) {
	countGoVulncheckMCP.Inc()
	snapshot, release, err := h.snapshot()
	if err != nil {
		return nil, nil, err
	}
	defer release()

	dir := params.Dir
	if dir == "" && len(h.session.Views()) > 0 {
		dir = h.session.Views()[0].Root().Path()
	}

	pattern := params.Pattern
	if pattern == "" {
		pattern = "./..."
	}

	var logBuf bytes.Buffer
	result, err := scan.RunGovulncheck(ctx, pattern, snapshot, dir, &logBuf)
	if err != nil {
		return nil, nil, fmt.Errorf("running govulncheck failed: %v\nLogs:\n%s", err, logBuf.String())
	}

	groupedPkgs := make(map[string]map[string]struct{})
	for _, finding := range result.Findings {
		if osv := result.Entries[finding.OSV]; osv != nil {
			if _, ok := groupedPkgs[osv.ID]; !ok {
				groupedPkgs[osv.ID] = make(map[string]struct{})
			}
			pkg := finding.Trace[0].Package
			if pkg == "" {
				pkg = "Go standard library"
			}
			groupedPkgs[osv.ID][pkg] = struct{}{}
		}
	}

	var output VulncheckResultOutput
	if len(groupedPkgs) > 0 {
		output.Findings = make([]GroupedVulnFinding, 0, len(groupedPkgs))
		for id, pkgsSet := range groupedPkgs {
			pkgs := slices.Sorted(maps.Keys(pkgsSet))

			output.Findings = append(output.Findings, GroupedVulnFinding{
				ID:               id,
				Details:          result.Entries[id].Details,
				AffectedPackages: pkgs,
			})
		}
		sort.Slice(output.Findings, func(i, j int) bool {
			return output.Findings[i].ID < output.Findings[j].ID
		})
	}

	if logBuf.Len() > 0 {
		output.Logs = logBuf.String()
	}

	var summary bytes.Buffer
	fmt.Fprintf(&summary, "Vulnerability check for pattern %q complete. Found %d vulnerabilities.", pattern, len(output.Findings))
	if output.Logs != "" {
		fmt.Fprintf(&summary, "\nLogs are available in the structured output.")
	}

	return nil, &output, nil
}
