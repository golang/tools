// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"golang.org/x/telemetry/counter"
	"golang.org/x/telemetry/counter/countertest"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/server"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

// Test prompt file in old and new formats are handled as expected.
func TestTelemetryPrompt_PromptFile(t *testing.T) {
	const src = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

func main() {}
`

	defaultTelemetryStartTime := "1714521600" // 2024-05-01
	defaultToken := "7"
	samplesPerMille := "500"

	testCases := []struct {
		name, in, want string
		wantPrompt     bool
	}{
		{
			name:       "empty",
			in:         "",
			want:       "failed 1 1714521600 7",
			wantPrompt: true,
		},
		{
			name:       "v0.15-format/invalid",
			in:         "pending",
			want:       "failed 1 1714521600 7",
			wantPrompt: true,
		},
		{
			name:       "v0.15-format/pPending",
			in:         "pending 1",
			want:       "failed 2 1714521600 7",
			wantPrompt: true,
		},
		{
			name:       "v0.15-format/pPending",
			in:         "failed 1",
			want:       "failed 2 1714521600 7",
			wantPrompt: true,
		},
		{
			name: "v0.15-format/pYes",
			in:   "yes 1",
			want: "yes 1", // untouched since short-circuited
		},
		{
			name: "v0.16-format/pNotReady",
			in:   "- 0 1714521600 1000",
			want: "- 0 1714521600 1000",
		},
		{
			name:       "v0.16-format/pPending",
			in:         "pending 1 1714521600 1",
			want:       "failed 2 1714521600 1",
			wantPrompt: true,
		},
		{
			name:       "v0.16-format/pFailed",
			in:         "failed 2 1714521600 1",
			want:       "failed 3 1714521600 1",
			wantPrompt: true,
		},
		{
			name:       "v0.16-format/invalid",
			in:         "xxx 0 12345 678",
			want:       "failed 1 1714521600 7",
			wantPrompt: true,
		},
		{
			name: "v0.16-format/extra",
			in:   "- 0 1714521600 1000 7777 xxx",
			want: "- 0 1714521600 1000", // drop extra
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			modeFile := filepath.Join(t.TempDir(), "mode")
			goplsConfigDir := t.TempDir()
			promptDir := filepath.Join(goplsConfigDir, "prompt")
			promptFile := filepath.Join(promptDir, "telemetry")

			if err := os.MkdirAll(promptDir, 0777); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(promptFile, []byte(tc.in), 0666); err != nil {
				t.Fatal(err)
			}
			WithOptions(
				Modes(Default), // no need to run this in all modes
				EnvVars{
					server.GoplsConfigDirEnvvar:                  goplsConfigDir,
					server.FakeTelemetryModefileEnvvar:           modeFile,
					server.GoTelemetryGoplsClientStartTimeEnvvar: defaultTelemetryStartTime,
					server.GoTelemetryGoplsClientTokenEnvvar:     defaultToken,
					server.FakeSamplesPerMille:                   samplesPerMille,
				},
				Settings{
					"telemetryPrompt": true,
				},
			).Run(t, src, func(t *testing.T, env *Env) {
				expectation := ShownMessageRequest(".*Would you like to enable Go telemetry?")
				if !tc.wantPrompt {
					expectation = Not(expectation)
				}
				env.OnceMet(
					CompletedWork(server.TelemetryPromptWorkTitle, 1, true),
					expectation,
				)
				if got, err := os.ReadFile(promptFile); err != nil || string(got) != tc.want {
					t.Fatalf("(%q) -> (%q, %v), want %q", tc.in, got, err, tc.want)
				}
			})
		})
	}
}

// Test that gopls prompts for telemetry only when it is supposed to.
func TestTelemetryPrompt_Conditions_Mode(t *testing.T) {
	const src = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

func main() {
}
`

	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("telemetryPrompt=%v", enabled), func(t *testing.T) {
			for _, initialMode := range []string{"", "local", "off", "on"} {
				t.Run(fmt.Sprintf("initial_mode=%s", initialMode), func(t *testing.T) {
					modeFile := filepath.Join(t.TempDir(), "mode")
					if initialMode != "" {
						if err := os.WriteFile(modeFile, []byte(initialMode), 0666); err != nil {
							t.Fatal(err)
						}
					}
					telemetryStartTime := time.Now().Add(-8 * 24 * time.Hour) // telemetry started a while ago
					WithOptions(
						Modes(Default), // no need to run this in all modes
						EnvVars{
							server.GoplsConfigDirEnvvar:                  t.TempDir(),
							server.FakeTelemetryModefileEnvvar:           modeFile,
							server.GoTelemetryGoplsClientStartTimeEnvvar: strconv.FormatInt(telemetryStartTime.Unix(), 10),
							server.GoTelemetryGoplsClientTokenEnvvar:     "1", // always sample because samplingPerMille >= 1.
						},
						Settings{
							"telemetryPrompt": enabled,
						},
					).Run(t, src, func(t *testing.T, env *Env) {
						wantPrompt := enabled && (initialMode == "" || initialMode == "local")
						expectation := ShownMessageRequest(".*Would you like to enable Go telemetry?")
						if !wantPrompt {
							expectation = Not(expectation)
						}
						env.OnceMet(
							CompletedWork(server.TelemetryPromptWorkTitle, 1, true),
							expectation,
						)
					})
				})
			}
		})
	}
}

// Test that gopls prompts for telemetry only after instrumenting for a while, and
// when the token is within the range for sample.
func TestTelemetryPrompt_Conditions_StartTimeAndSamplingToken(t *testing.T) {
	const src = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

func main() {
}
`
	day := 24 * time.Hour
	samplesPerMille := 50
	for _, token := range []int{1, samplesPerMille, samplesPerMille + 1} {
		wantSampled := token <= samplesPerMille
		t.Run(fmt.Sprintf("to_sample=%t/tokens=%d", wantSampled, token), func(t *testing.T) {
			for _, elapsed := range []time.Duration{8 * day, 1 * day, 0} {
				telemetryStartTimeOrEmpty := ""
				if elapsed > 0 {
					telemetryStartTimeOrEmpty = strconv.FormatInt(time.Now().Add(-elapsed).Unix(), 10)
				}
				t.Run(fmt.Sprintf("elapsed=%s", elapsed), func(t *testing.T) {
					modeFile := filepath.Join(t.TempDir(), "mode")
					WithOptions(
						Modes(Default), // no need to run this in all modes
						EnvVars{
							server.GoplsConfigDirEnvvar:                  t.TempDir(),
							server.FakeTelemetryModefileEnvvar:           modeFile,
							server.GoTelemetryGoplsClientStartTimeEnvvar: telemetryStartTimeOrEmpty,
							server.GoTelemetryGoplsClientTokenEnvvar:     strconv.Itoa(token),
							server.FakeSamplesPerMille:                   strconv.Itoa(samplesPerMille), // want token ∈ [1, 50] is always sampled.
						},
						Settings{
							"telemetryPrompt": true,
						},
					).Run(t, src, func(t *testing.T, env *Env) {
						wantPrompt := wantSampled && elapsed > 7*day
						expectation := ShownMessageRequest(".*Would you like to enable Go telemetry?")
						if !wantPrompt {
							expectation = Not(expectation)
						}
						env.OnceMet(
							CompletedWork(server.TelemetryPromptWorkTitle, 1, true),
							expectation,
						)
					})
				})
			}
		})
	}
}

// Test that responding to the telemetry prompt results in the expected state.
func TestTelemetryPrompt_Response(t *testing.T) {
	if !countertest.SupportedPlatform {
		t.Skip("requires counter support")
	}

	const src = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

func main() {
}
`

	acceptanceCounterName := "gopls/telemetryprompt/accepted"
	acceptanceCounter := counter.New(acceptanceCounterName)
	// We must increment the acceptance counter in order for the initial read
	// below to succeed.
	//
	// TODO(rfindley): ReadCounter should simply return 0 for uninitialized
	// counters.
	acceptanceCounter.Inc()

	tests := []struct {
		name     string // subtest name
		response string // response to choose for the telemetry dialog
		wantMode string // resulting telemetry mode
		wantMsg  string // substring contained in the follow-up popup (if empty, no popup is expected)
		wantInc  uint64 // expected 'prompt accepted' counter increment
	}{
		{"yes", server.TelemetryYes, "on", "uploading is now enabled", 1},
		{"no", server.TelemetryNo, "", "", 0},
		{"empty", "", "", "", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initialCount, err := countertest.ReadCounter(acceptanceCounter)
			if err != nil {
				t.Fatalf("ReadCounter(%q) failed: %v", acceptanceCounterName, err)
			}
			modeFile := filepath.Join(t.TempDir(), "mode")
			telemetryStartTime := time.Now().Add(-8 * 24 * time.Hour)
			msgRE := regexp.MustCompile(".*Would you like to enable Go telemetry?")
			respond := func(m *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
				if msgRE.MatchString(m.Message) {
					for _, item := range m.Actions {
						if item.Title == test.response {
							return &item, nil
						}
					}
					if test.response != "" {
						t.Errorf("action item %q not found", test.response)
					}
				}
				return nil, nil
			}
			WithOptions(
				Modes(Default), // no need to run this in all modes
				EnvVars{
					server.GoplsConfigDirEnvvar:                  t.TempDir(),
					server.FakeTelemetryModefileEnvvar:           modeFile,
					server.GoTelemetryGoplsClientStartTimeEnvvar: strconv.FormatInt(telemetryStartTime.Unix(), 10),
					server.GoTelemetryGoplsClientTokenEnvvar:     "1", // always sample because samplingPerMille >= 1.
				},
				Settings{
					"telemetryPrompt": true,
				},
				MessageResponder(respond),
			).Run(t, src, func(t *testing.T, env *Env) {
				var postConditions []Expectation
				if test.wantMsg != "" {
					postConditions = append(postConditions, ShownMessage(test.wantMsg))
				}
				env.OnceMet(
					CompletedWork(server.TelemetryPromptWorkTitle, 1, true),
					postConditions...,
				)
				gotMode := ""
				if contents, err := os.ReadFile(modeFile); err == nil {
					gotMode = string(contents)
				} else if !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if gotMode != test.wantMode {
					t.Errorf("after prompt, mode=%s, want %s", gotMode, test.wantMode)
				}
				finalCount, err := countertest.ReadCounter(acceptanceCounter)
				if err != nil {
					t.Fatalf("ReadCounter(%q) failed: %v", acceptanceCounterName, err)
				}
				if gotInc := finalCount - initialCount; gotInc != test.wantInc {
					t.Errorf("%q mismatch: got %d, want %d", acceptanceCounterName, gotInc, test.wantInc)
				}
			})
		})
	}
}

// Test that we stop asking about telemetry after the user ignores the question
// 5 times.
func TestTelemetryPrompt_GivingUp(t *testing.T) {
	const src = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

func main() {
}
`

	// For this test, we want to share state across gopls sessions.
	modeFile := filepath.Join(t.TempDir(), "mode")
	telemetryStartTime := time.Now().Add(-30 * 24 * time.Hour)
	configDir := t.TempDir()

	const maxPrompts = 5 // internal prompt limit defined by gopls

	for i := 0; i < maxPrompts+1; i++ {
		WithOptions(
			Modes(Default), // no need to run this in all modes
			EnvVars{
				server.GoplsConfigDirEnvvar:                  configDir,
				server.FakeTelemetryModefileEnvvar:           modeFile,
				server.GoTelemetryGoplsClientStartTimeEnvvar: strconv.FormatInt(telemetryStartTime.Unix(), 10),
				server.GoTelemetryGoplsClientTokenEnvvar:     "1", // always sample because samplingPerMille >= 1.
			},
			Settings{
				"telemetryPrompt": true,
			},
		).Run(t, src, func(t *testing.T, env *Env) {
			wantPrompt := i < maxPrompts
			expectation := ShownMessageRequest(".*Would you like to enable Go telemetry?")
			if !wantPrompt {
				expectation = Not(expectation)
			}
			env.OnceMet(
				CompletedWork(server.TelemetryPromptWorkTitle, 1, true),
				expectation,
			)
		})
	}
}

// Test that gopls prompts for telemetry only when it is supposed to.
func TestTelemetryPrompt_Conditions_Command(t *testing.T) {
	const src = `
-- go.mod --
module mod.com

go 1.12
-- main.go --
package main

func main() {
}
`
	modeFile := filepath.Join(t.TempDir(), "mode")
	telemetryStartTime := time.Now().Add(-8 * 24 * time.Hour)
	WithOptions(
		Modes(Default), // no need to run this in all modes
		EnvVars{
			server.GoplsConfigDirEnvvar:                  t.TempDir(),
			server.FakeTelemetryModefileEnvvar:           modeFile,
			server.GoTelemetryGoplsClientStartTimeEnvvar: fmt.Sprintf("%d", telemetryStartTime.Unix()),
			server.GoTelemetryGoplsClientTokenEnvvar:     "1", // always sample because samplingPerMille >= 1.
		},
		Settings{
			// off because we are testing
			// if we can trigger the prompt with command.
			"telemetryPrompt": false,
		},
	).Run(t, src, func(t *testing.T, env *Env) {
		cmd, err := command.NewMaybePromptForTelemetryCommand("prompt")
		if err != nil {
			t.Fatal(err)
		}
		var result error
		env.ExecuteCommand(&protocol.ExecuteCommandParams{
			Command: cmd.Command,
		}, &result)
		if result != nil {
			t.Fatal(err)
		}
		expectation := ShownMessageRequest(".*Would you like to enable Go telemetry?")
		env.OnceMet(
			CompletedWork(server.TelemetryPromptWorkTitle, 2, true),
			expectation,
		)
	})
}
