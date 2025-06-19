// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
)

// A PromptHandler handles a call to prompts/get.
type PromptHandler func(context.Context, *ServerSession, *GetPromptParams) (*GetPromptResult, error)

// A Prompt is a prompt definition bound to a prompt handler.
type ServerPrompt struct {
	Prompt  *Prompt
	Handler PromptHandler
}
