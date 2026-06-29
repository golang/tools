// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file exports unexported internal symbols exclusively
// for use in package cmd_test.

package cmd

type (
	DefinitionJSON = definitionJSON
	StatsJSON      = statsJSON
)

// CommandNames returns the names of all commands, including the root command.
func CommandNames() []string {
	var names []string
	app := newApplication()
	for _, c := range app.Commands() {
		names = append(names, c.Name())
	}
	names = append(names, app.Name())
	return names
}
