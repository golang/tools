// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.18
// +build go1.18

package scan

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/gopls/internal/vulncheck/govulncheck"
	"golang.org/x/tools/gopls/internal/vulncheck/osv"
	"golang.org/x/vuln/scan"
)

// Main implements gopls vulncheck.
func Main(ctx context.Context, args ...string) error {
	// wrapping govulncheck.
	cmd := scan.Command(ctx, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Wait()
}

// RunGovulncheck implements the codelens "Run Govulncheck"
// that runs 'gopls vulncheck' and converts the output to gopls's internal data
// used for diagnostics and hover message construction.
//
// TODO(rfindley): this should accept a *View (which exposes) Options, rather
// than a snapshot.
func RunGovulncheck(ctx context.Context, pattern string, snapshot *cache.Snapshot, dir string, log io.Writer) (*vulncheck.Result, error) {
	vulncheckargs := []string{
		"vulncheck", "--",
		"-json",
		"-mode", "source",
		"-scan", "symbol",
	}
	if dir != "" {
		vulncheckargs = append(vulncheckargs, "-C", dir)
	}
	if db := cache.GetEnv(snapshot, "GOVULNDB"); db != "" {
		vulncheckargs = append(vulncheckargs, "-db", db)
	}
	vulncheckargs = append(vulncheckargs, pattern)
	// TODO: support -tags. need to compute tags args from opts.BuildFlags.
	// TODO: support -test.

	ir, iw := io.Pipe()
	handler := &govulncheckHandler{logger: log, osvs: map[string]*osv.Entry{}}

	stderr := new(bytes.Buffer)
	var g errgroup.Group
	// We run the govulncheck's analysis in a separate process as it can
	// consume a lot of CPUs and memory, and terminates: a separate process
	// is a perfect garbage collector and affords us ways to limit its resource usage.
	g.Go(func() error {
		defer iw.Close()

		cmd := exec.CommandContext(ctx, os.Args[0], vulncheckargs...)
		cmd.Env = getEnvSlices(snapshot)
		if goversion := cache.GetEnv(snapshot, cache.GoVersionForVulnTest); goversion != "" {
			// Let govulncheck API use a different Go version using the (undocumented) hook
			// in https://go.googlesource.com/vuln/+/v1.0.1/internal/scan/run.go#76
			cmd.Env = append(cmd.Env, "GOVERSION="+goversion)
		}
		cmd.Stderr = stderr // stream vulncheck's STDERR as progress reports
		cmd.Stdout = iw     // let the other goroutine parses the result.

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start govulncheck: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("failed to run govulncheck: %v", err)
		}
		return nil
	})
	g.Go(func() error {
		return govulncheck.HandleJSON(ir, handler)
	})
	if err := g.Wait(); err != nil {
		if stderr.Len() > 0 {
			log.Write(stderr.Bytes())
		}
		return nil, fmt.Errorf("failed to read govulncheck output: %v", err)
	}

	findings := handler.findings // sort so the findings in the result is deterministic.
	sort.Slice(findings, func(i, j int) bool {
		x, y := findings[i], findings[j]
		if x.OSV != y.OSV {
			return x.OSV < y.OSV
		}
		return x.Trace[0].Package < y.Trace[0].Package
	})
	result := &vulncheck.Result{
		Mode:     vulncheck.ModeGovulncheck,
		AsOf:     time.Now(),
		Entries:  handler.osvs,
		Findings: findings,
	}
	return result, nil
}

type govulncheckHandler struct {
	logger io.Writer // forward progress reports to logger.

	osvs     map[string]*osv.Entry
	findings []*govulncheck.Finding
}

// Config implements vulncheck.Handler.
func (h *govulncheckHandler) Config(config *govulncheck.Config) error {
	if config.GoVersion != "" {
		fmt.Fprintf(h.logger, "Go: %v\n", config.GoVersion)
	}
	if config.ScannerName != "" {
		scannerName := fmt.Sprintf("Scanner: %v", config.ScannerName)
		if config.ScannerVersion != "" {
			scannerName += "@" + config.ScannerVersion
		}
		fmt.Fprintln(h.logger, scannerName)
	}
	if config.DB != "" {
		dbInfo := fmt.Sprintf("DB: %v", config.DB)
		if config.DBLastModified != nil {
			dbInfo += fmt.Sprintf(" (DB updated: %v)", config.DBLastModified.String())
		}
		fmt.Fprintln(h.logger, dbInfo)
	}
	return nil
}

// Finding implements vulncheck.Handler.
func (h *govulncheckHandler) Finding(finding *govulncheck.Finding) error {
	h.findings = append(h.findings, finding)
	return nil
}

// OSV implements vulncheck.Handler.
func (h *govulncheckHandler) OSV(entry *osv.Entry) error {
	h.osvs[entry.ID] = entry
	return nil
}

// Progress implements vulncheck.Handler.
func (h *govulncheckHandler) Progress(progress *govulncheck.Progress) error {
	if progress.Message != "" {
		fmt.Fprintf(h.logger, "%v\n", progress.Message)
	}
	return nil
}

func getEnvSlices(snapshot *cache.Snapshot) []string {
	return append(os.Environ(), snapshot.Options().EnvSlice()...)
}
