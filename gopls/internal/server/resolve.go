// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"errors"

	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/internal/event"
)

// This file contains the code to mediate user dialogs in the client.

// ResolveCommand implements the interactive resolution step for workspace commands.
//
// For full details on the interactive protocol, the multi-step handshake, and the
// conditions under which the parameter is modified or returned as-is, see
// [protocol.InteractiveParams] and [protocol.Server.ResolveCommand].
func (s *server) ResolveCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (*protocol.ExecuteCommandParams, error) {
	ctx, done := event.Start(ctx, "server.ResolveCommand")
	defer done()

	if !command.Command(params.Command).Interactive() {
		return params, nil // the command asks nothing
	}

	handler := &commandHandler{
		s:      s,
		params: params,
	}
	if _, err := command.Dispatch(ctx, params, handler); err != nil {
		if !errors.Is(err, command.ErrPendingAnswer) {
			return nil, err // real failure
		}
		return params, nil // params holds the questions, and the answers so far
	}

	// An empty form tells the client that the interactive phase is over.
	params.FormFields = nil
	return params, nil
}
