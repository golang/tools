// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/progress"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/vulncheck/govulncheck"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/xcontext"
)

const (
	// goModHashKind is the kind for the go.mod hash in the filecache.
	goModHashKind  = "gomodhash"
	maxVulnsToShow = 10
)

type vulncheckAction string

const (
	vulncheckActionYes    vulncheckAction = "Yes"
	vulncheckActionNo     vulncheckAction = "No"
	vulncheckActionAlways vulncheckAction = "Always"
	vulncheckActionNever  vulncheckAction = "Never"
	vulncheckActionEmpty  vulncheckAction = ""
)

type vulnupgradeAction string

const (
	vulnupgradeActionUpgradeAll vulnupgradeAction = "Upgrade All"
	vulnupgradeActionIgnore     vulnupgradeAction = "Ignore"
	vulnupgradeActionEmpty      vulnupgradeAction = ""
)

// computeGoModHash computes the SHA256 hash of the go.mod file's dependencies.
// It only considers the Require, Exclude, and Replace directives and ignores
// other parts of the file.
func computeGoModHash(file *modfile.File) (string, error) {
	h := sha256.New()
	for _, req := range file.Require {
		if _, err := h.Write([]byte(req.Mod.Path + "\x00" + req.Mod.Version)); err != nil {
			return "", err
		}
	}
	for _, exc := range file.Exclude {
		if _, err := h.Write([]byte(exc.Mod.Path + "\x00" + exc.Mod.Version)); err != nil {
			return "", err
		}
	}
	for _, rep := range file.Replace {
		if _, err := h.Write([]byte(rep.Old.Path + "\x00" + rep.Old.Version + "\x00" + rep.New.Path + "\x00" + rep.New.Version)); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func getModFileHashes(uri protocol.DocumentURI) (contentHash string, pathHash [32]byte, err error) {
	content, err := os.ReadFile(uri.Path())
	if err != nil {
		return "", [32]byte{}, err
	}
	newModFile, err := modfile.Parse("go.mod", content, nil)
	if err != nil {
		return "", [32]byte{}, err
	}
	contentHash, err = computeGoModHash(newModFile)
	if err != nil {
		return "", [32]byte{}, err
	}
	pathHash = sha256.Sum256([]byte(uri.Path()))
	return contentHash, pathHash, nil
}

func (s *server) checkGoModDeps(ctx context.Context, uri protocol.DocumentURI) {
	if s.Options().Vulncheck != settings.ModeVulncheckPrompt {
		return
	}
	ctx, done := event.Start(ctx, "server.CheckGoModDeps")
	defer done()

	newHash, pathHash, err := getModFileHashes(uri)
	if err != nil {
		event.Error(ctx, "getting go.mod hashes failed", err)
		return
	}

	oldHashBytes, err := filecache.Get(goModHashKind, pathHash)
	if err != nil && err != filecache.ErrNotFound {
		event.Error(ctx, "reading old go.mod hash from filecache failed", err)
		return
	}
	oldHash := string(oldHashBytes)

	if oldHash != newHash {
		fileLink := fmt.Sprintf("[%s](%s)", uri.Path(), string(uri))
		govulncheckLink := "[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)"
		message := fmt.Sprintf("Dependencies have changed in %s, would you like to run %s to check for vulnerabilities?", fileLink, govulncheckLink)

		action, err := getVulncheckPreference()
		if err != nil {
			event.Error(ctx, "reading vulncheck preference failed", err)
		}

		switch action {
		case vulncheckActionAlways:
			go s.handleVulncheck(ctx, uri)
		case vulncheckActionNever:
			return
		case vulncheckActionEmpty:
			// no previous preference stored, prompt the user.
			fallthrough
		default:
			// invalid value, clear preference and prompt the user again.
			choice, err := showMessageRequest(ctx, s.client, protocol.Info, message, string(vulncheckActionYes), string(vulncheckActionNo), string(vulncheckActionAlways), string(vulncheckActionNever))
			if err != nil {
				event.Error(ctx, "showing go.mod changed notification failed", err)
				return
			}
			action = vulncheckAction(choice)
			recordVulncheckAction(action)
			if action == vulncheckActionAlways || action == vulncheckActionNever {
				if err := setVulncheckPreference(action); err != nil {
					event.Error(ctx, "writing vulncheck preference failed", err)
					showMessage(ctx, s.client, protocol.Error, fmt.Sprintf("Failed to save vulncheck preference: %v", err))
				}
			}
			if action == vulncheckActionYes || action == vulncheckActionAlways {
				go s.handleVulncheck(ctx, uri)
			}
			if action == vulncheckActionEmpty {
				// No user input gathered from prompt.
				return
			}
		}

		if err := filecache.Set(goModHashKind, pathHash, []byte(newHash)); err != nil {
			event.Error(ctx, "writing new go.mod hash to filecache failed", err)
			return
		}
	}
}

func recordVulncheckAction(action vulncheckAction) {
	switch action {
	case vulncheckActionYes:
		countVulncheckPromptYes.Inc()
	case vulncheckActionNo:
		countVulncheckPromptNo.Inc()
	case vulncheckActionAlways:
		countVulncheckPromptAlways.Inc()
	case vulncheckActionNever:
		countVulncheckPromptNever.Inc()
	}
}

func recordVulncheckupgradeAction(action vulnupgradeAction) {
	switch action {
	case vulnupgradeActionUpgradeAll:
		countVulncheckUpgradeAll.Inc()
	case vulnupgradeActionIgnore:
		countVulncheckUpgradeIgnore.Inc()
	}
}

func (s *server) handleVulncheck(ctx context.Context, uri protocol.DocumentURI) {
	_, snapshot, release, err := s.session.FileOf(ctx, uri)
	if err != nil {
		event.Error(ctx, "getting file snapshot failed", err)
		return
	}
	defer release()
	ctx = xcontext.Detach(ctx)

	work := s.progress.Start(ctx, GoVulncheckCommandTitle, "Running govulncheck...", nil, nil)
	defer work.End(ctx, "Done.")
	workDoneWriter := progress.NewWorkDoneWriter(ctx, work)
	result, err := s.runVulncheck(ctx, snapshot, uri, "./...", workDoneWriter)
	if err != nil {
		event.Error(ctx, "govulncheck failed", err)
		showMessage(ctx, s.client, protocol.Error, fmt.Sprintf("govulncheck failed: %v", err))
		return
	}

	affecting, stdLibVulns, modulesToUpgrade := computeModulesToUpgrade(result.Findings)
	numStdLib := len(stdLibVulns)
	if len(affecting) == 0 && numStdLib == 0 {
		showMessage(ctx, s.client, protocol.Info, "No vulnerabilities found.")
		return
	}

	affectingOSVs := slices.Sorted(maps.Keys(affecting))
	sort.Strings(affectingOSVs)

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d actionable vulnerabilities and %d standard library vulnerabilities affecting your dependencies:\n\n", len(affectingOSVs), numStdLib)

	for i, id := range affectingOSVs {
		if i >= maxVulnsToShow {
			break
		}
		cveURL := fmt.Sprintf("https://pkg.go.dev/vuln/%s", id)
		if s.Options().LinkifyShowMessage {
			fmt.Fprintf(&b, "Vulnerability #%d: [%s](%s)", i+1, id, cveURL)
		} else {
			fmt.Fprintf(&b, "Vulnerability #%d: %s (%s)", i+1, id, cveURL)
		}
		if i < len(affectingOSVs)-1 && i < maxVulnsToShow-1 {
			b.WriteString(", ")
		}
	}
	if len(affectingOSVs) > maxVulnsToShow {
		fmt.Fprintf(&b, "\n\n...and %d more.", len(affectingOSVs)-maxVulnsToShow)
	}

	if numStdLib > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Upgrading your Go version may address vulnerabilities in the standard library.")
	}

	actions := []string{string(vulnupgradeActionIgnore)}
	if len(modulesToUpgrade) > 0 {
		actions = append([]string{string(vulnupgradeActionUpgradeAll)}, actions...)
	}

	action, err := showMessageRequest(ctx, s.client, protocol.Warning, b.String(), actions...)
	if err != nil {
		event.Error(ctx, "vulncheck remediation failed", err)
		return
	}
	upgradeAction := vulnupgradeAction(action)
	recordVulncheckupgradeAction(upgradeAction)

	if upgradeAction == vulnupgradeActionUpgradeAll {
		if err := s.upgradeModules(ctx, snapshot, uri, modulesToUpgrade); err != nil {
			event.Error(ctx, "upgrading modules failed", err)
		}
	}
}

func computeModulesToUpgrade(findings []*govulncheck.Finding) (affecting map[string]bool, stdLibVulns map[string]bool, modulesToUpgrade map[string]string) {
	affecting = make(map[string]bool)
	stdLibVulns = make(map[string]bool)
	modulesToUpgrade = make(map[string]string)

	for _, f := range findings {
		if len(f.Trace) == 0 {
			continue
		}
		mod := f.Trace[0].Module
		// An empty module path or "stdlib" indicates a standard library package.
		// These vulnerabilities cannot be remediated via module upgrades and
		// instead require updating the Go toolchain.
		if mod == "stdlib" || mod == "" {
			stdLibVulns[f.OSV] = true
		} else {
			affecting[f.OSV] = true
			if f.FixedVersion != "" {
				current, ok := modulesToUpgrade[mod]
				if !ok || current == "latest" || semver.Compare(f.FixedVersion, current) > 0 {
					modulesToUpgrade[mod] = f.FixedVersion
				}
			} else if _, ok := modulesToUpgrade[mod]; !ok {
				modulesToUpgrade[mod] = "latest"
			}
		}
	}
	return affecting, stdLibVulns, modulesToUpgrade
}

func (s *server) upgradeModules(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, modulesToUpgrade map[string]string) error {
	if err := s.runGoGet(ctx, snapshot, uri, modulesToUpgrade); err != nil {
		return err
	}
	if err := s.runGoModTidy(ctx, snapshot, uri); err != nil {
		return err
	}

	var (
		upgradedStrs []string
		upgrades     []string
	)
	for module, version := range modulesToUpgrade {
		upgrades = append(upgrades, module+"@"+version)
		upgradedStrs = append(upgradedStrs, fmt.Sprintf("%s to %s", module, version))
	}
	sort.Strings(upgradedStrs)

	msg := fmt.Sprintf("Successfully upgraded vulnerable modules:\n %s", strings.Join(upgradedStrs, ",\n "))
	showMessage(ctx, s.client, protocol.Info, msg)
	if hash, pathHash, err := getModFileHashes(uri); err == nil {
		if err := filecache.Set(goModHashKind, pathHash, []byte(hash)); err != nil {
			event.Error(ctx, "failed to update go.mod hash after upgrade", err)
		}
	} else {
		event.Error(ctx, "failed to get go.mod hash after upgrade", err)
	}
	return nil
}

func (s *server) runGoGet(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, modulesToUpgrade map[string]string) error {
	work := s.progress.Start(ctx, "Upgrading Modules", "Running go get...", nil, nil)
	defer work.End(ctx, "Done.")

	var upgrades []string
	for module, version := range modulesToUpgrade {
		upgrades = append(upgrades, module+"@"+version)
	}

	if err := runGoCommand(ctx, snapshot, uri, "get", upgrades); err != nil {
		msg := fmt.Sprintf("Failed to upgrade modules: %v", err)
		showMessage(ctx, s.client, protocol.Error, msg)
		return err
	}
	return nil
}

func (s *server) runGoModTidy(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI) error {
	work := s.progress.Start(ctx, "Upgrading Modules", "Running go mod tidy...", nil, nil)
	defer work.End(ctx, "Done.")

	if err := runGoCommand(ctx, snapshot, uri, "mod", []string{"tidy"}); err != nil {
		event.Error(ctx, "go mod tidy failed", err)
		showMessage(ctx, s.client, protocol.Error, fmt.Sprintf("go mod tidy failed: %v", err))
		return err
	}
	return nil
}

func runGoCommand(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, verb string, args []string) error {
	dir := uri.DirPath()
	inv, cleanup, err := snapshot.GoCommandInvocation(cache.NetworkOK, dir, verb, args)
	if err != nil {
		return err
	}
	defer cleanup()
	var stdout, stderr bytes.Buffer
	if err := snapshot.View().GoCommandRunner().RunPiped(ctx, *inv, &stdout, &stderr); err != nil {
		return fmt.Errorf("go %s %s failed: %v\n-- stdout --\n%s\n-- stderr --\n%s", verb, strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return nil
}

type vulncheckConfig struct {
	VulncheckMode string `json:"vulncheck"`
}

func getVulncheckPreference() (vulncheckAction, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(configDir, "gopls", "vulncheck", "settings.json")
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var config vulncheckConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return "", err
	}
	return vulncheckAction(config.VulncheckMode), nil
}

func setVulncheckPreference(preference vulncheckAction) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	goplsDir := filepath.Join(configDir, "gopls", "vulncheck")
	if err := os.MkdirAll(goplsDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(goplsDir, "settings.json")
	config := vulncheckConfig{VulncheckMode: string(preference)}
	content, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0644)
}
