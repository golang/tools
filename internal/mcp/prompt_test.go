// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/mcp"
)

// testPromptHandler is used for type inference in TestNewPrompt.
func testPromptHandler[T any](context.Context, *mcp.ServerSession, T, *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	panic("not implemented")
}

func TestNewPrompt(t *testing.T) {
	tests := []struct {
		prompt *mcp.ServerPrompt
		want   []*mcp.PromptArgument
	}{
		{
			mcp.NewPrompt("empty", "", testPromptHandler[struct{}]),
			nil,
		},
		{
			mcp.NewPrompt("add_arg", "", testPromptHandler[struct{}], mcp.Argument("x")),
			[]*mcp.PromptArgument{{Name: "x"}},
		},
		{
			mcp.NewPrompt("combo", "", testPromptHandler[struct {
				Name    string `json:"name"`
				Country string `json:"country,omitempty"`
				State   string
			}],
				mcp.Argument("name", mcp.Description("the person's name")),
				mcp.Argument("State", mcp.Required(false))),
			[]*mcp.PromptArgument{
				{Name: "State"},
				{Name: "country"},
				{Name: "name", Required: true, Description: "the person's name"},
			},
		},
	}
	for _, test := range tests {
		if diff := cmp.Diff(test.want, test.prompt.Prompt.Arguments); diff != "" {
			t.Errorf("NewPrompt(%v) mismatch (-want +got):\n%s", test.prompt.Prompt.Name, diff)
		}
	}
}
