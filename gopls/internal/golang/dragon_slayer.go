// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file defines the "Slay the dragon" code action, a toy adventure game
// demonstrating interactive refactoring. See [Dialog].
//
// The slayer starts at the village gate and has 6 moves to reach the
// dragon's lair, one step north-east. A sword lies one step north; on
// finding it, the slayer may pick it up. In the lair, the slayer chooses how
// to attack (with the sword only if armed) and where to strike. The dragon
// has a single weak spot, revealed by the engraving on the sword.
//
// The game keeps no state: every round of "command/resolve" replays all the
// answers so far from the start.

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
)

// dragonPos is a position on the map: x grows eastward, y grows northward.
type dragonPos struct{ x, y int }

var (
	dragonSword = dragonPos{0, 1} // north of the gate at (0, 0)
	dragonLair  = dragonPos{1, 1} // east of the sword
)

var (
	// DragonMoveQuestion asks which way the slayer goes next.
	DragonMoveQuestion = formQuestion[string, dragonPos]{
		ID:          "move",
		Description: "Which way? The dragon's roar echoes from the north-east.",
		Required:    true,
		Types: []any{protocol.FormFieldTypeEnum{
			Kind: protocol.FormFieldKindEnum,
			Entries: []protocol.FormEnumEntry{
				{Value: "north", Description: "Go North"},
				{Value: "south", Description: "Go South"},
				{Value: "east", Description: "Go East"},
				{Value: "west", Description: "Go West"},
			},
		}},
		convert: func(answer string) (dragonPos, error) {
			switch answer {
			case "north":
				return dragonPos{0, 1}, nil
			case "south":
				return dragonPos{0, -1}, nil
			case "east":
				return dragonPos{1, 0}, nil
			case "west":
				return dragonPos{-1, 0}, nil
			}
			return dragonPos{}, fmt.Errorf("you cannot %q here", answer)
		},
	}

	// DragonPickupSwordQuestion asks whether the slayer picks up the sword.
	DragonPickupSwordQuestion = formQuestion[string, bool]{
		ID:          "pickup",
		Description: `A sword lies on the ground. Its blade is engraved: "dragonSlayer. Strike the belly."`,
		Required:    true,
		Types: []any{protocol.FormFieldTypeEnum{
			Kind: protocol.FormFieldKindEnum,
			Entries: []protocol.FormEnumEntry{
				{Value: "yes", Description: "Pick up the sword"},
				{Value: "no", Description: "Leave it"},
			},
		}},
		convert: func(s string) (bool, error) {
			switch s {
			case "yes":
				return true, nil
			case "no":
				return false, nil
			}
			return false, fmt.Errorf("pickup doesn't accept answer: %s", s)
		},
	}

	// DragonArmedAttackQuestion asks an armed slayer how to attack.
	DragonArmedAttackQuestion = formQuestion[string, string]{
		ID:          "attack",
		Description: "You are in the dragon's lair! How do you attack?",
		Required:    true,
		Types: []any{protocol.FormFieldTypeEnum{
			Kind: protocol.FormFieldKindEnum,
			Entries: []protocol.FormEnumEntry{
				{Value: "sword", Description: "With the sword"},
				{Value: "fists", Description: "With your bare hands"},
			},
		}},
	}

	// DragonUnarmedAttackQuestion asks an unarmed slayer how to attack.
	DragonUnarmedAttackQuestion = formQuestion[string, string]{
		ID:          "attack",
		Description: "You are in the dragon's lair! (If only you had a sword...) How do you attack?",
		Required:    true,
		Types: []any{protocol.FormFieldTypeEnum{
			Kind: protocol.FormFieldKindEnum,
			Entries: []protocol.FormEnumEntry{
				{Value: "fists", Description: "With your bare hands"},
			},
		}},
	}

	// DragonTargetQuestion asks where the slayer strikes.
	DragonTargetQuestion = formQuestion[string, string]{
		ID:          "target",
		Description: "Where do you strike?",
		Required:    true,
		Types: []any{protocol.FormFieldTypeEnum{
			Kind: protocol.FormFieldKindEnum,
			Entries: []protocol.FormEnumEntry{
				{Value: "head", Description: "The head"},
				{Value: "wing", Description: "A wing"},
				{Value: "belly", Description: "The belly"},
			},
		}},
	}

	dragonSlayerQuestions = []question{
		DragonMoveQuestion, DragonPickupSwordQuestion,
		DragonArmedAttackQuestion, DragonUnarmedAttackQuestion, DragonTargetQuestion,
	}
)

// DragonSlayer plays the game, asking the questions through d.
//
// It returns [command.ErrPendingAnswer] (or a client protocol error) while
// the game is still in progress. Once the game is over, it reports whether
// the dragon was slain, and a message describing the outcome.
func DragonSlayer(d *Dialog) (won bool, msg string, err error) {
	var (
		pos   dragonPos // the village gate
		armed bool
	)
	for moves := 0; pos != dragonLair; moves++ {
		if moves == 6 { // maximum steps the slayer can move
			return false, "You wandered for too long, and the dragon flew away.", nil
		}
		delta := d.Ask(DragonMoveQuestion)
		if err := d.Check(); err != nil {
			return false, "", err
		}
		pos = dragonPos{pos.x + delta.x, pos.y + delta.y}

		if pos == dragonSword && !armed {
			armed = d.Ask(DragonPickupSwordQuestion)
			if err := d.Check(); err != nil {
				return false, "", err
			}
		}
	}

	var attackMethod string
	if armed {
		attackMethod = d.Ask(DragonArmedAttackQuestion)
	} else {
		attackMethod = d.Ask(DragonUnarmedAttackQuestion)
	}
	attackTarget := d.Ask(DragonTargetQuestion)
	if err := d.Check(); err != nil {
		return false, "", err
	}

	switch {
	case attackMethod == "fists":
		return false, fmt.Sprintf("You punch the dragon's %s. It is not impressed, and has you for lunch.", attackTarget), nil
	case attackTarget != "belly":
		return false, fmt.Sprintf("Your sword glances off the dragon's %s. It has you for lunch.", attackTarget), nil
	default:
		return true, "Victory! The dragon is slain.", nil
	}
}

// goplsDragonSlayer produces the "Slay the dragon" code action, offered only
// on the name of a package-level variable named dragonSlayer.
// See [server.commandHandler.DragonSlayer] for command implementation.
func goplsDragonSlayer(_ context.Context, req *codeActionsRequest) error {
	if !supportsDialog(req.snapshot.Options().ClientOptions, dragonSlayerQuestions) {
		return nil
	}
	for _, decl := range req.pgf.File.Decls {
		decl, ok := decl.(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR {
			continue
		}
		for _, spec := range decl.Specs {
			for _, id := range spec.(*ast.ValueSpec).Names {
				if id.Name == "dragonSlayer" && id.Pos() <= req.start && req.end <= id.End() {
					cmd := command.NewDragonSlayerCommand("Slay the dragon", command.DragonSlayerArgs{Location: req.loc})
					req.addCommandAction(cmd, false)
					return nil
				}
			}
		}
	}
	return nil
}
