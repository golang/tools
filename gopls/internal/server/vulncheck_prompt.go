// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/event"
)

const (
	// goModHashKind is the kind for the go.mod hash in the filecache.
	goModHashKind = "gomodhash"
)

// computeGoModHash computes the SHA256 hash of the go.mod file's dependencies.
// It only considers the Require, Exclude, and Replace directives and ignores
// other parts of the file.
func computeGoModHash(file *modfile.File) (string, error) {
	h := sha256.New()
	for _, req := range file.Require {
		if _, err := h.Write([]byte(req.Mod.Path + req.Mod.Version)); err != nil {
			return "", err
		}
	}
	for _, exc := range file.Exclude {
		if _, err := h.Write([]byte(exc.Mod.Path + exc.Mod.Version)); err != nil {
			return "", err
		}
	}
	for _, rep := range file.Replace {
		if _, err := h.Write([]byte(rep.Old.Path + rep.Old.Version + rep.New.Path + rep.New.Version)); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *server) checkGoModDeps(ctx context.Context, uri protocol.DocumentURI) {
	if s.Options().Vulncheck != settings.ModeVulncheckPrompt {
		return
	}
	ctx, done := event.Start(ctx, "server.CheckGoModDeps")
	defer done()

	var (
		newHash, oldHash string
		pathHash         [32]byte
	)
	{
		newContent, err := os.ReadFile(uri.Path())
		if err != nil {
			event.Error(ctx, "reading new go.mod content failed", err)
			return
		}
		newModFile, err := modfile.Parse("go.mod", newContent, nil)
		if err != nil {
			event.Error(ctx, "parsing new go.mod failed", err)
			return
		}
		hash, err := computeGoModHash(newModFile)
		if err != nil {
			event.Error(ctx, "computing new go.mod hash failed", err)
			return
		}
		newHash = hash

		pathHash = sha256.Sum256([]byte(uri.Path()))
		oldHashBytes, err := filecache.Get(goModHashKind, pathHash)
		if err != nil && err != filecache.ErrNotFound {
			event.Error(ctx, "reading old go.mod hash from filecache failed", err)
			return
		}
		oldHash = string(oldHashBytes)
	}
	if oldHash != newHash {
		fileLink := fmt.Sprintf("[%s](%s)", uri.Path(), string(uri))
		govulncheckLink := "[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)"
		message := fmt.Sprintf("Dependencies have changed in %s, would you like to run %s to check for vulnerabilities?", fileLink, govulncheckLink)

		preference, err := getVulncheckPreference()
		if err != nil {
			event.Error(ctx, "reading vulncheck preference failed", err)
		}
		if preference == "Never" {
			return
		}
		if preference == "Always" {
			// TODO: Update this so that we run vulncheck when Always is set.
			return
		}
		action, err := showMessageRequest(ctx, s.client, protocol.Info, message, "Yes", "No", "Always", "Never")
		if err != nil {
			event.Error(ctx, "showing go.mod changed notification", err)
			return
		}

		if action == "Always" || action == "Never" {
			if err := setVulncheckPreference(action); err != nil {
				event.Error(ctx, "writing vulncheck preference failed", err)
				showMessage(ctx, s.client, protocol.Error, fmt.Sprintf("Failed to save vulncheck preference: %v", err))
			}
		}

		// TODO: Implement the logic to run govulncheck when action is "Yes" or "Always".
		if action == "No" || action == "Never" || action == "" {
			return // Skip the check and don't update the hash.
		}

		if err := filecache.Set(goModHashKind, pathHash, []byte(newHash)); err != nil {
			event.Error(ctx, "writing new go.mod hash to filecache failed", err)
			return
		}
	}
}

type vulncheckConfig struct {
	VulncheckMode string `json:"vulncheck"`
}

func getVulncheckPreference() (string, error) {
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
	return config.VulncheckMode, nil
}

func setVulncheckPreference(preference string) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	goplsDir := filepath.Join(configDir, "gopls", "vulncheck")
	if err := os.MkdirAll(goplsDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(goplsDir, "settings.json")
	config := vulncheckConfig{VulncheckMode: preference}
	content, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0644)
}
