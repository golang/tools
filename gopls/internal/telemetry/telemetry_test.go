// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.21 && !openbsd && !js && !wasip1 && !solaris && !android && !386
// +build go1.21,!openbsd,!js,!wasip1,!solaris,!android,!386

package telemetry_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/telemetry/counter"
	"golang.org/x/telemetry/counter/countertest" // requires go1.21+
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/telemetry"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/util/bug"
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "gopls-telemetry-test-counters")
	if err != nil {
		panic(err)
	}
	countertest.Open(tmp)
	code := Main(m)
	os.RemoveAll(tmp) // golang/go#68243: ignore error; cleanup fails on Windows
	os.Exit(code)
}

func TestTelemetry(t *testing.T) {
	var (
		goversion = ""
		editor    = "vscode" // We set ClientName("Visual Studio Code") below.
	)

	// Run gopls once to determine the Go version.
	WithOptions(
		Modes(Default),
	).Run(t, "", func(_ *testing.T, env *Env) {
		goversion = strconv.Itoa(env.GoVersion())
	})

	// counters that should be incremented once per session
	sessionCounters := []*counter.Counter{
		counter.New("gopls/client:" + editor),
		counter.New("gopls/goversion:1." + goversion),
		counter.New("fwd/vscode/linter:a"),
		counter.New("gopls/gotoolchain:local"),
	}
	initialCounts := make([]uint64, len(sessionCounters))
	for i, c := range sessionCounters {
		count, err := countertest.ReadCounter(c)
		if err != nil {
			continue // counter db not open, or counter not found
		}
		initialCounts[i] = count
	}

	// Verify that a properly configured session gets notified of a bug on the
	// server.
	WithOptions(
		Modes(Default), // must be in-process to receive the bug report below
		Settings{"showBugReports": true},
		ClientName("Visual Studio Code"),
		EnvVars{
			"GOTOOLCHAIN": "local", // so that the local counter is incremented
		},
	).Run(t, "", func(_ *testing.T, env *Env) {
		goversion = strconv.Itoa(env.GoVersion())
		addForwardedCounters(env, []string{"vscode/linter:a"}, []int64{1})
		const desc = "got a bug"

		// This will increment a counter named something like:
		//
		// `gopls/bug
		// golang.org/x/tools/gopls/internal/util/bug.report:+35
		// golang.org/x/tools/gopls/internal/util/bug.Report:=68
		// golang.org/x/tools/gopls/internal/telemetry_test.TestTelemetry.func2:+4
		// golang.org/x/tools/gopls/internal/test/integration.(*Runner).Run.func1:+87
		// testing.tRunner:+150
		// runtime.goexit:+0`
		//
		bug.Report(desc) // want a stack counter with the trace starting from here.

		env.Await(ShownMessage(desc))
	})

	// gopls/editor:client
	// gopls/goversion:1.x
	// fwd/vscode/linter:a
	// gopls/gotoolchain:local
	for i, c := range sessionCounters {
		want := initialCounts[i] + 1
		got, err := countertest.ReadCounter(c)
		if err != nil || got != want {
			t.Errorf("ReadCounter(%q) = (%v, %v), want (%v, nil)", c.Name(), got, err, want)
			t.Logf("Current timestamp = %v", time.Now().UTC())
		}
	}

	// gopls/bug
	bugcount := bug.BugReportCount
	counts, err := countertest.ReadStackCounter(bugcount)
	if err != nil {
		t.Fatalf("ReadStackCounter(bugreportcount) failed - %v", err)
	}
	if len(counts) != 1 || !hasEntry(counts, t.Name(), 1) {
		t.Errorf("read stackcounter(%q) = (%#v, %v), want one entry", "gopls/bug", counts, err)
		t.Logf("Current timestamp = %v", time.Now().UTC())
	}
}

func TestSettingTelemetry(t *testing.T) {
	// counters that should be incremented by each session
	sessionCounters := []*counter.Counter{
		counter.New("gopls/setting/diagnosticsDelay"),
		counter.New("gopls/setting/staticcheck:true"),
		counter.New("gopls/setting/noSemanticString:true"),
		counter.New("gopls/setting/analyses/deprecated:false"),
	}

	initialCounts := make([]uint64, len(sessionCounters))
	for i, c := range sessionCounters {
		count, err := countertest.ReadCounter(c)
		if err != nil {
			continue // counter db not open, or counter not found
		}
		initialCounts[i] = count
	}

	// Run gopls.
	WithOptions(
		Modes(Default),
		Settings{
			"staticcheck": true,
			"analyses": map[string]bool{
				"deprecated": false,
			},
			"diagnosticsDelay": "0s",
			"noSemanticString": true,
		},
	).Run(t, "", func(_ *testing.T, env *Env) {
	})

	for i, c := range sessionCounters {
		count, err := countertest.ReadCounter(c)
		if err != nil {
			t.Errorf("ReadCounter(%q) failed: %v", c.Name(), err)
			continue
		}
		if count <= initialCounts[i] {
			t.Errorf("ReadCounter(%q) = %d, want > %d", c.Name(), count, initialCounts[i])
		}
	}
}

func addForwardedCounters(env *Env, names []string, values []int64) {
	args, err := command.MarshalArgs(command.AddTelemetryCountersArgs{
		Names: names, Values: values,
	})
	if err != nil {
		env.T.Fatal(err)
	}
	var res error
	env.ExecuteCommand(&protocol.ExecuteCommandParams{
		Command:   command.AddTelemetryCounters.String(),
		Arguments: args,
	}, &res)
	if res != nil {
		env.T.Errorf("%v failed - %v", command.AddTelemetryCounters, res)
	}
}

func hasEntry(counts map[string]uint64, pattern string, want uint64) bool {
	for k, v := range counts {
		if strings.Contains(k, pattern) && v == want {
			return true
		}
	}
	return false
}

func TestLatencyCounter(t *testing.T) {
	const operation = "TestLatencyCounter" // a unique operation name

	stop := telemetry.StartLatencyTimer(operation)
	stop(context.Background(), nil)

	for isError, want := range map[bool]uint64{false: 1, true: 0} {
		if got := totalLatencySamples(t, operation, isError); got != want {
			t.Errorf("totalLatencySamples(operation=%v, isError=%v) = %d, want %d", operation, isError, got, want)
		}
	}
}

func TestLatencyCounter_Error(t *testing.T) {
	const operation = "TestLatencyCounter_Error" // a unique operation name

	stop := telemetry.StartLatencyTimer(operation)
	stop(context.Background(), errors.New("bad"))

	for isError, want := range map[bool]uint64{false: 0, true: 1} {
		if got := totalLatencySamples(t, operation, isError); got != want {
			t.Errorf("totalLatencySamples(operation=%v, isError=%v) = %d, want %d", operation, isError, got, want)
		}
	}
}

func TestLatencyCounter_Cancellation(t *testing.T) {
	const operation = "TestLatencyCounter_Cancellation"

	stop := telemetry.StartLatencyTimer(operation)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stop(ctx, nil)

	for isError, want := range map[bool]uint64{false: 0, true: 0} {
		if got := totalLatencySamples(t, operation, isError); got != want {
			t.Errorf("totalLatencySamples(operation=%v, isError=%v) = %d, want %d", operation, isError, got, want)
		}
	}
}

func totalLatencySamples(t *testing.T, operation string, isError bool) uint64 {
	var total uint64
	telemetry.ForEachLatencyCounter(operation, isError, func(c *counter.Counter) {
		count, err := countertest.ReadCounter(c)
		if err != nil {
			t.Errorf("ReadCounter(%s) failed: %v", c.Name(), err)
		} else {
			total += count
		}
	})
	return total
}

func TestLatencyInstrumentation(t *testing.T) {
	const files = `
-- go.mod --
module mod.test/a
go 1.18
-- a.go --
package a

func _() {
	x := 0
	_ = x
}
`

	// Verify that a properly configured session gets notified of a bug on the
	// server.
	WithOptions(
		Modes(Default), // must be in-process to receive the bug report below
	).Run(t, files, func(_ *testing.T, env *Env) {
		env.OpenFile("a.go")
		before := totalLatencySamples(t, "completion", false)
		loc := env.RegexpSearch("a.go", "x")
		for i := 0; i < 10; i++ {
			env.Completion(loc)
		}
		after := totalLatencySamples(t, "completion", false)
		if after-before < 10 {
			t.Errorf("after 10 completions, completion counter went from %d to %d", before, after)
		}
	})
}
