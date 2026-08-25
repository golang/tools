package testingcontext

import (
	"context"

	"testing"
)

func Test(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()) // want "context.WithCancel can be modernized using t.Context"
	defer cancel()
	_ = ctx

	func() {
		ctx, cancel := context.WithCancel(context.Background()) // Nope. scope of defer is not the testing func.
		defer cancel()
		_ = ctx
	}()
}

func TestScope(t *testing.T) {
	{
		ctx, cancel := context.WithCancel(context.TODO()) // want "context.WithCancel can be modernized using t.Context"
		defer cancel()
		_ = ctx
		var t int // not in scope of the call to WithCancel
		_ = t
	}
}

func TestRedeclared(t *testing.T) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(context.Background()) // Nope. ctx is redeclared.
	defer cancel()
	_ = ctx
}

func TestShadowed(t *testing.T) {
	{
		var t int
		ctx, cancel := context.WithCancel(context.Background()) // Nope. t is shadowed.
		defer cancel()
		_ = ctx
		_ = t
	}

	t.Run("subtest", func(t2 *testing.T) {
		ctx, cancel := context.WithCancel(context.Background()) // want "context.WithCancel can be modernized using t2.Context"
		defer cancel()
		_ = ctx
	})
}

func TestAlt(t2 *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()) // want "context.WithCancel can be modernized using t2.Context"
	defer cancel()
	_ = ctx
}

func Testnot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()) // Nope. Not a test func.
	defer cancel()
	_ = ctx
}

func Benchmark(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background()) // want "context.WithCancel can be modernized using b.Context"
	defer cancel()
	_ = ctx

	b.Run("subtest", func(b2 *testing.B) {
		ctx, cancel := context.WithCancel(context.Background()) // want "context.WithCancel can be modernized using b2.Context"
		defer cancel()
		_ = ctx
	})
}

func Fuzz(f *testing.F) {
	ctx, cancel := context.WithCancel(context.Background()) // want "context.WithCancel can be modernized using f.Context"
	defer cancel()
	_ = ctx
}

func TestDeferredFuncsBefore(t *testing.T) {
	x := 1
	defer func() {
		x++
	}()
	ctx, cancel := context.WithCancel(context.Background()) // Nope. Deferred call before context.WithCancel
	defer cancel()
	_ = ctx
}

func TestDeferredFuncsNested(t *testing.T) {
	x := 1
	if x == 1 {
		defer func() {
			x++
		}()
	}
	ctx, cancel := context.WithCancel(context.Background()) // Nope. Deferred call before context.WithCancel
	defer cancel()
	_ = ctx
}

func TestDeferredFuncsAfter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background()) // want "context.WithCancel can be modernized using t.Context"
	defer cancel()
	_ = ctx
	x := 1
	defer func() {
		x++
	}()
}

func TestDeferredFuncsInClosure(t *testing.T) {
	x := 1
	f := func() {
		defer func() { // defer before context.WithCancel, but within func lit, is fine
			x++
		}()
	}
	f()
	ctx, cancel := context.WithCancel(context.Background()) // want "context.WithCancel can be modernized using t.Context"
	defer cancel()
	_ = ctx
}
