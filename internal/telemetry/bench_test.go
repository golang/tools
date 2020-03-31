package telemetry_test

import (
	"context"
	"io/ioutil"
	"log"
	"testing"

	"golang.org/x/tools/internal/telemetry/event"
	"golang.org/x/tools/internal/telemetry/export"
)

type Hooks struct {
	A func(ctx context.Context, a int) (context.Context, func())
	B func(ctx context.Context, b string) (context.Context, func())
}

var (
	aValue  = event.NewIntKey("a", "")
	bValue  = event.NewStringKey("b", "")
	aCount  = event.NewInt64Key("aCount", "Count of time A is called.")
	aStat   = event.NewIntKey("aValue", "A value.")
	bCount  = event.NewInt64Key("B", "Count of time B is called.")
	bLength = event.NewIntKey("BLen", "B length.")

	Baseline = Hooks{
		A: func(ctx context.Context, a int) (context.Context, func()) {
			return ctx, func() {}
		},
		B: func(ctx context.Context, b string) (context.Context, func()) {
			return ctx, func() {}
		},
	}

	StdLog = Hooks{
		A: func(ctx context.Context, a int) (context.Context, func()) {
			log.Printf("A where a=%d", a)
			return ctx, func() {}
		},
		B: func(ctx context.Context, b string) (context.Context, func()) {
			log.Printf("B where b=%q", b)
			return ctx, func() {}
		},
	}

	Log = Hooks{
		A: func(ctx context.Context, a int) (context.Context, func()) {
			event.Print1(ctx, "A", aValue.Of(a))
			return ctx, func() {}
		},
		B: func(ctx context.Context, b string) (context.Context, func()) {
			event.Print1(ctx, "B", bValue.Of(b))
			return ctx, func() {}
		},
	}

	Trace = Hooks{
		A: func(ctx context.Context, a int) (context.Context, func()) {
			return event.StartSpan1(ctx, "A", aValue.Of(a))
		},
		B: func(ctx context.Context, b string) (context.Context, func()) {
			return event.StartSpan1(ctx, "B", bValue.Of(b))
		},
	}

	Stats = Hooks{
		A: func(ctx context.Context, a int) (context.Context, func()) {
			event.Record1(ctx, aStat.Of(a))
			event.Record1(ctx, aCount.Of(1))
			return ctx, func() {}
		},
		B: func(ctx context.Context, b string) (context.Context, func()) {
			event.Record1(ctx, bLength.Of(len(b)))
			event.Record1(ctx, bCount.Of(1))
			return ctx, func() {}
		},
	}

	initialList = []int{0, 1, 22, 333, 4444, 55555, 666666, 7777777}
	stringList  = []string{
		"A value",
		"Some other value",
		"A nice longer value but not too long",
		"V",
		"",
		"ı",
		"prime count of values",
	}
)

type namedBenchmark struct {
	name string
	test func(*testing.B)
}

func Benchmark(b *testing.B) {
	b.Run("Baseline", Baseline.runBenchmark)
	b.Run("StdLog", StdLog.runBenchmark)
	benchmarks := []namedBenchmark{
		{"Log", Log.runBenchmark},
		{"Trace", Trace.runBenchmark},
		{"Stats", Stats.runBenchmark},
	}

	event.SetExporter(nil)
	for _, t := range benchmarks {
		b.Run(t.name+"NoExporter", t.test)
	}

	event.SetExporter(noopExporter)
	for _, t := range benchmarks {
		b.Run(t.name+"Noop", t.test)
	}

	event.SetExporter(export.Spans(export.LogWriter(ioutil.Discard, false)))
	for _, t := range benchmarks {
		b.Run(t.name, t.test)
	}
}

func A(ctx context.Context, hooks Hooks, a int) int {
	ctx, done := hooks.A(ctx, a)
	defer done()
	return B(ctx, hooks, a, stringList[a%len(stringList)])
}

func B(ctx context.Context, hooks Hooks, a int, b string) int {
	_, done := hooks.B(ctx, b)
	defer done()
	return a + len(b)
}

func (hooks Hooks) runBenchmark(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	var acc int
	for i := 0; i < b.N; i++ {
		for _, value := range initialList {
			acc += A(ctx, hooks, value)
		}
	}
}

func init() {
	log.SetOutput(ioutil.Discard)
}

func noopExporter(ctx context.Context, ev event.Event, tagMap event.TagMap) context.Context {
	return ctx
}
