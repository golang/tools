// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

// InteractiveListEnum handles requests to dynamically populate interactive UI
// elements in the client. Based on the requested param.Source, it queries the
// underlying session data (like workspace symbols) and returns a list of enum
// entries matching the user's query.
func (s *server) InteractiveListEnum(ctx context.Context, param *protocol.InteractiveListEnumParams) ([]protocol.FormEnumEntry, error) {
	ctx, done := event.Start(ctx, "server.interactiveListEnum")
	defer done()

	switch param.Source {
	case "workspaceSymbol":
		var snapshots []*cache.Snapshot
		for _, v := range s.session.Views() {
			snapshot, release, err := v.Snapshot()
			if err != nil {
				continue // snapshot is shutting down
			}
			// If err is non-nil, the snapshot is shutting down. Skip it.
			defer release()
			snapshots = append(snapshots, snapshot)
		}
		return golang.ListWorkspaceSymbol(ctx, snapshots, param)
	default:
		return nil, fmt.Errorf("unrecognized enum source: %s", param.Source)
	}
}
