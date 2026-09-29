// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package misc

import (
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	. "golang.org/x/tools/gopls/internal/test/integration"
)

const dragonSlayerCapabilities = `{"experimental":{"interactiveResolve":{"inputTypes":["enum"]}}}`

const dragonSlayerFiles = `
-- go.mod --
module example.com

go 1.20

-- a.go --
package a

var dragonSlayer = "sword"

var notTheSword int

func _() {
	var dragonSlayer int
	_ = dragonSlayer
}
`

func TestDragonSlayerGame(t *testing.T) {
	for _, test := range []struct {
		name      string
		answers   []string // answers to the questions, in order
		wantDescs []string // substrings of each question's description ("" to skip)
		wantErr   string   // substring of a form field error; the game stops there
		wantWon   bool
		wantMsg   string // substring of the message shown on defeat
	}{
		{
			name:      "win",
			answers:   []string{"north", "yes", "east", "sword", "belly"},
			wantDescs: []string{"north-east", "Strike the belly", "", "How do you attack", "Where"},
			wantWon:   true,
		},
		{
			name:    "detour",
			answers: []string{"east", "west", "north", "yes", "east", "sword", "belly"},
			wantWon: true,
		},
		{
			name:    "wrong spot",
			answers: []string{"north", "yes", "east", "sword", "head"},
			wantMsg: "glances off",
		},
		{
			name:    "fists",
			answers: []string{"north", "yes", "east", "fists", "belly"},
			wantMsg: "not impressed",
		},
		{
			name:      "left the sword",
			answers:   []string{"north", "no", "east", "fists", "belly"},
			wantDescs: []string{"", "", "", "If only you had a sword"},
			wantMsg:   "not impressed",
		},
		{
			name:      "missed the sword",
			answers:   []string{"east", "north", "fists", "belly"},
			wantDescs: []string{"", "", "If only you had a sword"},
			wantMsg:   "not impressed",
		},
		{
			name:    "lost",
			answers: []string{"south", "south", "north", "north", "south", "north"},
			wantMsg: "flew away",
		},
		{
			name:    "invalid",
			answers: []string{"up"},
			wantErr: `cannot "up"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			WithOptions(CapabilitiesJSON([]byte(dragonSlayerCapabilities))).Run(t, dragonSlayerFiles, func(t *testing.T, env *Env) {
				env.OpenFile("a.go")
				actions := env.CodeAction(env.RegexpSearch("a.go", `var (dragonSlayer) =`), nil, 0)
				action, err := CodeActionByKind(actions, settings.GoplsDragonSlayer)
				if err != nil {
					t.Fatal(err)
				}

				// Play the game as a client would: each round, answer
				// every question asked so far; the server replays them
				// and asks the next one.
				params := &protocol.ExecuteCommandParams{
					Command:   action.Command.Command,
					Arguments: action.Command.Arguments,
				}
				var descs []string
			rounds:
				for round := 0; ; round++ {
					res, err := env.Editor.Server.ResolveCommand(env.Ctx, params)
					if err != nil {
						t.Fatalf("ResolveCommand: %v", err)
					}
					params = res
					if len(res.FormFields) == 0 {
						break
					}
					for _, field := range res.FormFields {
						if field.Error != "" {
							if test.wantErr == "" || !strings.Contains(field.Error, test.wantErr) {
								t.Fatalf("question %q: got error %q, want %q", field.ID, field.Error, test.wantErr)
							}
							break rounds
						}
					}
					if round >= 10 {
						t.Fatalf("still asking after 10 rounds")
					}
					res.FormAnswers = nil
					for i, field := range res.FormFields {
						if i >= len(test.answers) {
							t.Fatalf("unexpected question %q: %s", field.ID, field.Description)
						}
						res.FormAnswers = append(res.FormAnswers, protocol.FormAnswer{ID: field.ID, Value: test.answers[i]})
					}
					// A round may ask several new questions at once.
					for _, field := range res.FormFields[len(descs):] {
						descs = append(descs, field.Description)
					}
				}
				for i, want := range test.wantDescs {
					if i < len(descs) && !strings.Contains(descs[i], want) {
						t.Errorf("question %d: got %q, want it to contain %q", i, descs[i], want)
					}
				}
				if test.wantErr != "" {
					return
				}
				if len(descs) != len(test.answers) {
					t.Fatalf("asked %d questions, want %d:\n%s", len(descs), len(test.answers), strings.Join(descs, "\n"))
				}

				listenDocs := env.Awaiter.ListenToShownDocuments()
				if _, err := env.Editor.Server.ExecuteCommand(env.Ctx, params); err != nil {
					t.Fatalf("ExecuteCommand: %v", err)
				}
				// showDocument is a request, so it has arrived by now.
				docs := listenDocs()

				if test.wantWon {
					if len(docs) != 1 || !strings.Contains(docs[0].URI, "slain+dragon") {
						t.Errorf("got shown documents %v, want slain dragon", docs)
					}
				} else {
					if len(docs) != 0 {
						t.Errorf("got shown documents %v, want none", docs)
					}
					// showMessage is a notification, so it may still be in flight.
					env.Await(ShownMessage(test.wantMsg))
				}
			})
		})
	}
}
