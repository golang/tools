package server

import "golang.org/x/telemetry/counter"

// Proposed counters for evaluating gopls code completion.
var (
	complCnt   = counter.New("gopls/completion/cnt")   // for Go programs
	complEmpty = counter.New("gopls/completion/empty") // count empty responses
	complLong  = counter.New("gopls/completion/long")  // returning more than 10 items

	changeMulti = counter.New("gopls/completion/multi-change") // multiple changes in didChange
	changeFull  = counter.New("gopls/completion/full-change")  // full file change in didChange

	complUsed = counter.New("gopls/completion/used") // used a completion

	// exported so tests can verify that counters are incrementd
	CompletionCounters = []*counter.Counter{
		complCnt,
		complEmpty,
		complLong,
		changeMulti,
		changeFull,
		complUsed,
	}
)
