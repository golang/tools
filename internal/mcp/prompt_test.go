// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/mcp/internal/protocol"
)

// testPromptHandler is used for type inference in TestMakePrompt.
func testPromptHandler[T any](context.Context, *mcp.ClientConnection, T) (*protocol.GetPromptResult, error) {
	panic("not implemented")
}

func TestMakePrompt(t *testing.T) {
	tests := []struct {
		prompt *mcp.Prompt
		want   []protocol.PromptArgument
	}{
		{
			mcp.MakePrompt("empty", "", testPromptHandler[struct{}]),
			nil,
		},
		{
			mcp.MakePrompt("add_arg", "", testPromptHandler[struct{}], mcp.Argument("x")),
			[]protocol.PromptArgument{{Name: "x"}},
		},
		{
			mcp.MakePrompt("combo", "", testPromptHandler[struct {
				Name    string `json:"name"`
				Country string `json:"country,omitempty"`
				State   string
			}],
				mcp.Argument("name", mcp.Description("the person's name")),
				mcp.Argument("State", mcp.Required(false))),
			[]protocol.PromptArgument{
				{Name: "State"},
				{Name: "country"},
				{Name: "name", Required: true, Description: "the person's name"},
			},
		},
	}
	for _, test := range tests {
		if diff := cmp.Diff(test.want, test.prompt.Definition.Arguments); diff != "" {
			t.Errorf("MakePrompt(%v) mismatch (-want +got):\n%s", test.prompt.Definition.Name, diff)
		}
	}
}
