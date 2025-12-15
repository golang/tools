// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package label provides common labels used to annotate gopls log messages
// and events.
package label

import "golang.org/x/tools/internal/event/keys"

var (
	File      = keys.NewString("file", "")
	Directory = keys.New("directory", "")
	URI       = keys.New("URI", "")
	Package   = keys.NewString("package", "") // sorted comma-separated list of Package IDs
	Query     = keys.New("query", "")
	ViewID    = keys.NewString("view_id", "")
	Snapshot  = keys.NewUInt64("snapshot", "")
	Operation = keys.NewString("operation", "")
	Duration  = keys.New("duration", "Elapsed time")

	Position     = keys.New("position", "")
	PackageCount = keys.NewInt("packages", "")
	Files        = keys.New("files", "")
	Port         = keys.NewInt("port", "")

	NewServer = keys.NewString("new_server", "A new server was added")
	EndServer = keys.NewString("end_server", "A server was shut down")

	ServerID     = keys.NewString("server", "The server ID an event is related to")
	Logfile      = keys.NewString("logfile", "")
	DebugAddress = keys.NewString("debug_address", "")
	GoplsPath    = keys.NewString("gopls_path", "")
	ClientID     = keys.NewString("client_id", "")

	Level = keys.NewInt("level", "The logging level")
)
