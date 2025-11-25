// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/filecache"
	"golang.org/x/tools/gopls/internal/progress"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/internal/testenv"
)

func TestComputeGoModHash(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name:    "empty file",
			content: "module example.com",
			want:    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", // sha256 of empty string
		},
		{
			name: "with require",
			content: `
			module example.com
			require (
				golang.org/x/tools v0.1.0
				golang.org/x/vuln v0.2.0
			)
			`,
			want: func() string {
				h := sha256.New()
				h.Write([]byte("golang.org/x/tools\x00v0.1.0"))
				h.Write([]byte("golang.org/x/vuln\x00v0.2.0"))
				return hex.EncodeToString(h.Sum(nil))
			}(),
		},
		{
			name: "with exclude",
			content: `
			module example.com
			exclude (
				golang.org/x/tools v0.1.0
			)
			`,
			want: func() string {
				h := sha256.New()
				h.Write([]byte("golang.org/x/tools\x00v0.1.0"))
				return hex.EncodeToString(h.Sum(nil))
			}(),
		},
		{
			name: "with replace",
			content: `
			module example.com
			replace (
				golang.org/x/tools v0.1.0 => golang.org/x/tools v0.2.0
			)
			`,
			want: func() string {
				h := sha256.New()
				h.Write([]byte("golang.org/x/tools\x00v0.1.0\x00golang.org/x/tools\x00v0.2.0"))
				return hex.EncodeToString(h.Sum(nil))
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modFile, err := modfile.Parse("go.mod", []byte(tt.content), nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := computeGoModHash(modFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("computeGoModHash() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("computeGoModHash() = %v, want %v", got, tt.want)
			}
		})
	}
}

type mockClient struct {
	protocol.Client
	showMessageRequest func(context.Context, *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error)
}

func (c *mockClient) ShowMessageRequest(ctx context.Context, params *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
	if c.showMessageRequest != nil {
		return c.showMessageRequest(ctx, params)
	}
	return nil, nil
}

func (c *mockClient) ShowMessage(ctx context.Context, params *protocol.ShowMessageParams) error {
	return nil
}

func (c *mockClient) Close() error {
	return nil
}

func TestCheckGoModDeps(t *testing.T) {
	testenv.NeedsExec(t)
	const (
		yes    = "Yes"
		no     = "No"
		always = "Always"
		never  = "Never"
	)

	tests := []struct {
		name            string
		vulncheckMode   settings.VulncheckMode
		oldContent      string
		newContent      string
		userAction      string
		wantPrompt      bool
		wantHashUpdated bool
	}{
		{
			name:          "vulncheck disabled",
			vulncheckMode: settings.ModeVulncheckOff,
			oldContent:    "module example.com",
			newContent: `
			module example.com
			require golang.org/x/tools v0.1.0
			`,
			wantPrompt: false,
		},
		{
			name:          "no changes",
			vulncheckMode: settings.ModeVulncheckPrompt,
			oldContent:    "module example.com",
			newContent:    "module example.com",
			wantPrompt:    false,
		},
		{
			name:          "user says yes",
			vulncheckMode: settings.ModeVulncheckPrompt,
			oldContent:    "module example.com",
			newContent: `
			module example.com
			require golang.org/x/tools v0.1.0
			`,
			userAction:      yes,
			wantPrompt:      true,
			wantHashUpdated: true,
		},
		{
			name:          "user says no",
			vulncheckMode: settings.ModeVulncheckPrompt,
			oldContent:    "module example.com",
			newContent: `
			module example.com
			require golang.org/x/tools v0.1.0
			`,
			userAction:      no,
			wantPrompt:      true,
			wantHashUpdated: true,
		},
		{
			name:          "user says always",
			vulncheckMode: settings.ModeVulncheckPrompt,
			oldContent:    "module example.com",
			newContent: `
			module example.com
			require golang.org/x/tools v0.1.0
			`,
			userAction:      always,
			wantPrompt:      true,
			wantHashUpdated: true,
		},
		{
			name:          "user says never",
			vulncheckMode: settings.ModeVulncheckPrompt,
			oldContent:    "module example.com",
			newContent: `
			module example.com
			require golang.org/x/tools v0.1.0
			`,
			userAction:      never,
			wantPrompt:      true,
			wantHashUpdated: true,
		},
		{
			name:          "user dismisses prompt",
			vulncheckMode: settings.ModeVulncheckPrompt,
			oldContent:    "module example.com",
			newContent: `
			module example.com
			require golang.org/x/tools v0.1.0
			`,
			userAction: "",
			wantPrompt: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(func() {
				configDir, err := os.UserConfigDir()
				if err != nil {
					t.Fatalf("os.UserConfigDir() failed: %v", err)
				}
				if err := os.RemoveAll(filepath.Join(configDir, "gopls")); err != nil && !os.IsNotExist(err) {
					t.Fatalf("failed to clear user config: %v", err)
				}
			})
			t.Setenv("HOME", t.TempDir())
			ctx := context.Background()
			var promptShown bool
			client := &mockClient{
				showMessageRequest: func(ctx context.Context, params *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
					promptShown = true
					if tt.userAction == "" {
						return nil, nil
					}
					return &protocol.MessageActionItem{Title: tt.userAction}, nil
				},
			}
			s := &server{
				client:   client,
				session:  cache.NewSession(ctx, cache.New(nil)),
				progress: progress.NewTracker(client),
				options: &settings.Options{
					UserOptions: settings.UserOptions{
						UIOptions: settings.UIOptions{
							DiagnosticOptions: settings.DiagnosticOptions{
								Vulncheck: tt.vulncheckMode,
							},
						},
					},
				},
			}
			dir := t.TempDir()
			goModPath := filepath.Join(dir, "go.mod")
			if err := os.WriteFile(goModPath, []byte(tt.oldContent), 0644); err != nil {
				t.Fatal(err)
			}
			uri := protocol.URIFromPath(goModPath)

			// Set the initial hash in the cache.
			oldModFile, err := modfile.Parse("go.mod", []byte(tt.oldContent), nil)
			if err != nil {
				t.Fatal(err)
			}
			oldHash, err := computeGoModHash(oldModFile)
			if err != nil {
				t.Fatal(err)
			}
			pathHash := sha256.Sum256([]byte(uri.Path()))
			if err := filecache.Set(goModHashKind, pathHash, []byte(oldHash)); err != nil {
				t.Fatal(err)
			}

			// Simulate the file change.
			if err := os.WriteFile(goModPath, []byte(tt.newContent), 0644); err != nil {
				t.Fatal(err)
			}

			s.checkGoModDeps(ctx, uri)

			if promptShown != tt.wantPrompt {
				t.Errorf("promptShown = %v, want %v", promptShown, tt.wantPrompt)
			}

			// Check if the hash was updated.
			newModFile, err := modfile.Parse("go.mod", []byte(tt.newContent), nil)
			if err != nil {
				t.Fatal(err)
			}
			newHash, err := computeGoModHash(newModFile)
			if err != nil {
				t.Fatal(err)
			}

			cachedHashBytes, err := filecache.Get(goModHashKind, pathHash)
			if err != nil && err != filecache.ErrNotFound {
				t.Fatal(err)
			}
			cachedHash := string(cachedHashBytes)

			if tt.wantHashUpdated {
				if cachedHash != newHash {
					t.Errorf("hash was not updated in cache")
				}
			} else {
				if cachedHash == newHash && oldHash != newHash {
					t.Errorf("hash was updated in cache, but should not have been")
				}
			}
		})
	}
}

func TestVulncheckPreference(t *testing.T) {
	if runtime.GOARCH == "wasm" {
		t.Skip("test not supported in wasm")
	}
	t.Cleanup(func() {
		configDir, err := os.UserConfigDir()
		if err != nil {
			t.Fatalf("os.UserConfigDir() failed: %v", err)
		}
		if err := os.RemoveAll(filepath.Join(configDir, "gopls")); err != nil && !os.IsNotExist(err) {
			t.Fatalf("failed to clear user config: %v", err)
		}
	})
	t.Setenv("HOME", t.TempDir())

	pref, err := getVulncheckPreference()
	if err != nil {
		t.Fatalf("getVulncheckPreference() failed: %v", err)
	}
	if pref != "" {
		t.Errorf("got %q, want empty string", pref)
	}

	want := vulncheckActionAlways
	if err := setVulncheckPreference(want); err != nil {
		t.Fatalf("setVulncheckPreference() failed: %v", err)
	}

	got, err := getVulncheckPreference()
	if err != nil {
		t.Fatalf("getVulncheckPreference() failed: %v", err)
	}

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
