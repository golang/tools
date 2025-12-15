// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import "golang.org/x/telemetry/counter"

// Proposed counters for evaluating gopls code completion.
var (
	complEmpty = counter.New("gopls/completion/len:0")    // count empty suggestions
	complShort = counter.New("gopls/completion/len:<=10") // not empty, not long
	complLong  = counter.New("gopls/completion/len:>10")  // returning more than 10 items

	changeFull  = counter.New("gopls/completion/used:unknown") // full file change in didChange
	complUnused = counter.New("gopls/completion/used:no")      // did not use a completion
	complUsed   = counter.New("gopls/completion/used:yes")     // used a completion

	// exported so tests can verify that counters are incremented
	CompletionCounters = []*counter.Counter{
		complEmpty,
		complShort,
		complLong,
		changeFull,
		complUnused,
		complUsed,
	}
)

// Proposed counters for evaluating gopls change signature and rename. These
// counters increment when the user attempts to perform one of these operations,
// regardless of whether it succeeds.
var (
	countChangeSignature = counter.New("gopls/changesig")

	countRename = counter.New("gopls/rename")
)

// Proposed counters for evaluating gopls refactoring codeactions add struct
// tags and remove struct tags.
var (
	countAddStructTags = counter.New("gopls/structtags:add")

	countRemoveStructTags = counter.New("gopls/structtags:remove")
)
