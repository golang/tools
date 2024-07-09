// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package mod provides core features related to go.mod file
// handling for use by Go editors and tools.
package mod

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/vulncheck/govulncheck"
	"golang.org/x/tools/internal/event"
)

// ParseDiagnostics returns diagnostics from parsing the go.mod files in the workspace.
func ParseDiagnostics(ctx context.Context, snapshot *cache.Snapshot) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	ctx, done := event.Start(ctx, "mod.Diagnostics", snapshot.Labels()...)
	defer done()

	return collectDiagnostics(ctx, snapshot, ModParseDiagnostics)
}

// Diagnostics returns diagnostics from running go mod tidy.
func TidyDiagnostics(ctx context.Context, snapshot *cache.Snapshot) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	ctx, done := event.Start(ctx, "mod.Diagnostics", snapshot.Labels()...)
	defer done()

	return collectDiagnostics(ctx, snapshot, ModTidyDiagnostics)
}

// UpgradeDiagnostics returns upgrade diagnostics for the modules in the
// workspace with known upgrades.
func UpgradeDiagnostics(ctx context.Context, snapshot *cache.Snapshot) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	ctx, done := event.Start(ctx, "mod.UpgradeDiagnostics", snapshot.Labels()...)
	defer done()

	return collectDiagnostics(ctx, snapshot, ModUpgradeDiagnostics)
}

// VulnerabilityDiagnostics returns vulnerability diagnostics for the active modules in the
// workspace with known vulnerabilities.
func VulnerabilityDiagnostics(ctx context.Context, snapshot *cache.Snapshot) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	ctx, done := event.Start(ctx, "mod.VulnerabilityDiagnostics", snapshot.Labels()...)
	defer done()

	return collectDiagnostics(ctx, snapshot, ModVulnerabilityDiagnostics)
}

func collectDiagnostics(ctx context.Context, snapshot *cache.Snapshot, diagFn func(context.Context, *cache.Snapshot, file.Handle) ([]*cache.Diagnostic, error)) (map[protocol.DocumentURI][]*cache.Diagnostic, error) {
	g, ctx := errgroup.WithContext(ctx)
	cpulimit := runtime.GOMAXPROCS(0)
	g.SetLimit(cpulimit)

	var mu sync.Mutex
	reports := make(map[protocol.DocumentURI][]*cache.Diagnostic)

	for _, uri := range snapshot.View().ModFiles() {
		uri := uri
		g.Go(func() error {
			fh, err := snapshot.ReadFile(ctx, uri)
			if err != nil {
				return err
			}
			diagnostics, err := diagFn(ctx, snapshot, fh)
			if err != nil {
				return err
			}
			for _, d := range diagnostics {
				mu.Lock()
				reports[d.URI] = append(reports[fh.URI()], d)
				mu.Unlock()
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return reports, nil
}

// ModParseDiagnostics reports diagnostics from parsing the mod file.
func ModParseDiagnostics(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) (diagnostics []*cache.Diagnostic, err error) {
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil {
		if pm == nil || len(pm.ParseErrors) == 0 {
			return nil, err
		}
		return pm.ParseErrors, nil
	}
	return nil, nil
}

// ModTidyDiagnostics reports diagnostics from running go mod tidy.
func ModTidyDiagnostics(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) ([]*cache.Diagnostic, error) {
	pm, err := snapshot.ParseMod(ctx, fh) // memoized
	if err != nil {
		return nil, nil // errors reported by ModDiagnostics above
	}

	tidied, err := snapshot.ModTidy(ctx, pm)
	if err != nil {
		if err != cache.ErrNoModOnDisk && !strings.Contains(err.Error(), "GOPROXY=off") {
			// TODO(rfindley): the check for ErrNoModOnDisk was historically determined
			// to be benign, but may date back to the time when the Go command did not
			// have overlay support.
			//
			// See if we can pass the overlay to the Go command, and eliminate this guard..

			// TODO(golang/go#56395): remove the arbitrary suppression of the mod
			// tidy error when GOPROXY=off. The true fix for this noisy log message
			// is to fix the mod tidy diagnostics.
			event.Error(ctx, fmt.Sprintf("tidy: diagnosing %s", pm.URI), err)
		}
		return nil, nil
	}
	return tidied.Diagnostics, nil
}

// ModUpgradeDiagnostics adds upgrade quick fixes for individual modules if the upgrades
// are recorded in the view.
func ModUpgradeDiagnostics(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) (upgradeDiagnostics []*cache.Diagnostic, err error) {
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil {
		// Don't return an error if there are parse error diagnostics to be shown, but also do not
		// continue since we won't be able to show the upgrade diagnostics.
		if pm != nil && len(pm.ParseErrors) != 0 {
			return nil, nil
		}
		return nil, err
	}

	upgrades := snapshot.ModuleUpgrades(fh.URI())
	for _, req := range pm.File.Require {
		ver, ok := upgrades[req.Mod.Path]
		if !ok || req.Mod.Version == ver {
			continue
		}
		rng, err := pm.Mapper.OffsetRange(req.Syntax.Start.Byte, req.Syntax.End.Byte)
		if err != nil {
			return nil, err
		}
		// Upgrade to the exact version we offer the user, not the most recent.
		title := fmt.Sprintf("%s%v", upgradeCodeActionPrefix, ver)
		cmd, err := command.NewUpgradeDependencyCommand(title, command.DependencyArgs{
			URI:        fh.URI(),
			AddRequire: false,
			GoCmdArgs:  []string{req.Mod.Path + "@" + ver},
		})
		if err != nil {
			return nil, err
		}
		upgradeDiagnostics = append(upgradeDiagnostics, &cache.Diagnostic{
			URI:            fh.URI(),
			Range:          rng,
			Severity:       protocol.SeverityInformation,
			Source:         cache.UpgradeNotification,
			Message:        fmt.Sprintf("%v can be upgraded", req.Mod.Path),
			SuggestedFixes: []cache.SuggestedFix{cache.SuggestedFixFromCommand(cmd, protocol.QuickFix)},
		})
	}

	return upgradeDiagnostics, nil
}

const upgradeCodeActionPrefix = "Upgrade to "

// ModVulnerabilityDiagnostics adds diagnostics for vulnerabilities in individual modules
// if the vulnerability is recorded in the view.
func ModVulnerabilityDiagnostics(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle) (vulnDiagnostics []*cache.Diagnostic, err error) {
	pm, err := snapshot.ParseMod(ctx, fh)
	if err != nil {
		// Don't return an error if there are parse error diagnostics to be shown, but also do not
		// continue since we won't be able to show the vulnerability diagnostics.
		if pm != nil && len(pm.ParseErrors) != 0 {
			return nil, nil
		}
		return nil, err
	}

	diagSource := cache.Govulncheck
	vs := snapshot.Vulnerabilities(fh.URI())[fh.URI()]
	if vs == nil && snapshot.Options().Vulncheck == settings.ModeVulncheckImports {
		vs, err = snapshot.ModVuln(ctx, fh.URI())
		if err != nil {
			return nil, err
		}
		diagSource = cache.Vulncheck
	}
	if vs == nil || len(vs.Findings) == 0 {
		return nil, nil
	}

	suggestRunOrResetGovulncheck, err := suggestGovulncheckAction(diagSource == cache.Govulncheck, fh.URI())
	if err != nil {
		// must not happen
		return nil, err // TODO: bug report
	}
	vulnsByModule := make(map[string][]*govulncheck.Finding)

	for _, finding := range vs.Findings {
		if vuln, typ := foundVuln(finding); typ == vulnCalled || typ == vulnImported {
			vulnsByModule[vuln.Module] = append(vulnsByModule[vuln.Module], finding)
		}
	}
	for _, req := range pm.File.Require {
		mod := req.Mod.Path
		findings := vulnsByModule[mod]
		if len(findings) == 0 {
			continue
		}
		// note: req.Syntax is the line corresponding to 'require', which means
		// req.Syntax.Start can point to the beginning of the "require" keyword
		// for a single line require (e.g. "require golang.org/x/mod v0.0.0").
		start := req.Syntax.Start.Byte
		if len(req.Syntax.Token) == 3 {
			start += len("require ")
		}
		rng, err := pm.Mapper.OffsetRange(start, req.Syntax.End.Byte)
		if err != nil {
			return nil, err
		}
		// Map affecting vulns to 'warning' level diagnostics,
		// others to 'info' level diagnostics.
		// Fixes will include only the upgrades for warning level diagnostics.
		var warningFixes, infoFixes []cache.SuggestedFix
		var warningSet, infoSet = map[string]bool{}, map[string]bool{}
		for _, finding := range findings {
			// It is possible that the source code was changed since the last
			// govulncheck run and information in the `vulns` info is stale.
			// For example, imagine that a user is in the middle of updating
			// problematic modules detected by the govulncheck run by applying
			// quick fixes. Stale diagnostics can be confusing and prevent the
			// user from quickly locating the next module to fix.
			// Ideally we should rerun the analysis with the updated module
			// dependencies or any other code changes, but we are not yet
			// in the position of automatically triggering the analysis
			// (govulncheck can take a while). We also don't know exactly what
			// part of source code was changed since `vulns` was computed.
			// As a heuristic, we assume that a user upgrades the affecting
			// module to the version with the fix or the latest one, and if the
			// version in the require statement is equal to or higher than the
			// fixed version, skip generating a diagnostic about the vulnerability.
			// Eventually, the user has to rerun govulncheck.
			if finding.FixedVersion != "" && semver.IsValid(req.Mod.Version) && semver.Compare(finding.FixedVersion, req.Mod.Version) <= 0 {
				continue
			}
			switch _, typ := foundVuln(finding); typ {
			case vulnImported:
				infoSet[finding.OSV] = true
			case vulnCalled:
				warningSet[finding.OSV] = true
			}
			// Upgrade to the exact version we offer the user, not the most recent.
			if fixedVersion := finding.FixedVersion; semver.IsValid(fixedVersion) && semver.Compare(req.Mod.Version, fixedVersion) < 0 {
				cmd, err := getUpgradeCodeAction(fh, req, fixedVersion)
				if err != nil {
					return nil, err // TODO: bug report
				}
				sf := cache.SuggestedFixFromCommand(cmd, protocol.QuickFix)
				switch _, typ := foundVuln(finding); typ {
				case vulnImported:
					infoFixes = append(infoFixes, sf)
				case vulnCalled:
					warningFixes = append(warningFixes, sf)
				}
			}
		}

		if len(warningSet) == 0 && len(infoSet) == 0 {
			continue
		}
		// Remove affecting osvs from the non-affecting osv list if any.
		if len(warningSet) > 0 {
			for k := range infoSet {
				if warningSet[k] {
					delete(infoSet, k)
				}
			}
		}
		// Add an upgrade for module@latest.
		// TODO(suzmue): verify if latest is the same as fixedVersion.
		latest, err := getUpgradeCodeAction(fh, req, "latest")
		if err != nil {
			return nil, err // TODO: bug report
		}
		sf := cache.SuggestedFixFromCommand(latest, protocol.QuickFix)
		if len(warningFixes) > 0 {
			warningFixes = append(warningFixes, sf)
		}
		if len(infoFixes) > 0 {
			infoFixes = append(infoFixes, sf)
		}
		if len(warningSet) > 0 {
			warning := sortedKeys(warningSet)
			warningFixes = append(warningFixes, suggestRunOrResetGovulncheck)
			vulnDiagnostics = append(vulnDiagnostics, &cache.Diagnostic{
				URI:            fh.URI(),
				Range:          rng,
				Severity:       protocol.SeverityWarning,
				Source:         diagSource,
				Message:        getVulnMessage(req.Mod.Path, warning, true, diagSource == cache.Govulncheck),
				SuggestedFixes: warningFixes,
			})
		}
		if len(infoSet) > 0 {
			info := sortedKeys(infoSet)
			infoFixes = append(infoFixes, suggestRunOrResetGovulncheck)
			vulnDiagnostics = append(vulnDiagnostics, &cache.Diagnostic{
				URI:            fh.URI(),
				Range:          rng,
				Severity:       protocol.SeverityInformation,
				Source:         diagSource,
				Message:        getVulnMessage(req.Mod.Path, info, false, diagSource == cache.Govulncheck),
				SuggestedFixes: infoFixes,
			})
		}
	}

	// TODO(hyangah): place this diagnostic on the `go` directive or `toolchain` directive
	// after https://go.dev/issue/57001.
	const diagnoseStdLib = false

	// If diagnosing the stdlib, add standard library vulnerability diagnostics
	// on the module declaration.
	//
	// Only proceed if we have a valid module declaration on which to position
	// the diagnostics.
	if diagnoseStdLib && pm.File.Module != nil && pm.File.Module.Syntax != nil {
		// Add standard library vulnerabilities.
		stdlibVulns := vulnsByModule["stdlib"]
		if len(stdlibVulns) == 0 {
			return vulnDiagnostics, nil
		}

		// Put the standard library diagnostic on the module declaration.
		rng, err := pm.Mapper.OffsetRange(pm.File.Module.Syntax.Start.Byte, pm.File.Module.Syntax.End.Byte)
		if err != nil {
			return vulnDiagnostics, nil // TODO: bug report
		}

		var warningSet, infoSet = map[string]bool{}, map[string]bool{}
		for _, finding := range stdlibVulns {
			switch _, typ := foundVuln(finding); typ {
			case vulnImported:
				infoSet[finding.OSV] = true
			case vulnCalled:
				warningSet[finding.OSV] = true
			}
		}
		if len(warningSet) > 0 {
			warning := sortedKeys(warningSet)
			fixes := []cache.SuggestedFix{suggestRunOrResetGovulncheck}
			vulnDiagnostics = append(vulnDiagnostics, &cache.Diagnostic{
				URI:            fh.URI(),
				Range:          rng,
				Severity:       protocol.SeverityWarning,
				Source:         diagSource,
				Message:        getVulnMessage("go", warning, true, diagSource == cache.Govulncheck),
				SuggestedFixes: fixes,
			})

			// remove affecting osvs from the non-affecting osv list if any.
			for k := range infoSet {
				if warningSet[k] {
					delete(infoSet, k)
				}
			}
		}
		if len(infoSet) > 0 {
			info := sortedKeys(infoSet)
			fixes := []cache.SuggestedFix{suggestRunOrResetGovulncheck}
			vulnDiagnostics = append(vulnDiagnostics, &cache.Diagnostic{
				URI:            fh.URI(),
				Range:          rng,
				Severity:       protocol.SeverityInformation,
				Source:         diagSource,
				Message:        getVulnMessage("go", info, false, diagSource == cache.Govulncheck),
				SuggestedFixes: fixes,
			})
		}
	}

	return vulnDiagnostics, nil
}

type vulnFindingType int

const (
	vulnUnknown vulnFindingType = iota
	vulnCalled
	vulnImported
	vulnRequired
)

// foundVuln returns the frame info describing discovered vulnerable symbol/package/module
// and how this vulnerability affects the analyzed package or module.
func foundVuln(finding *govulncheck.Finding) (*govulncheck.Frame, vulnFindingType) {
	// finding.Trace is sorted from the imported vulnerable symbol to
	// the entry point in the callstack.
	// If Function is set, then Package must be set. Module will always be set.
	// If Function is set it was found in the call graph, otherwise if Package is set
	// it was found in the import graph, otherwise it was found in the require graph.
	// See the documentation of govulncheck.Finding.
	if len(finding.Trace) == 0 { // this shouldn't happen, but just in case...
		return nil, vulnUnknown
	}
	vuln := finding.Trace[0]
	if vuln.Package == "" {
		return vuln, vulnRequired
	}
	if vuln.Function == "" {
		return vuln, vulnImported
	}
	return vuln, vulnCalled
}

func sortedKeys(m map[string]bool) []string {
	ret := make([]string, 0, len(m))
	for k := range m {
		ret = append(ret, k)
	}
	sort.Strings(ret)
	return ret
}

// suggestGovulncheckAction returns a code action that suggests either run govulncheck
// for more accurate investigation (if the present vulncheck diagnostics are based on
// analysis less accurate than govulncheck) or reset the existing govulncheck result
// (if the present vulncheck diagnostics are already based on govulncheck run).
func suggestGovulncheckAction(fromGovulncheck bool, uri protocol.DocumentURI) (cache.SuggestedFix, error) {
	if fromGovulncheck {
		resetVulncheck, err := command.NewResetGoModDiagnosticsCommand("Reset govulncheck result", command.ResetGoModDiagnosticsArgs{
			URIArg:           command.URIArg{URI: uri},
			DiagnosticSource: string(cache.Govulncheck),
		})
		if err != nil {
			return cache.SuggestedFix{}, err
		}
		return cache.SuggestedFixFromCommand(resetVulncheck, protocol.QuickFix), nil
	}
	vulncheck, err := command.NewRunGovulncheckCommand("Run govulncheck to verify", command.VulncheckArgs{
		URI:     uri,
		Pattern: "./...",
	})
	if err != nil {
		return cache.SuggestedFix{}, err
	}
	return cache.SuggestedFixFromCommand(vulncheck, protocol.QuickFix), nil
}

func getVulnMessage(mod string, vulns []string, used, fromGovulncheck bool) string {
	var b strings.Builder
	if used {
		switch len(vulns) {
		case 1:
			fmt.Fprintf(&b, "%v has a vulnerability used in the code: %v.", mod, vulns[0])
		default:
			fmt.Fprintf(&b, "%v has vulnerabilities used in the code: %v.", mod, strings.Join(vulns, ", "))
		}
	} else {
		if fromGovulncheck {
			switch len(vulns) {
			case 1:
				fmt.Fprintf(&b, "%v has a vulnerability %v that is not used in the code.", mod, vulns[0])
			default:
				fmt.Fprintf(&b, "%v has known vulnerabilities %v that are not used in the code.", mod, strings.Join(vulns, ", "))
			}
		} else {
			switch len(vulns) {
			case 1:
				fmt.Fprintf(&b, "%v has a vulnerability %v.", mod, vulns[0])
			default:
				fmt.Fprintf(&b, "%v has known vulnerabilities %v.", mod, strings.Join(vulns, ", "))
			}
		}
	}
	return b.String()
}

// href returns the url for the vulnerability information.
// Eventually we should retrieve the url embedded in the osv.Entry.
// While vuln.go.dev is under development, this always returns
// the page in pkg.go.dev.
func href(vulnID string) string {
	return fmt.Sprintf("https://pkg.go.dev/vuln/%s", vulnID)
}

func getUpgradeCodeAction(fh file.Handle, req *modfile.Require, version string) (protocol.Command, error) {
	cmd, err := command.NewUpgradeDependencyCommand(upgradeTitle(version), command.DependencyArgs{
		URI:        fh.URI(),
		AddRequire: false,
		GoCmdArgs:  []string{req.Mod.Path + "@" + version},
	})
	if err != nil {
		return protocol.Command{}, err
	}
	return cmd, nil
}

func upgradeTitle(fixedVersion string) string {
	title := fmt.Sprintf("%s%v", upgradeCodeActionPrefix, fixedVersion)
	return title
}

// SelectUpgradeCodeActions takes a list of code actions for a required module
// and returns a more selective list of upgrade code actions,
// where the code actions have been deduped. Code actions unrelated to upgrade
// are deduplicated by the name.
func SelectUpgradeCodeActions(actions []protocol.CodeAction) []protocol.CodeAction {
	if len(actions) <= 1 {
		return actions // return early if no sorting necessary
	}
	var versionedUpgrade, latestUpgrade, resetAction protocol.CodeAction
	var chosenVersionedUpgrade string
	var selected []protocol.CodeAction

	seenTitles := make(map[string]bool)

	for _, action := range actions {
		if strings.HasPrefix(action.Title, upgradeCodeActionPrefix) {
			if v := getUpgradeVersion(action); v == "latest" && latestUpgrade.Title == "" {
				latestUpgrade = action
			} else if versionedUpgrade.Title == "" || semver.Compare(v, chosenVersionedUpgrade) > 0 {
				chosenVersionedUpgrade = v
				versionedUpgrade = action
			}
		} else if strings.HasPrefix(action.Title, "Reset govulncheck") {
			resetAction = action
		} else if !seenTitles[action.Command.Title] {
			seenTitles[action.Command.Title] = true
			selected = append(selected, action)
		}
	}
	if versionedUpgrade.Title != "" {
		selected = append(selected, versionedUpgrade)
	}
	if latestUpgrade.Title != "" {
		selected = append(selected, latestUpgrade)
	}
	if resetAction.Title != "" {
		selected = append(selected, resetAction)
	}
	return selected
}

func getUpgradeVersion(p protocol.CodeAction) string {
	return strings.TrimPrefix(p.Title, upgradeCodeActionPrefix)
}
