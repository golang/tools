// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains tests for the slog checker.

//go:build go1.21

package a

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

func F() {
	var (
		l *slog.Logger
		r slog.Record
	)

	// Unrelated call.
	fmt.Println("ok")

	// Valid calls.
	slog.Info("msg")
	slog.Info("msg", "a", 1)
	slog.Info("", "a", 1, "b", "two")
	l.Debug("msg", "a", 1)
	l.With("a", 1)
	slog.Warn("msg", slog.Int("a", 1))
	slog.Warn("msg", slog.Int("a", 1), "k", 2)
	l.WarnContext(nil, "msg", "a", 1, slog.Int("b", 2), slog.Int("c", 3), "d", 4)
	l.DebugContext(nil, "msg", "a", 1, slog.Int("b", 2), slog.Int("c", 3), "d", 4, slog.Int("e", 5))
	r.Add("a", 1, "b", 2)
	(*slog.Logger).Debug(l, "msg", "a", 1, "b", 2)

	var key string
	r.Add(key, 1)

	// bad
	slog.Info("msg", 1)                        // want `slog.Info arg "1" should be a string or a slog.Attr`
	l.Info("msg", 2)                           // want `slog.Logger.Info arg "2" should be a string or a slog.Attr`
	slog.Debug("msg", "a")                     // want `call to slog.Debug missing a final value`
	slog.Warn("msg", slog.Int("a", 1), "k")    // want `call to slog.Warn missing a final value`
	slog.ErrorContext(nil, "msg", "a", 1, "b") // want `call to slog.ErrorContext missing a final value`
	r.Add("K", "v", "k")                       // want `call to slog.Record.Add missing a final value`
	l.With("a", "b", 2)                        // want `slog.Logger.With arg "2" should be a string or a slog.Attr`

	// Report the first problem if there are multiple bad keys.
	slog.Debug("msg", "a", 1, 2, 3, 4) // want `slog.Debug arg "2" should be a string or a slog.Attr`
	slog.Debug("msg", "a", 1, 2, 3, 4) // want `slog.Debug arg "2" should be a string or a slog.Attr`

	slog.Log(nil, slog.LevelWarn, "msg", "a", "b", 2) // want `slog.Log arg "2" should be a string or a slog.Attr`

	// Test method expression call.
	(*slog.Logger).Debug(l, "msg", "a", 1, 2, 3) // want `slog.Logger.Debug arg "2" should be a string or a slog.Attr`

	// Skip calls with spread args.
	var args []any
	slog.Info("msg", args...)

	// Report keys that are statically not exactly "string".
	type MyString string
	myKey := MyString("a")  // any(x) looks like <MyString, "a">.
	slog.Info("", myKey, 1) // want `slog.Info arg "myKey" should be a string or a slog.Attr`

	// The variadic part of all the calls below begins with an argument of
	// static type any, followed by an integer.
	// Even though the we don't know the dynamic type of the first arg, and thus
	// whether it is a key, an Attr, or something else, the fact that the
	// following integer arg cannot be a key allows us to assume that we should
	// expect a key to follow.
	var a any = "key"

	// This is a valid call for which  we correctly produce no diagnostic.
	slog.Info("msg", a, 7, "key2", 5)

	// This is an invalid call because the final value is missing, but we can't
	// be sure that's the reason.
	slog.Info("msg", a, 7, "key2") // want `call to slog.Info has a missing or misplaced value`

	// Here our guess about the unknown arg (a) is wrong: we assume it's a string, but it's an Attr.
	// Therefore the second argument should be a key, but it is a number.
	// Ideally our diagnostic would pinpoint the problem, but we don't have enough information.
	a = slog.Int("a", 1)
	slog.Info("msg", a, 7, "key2") // want `call to slog.Info has a missing or misplaced value`

	// This call is invalid for the same reason as the one above, but we can't
	// detect that.
	slog.Info("msg", a, 7, "key2", 5)

	// Another invalid call we can't detect. Here the first argument is wrong.
	a = 1
	slog.Info("msg", a, 7, "b", 5)

	// We can detect the first case as the type of key is UntypedNil,
	// e.g. not yet assigned to any and not yet an interface.
	// We cannot detect the second.
	slog.Debug("msg", nil, 2) // want `slog.Debug arg "nil" should be a string or a slog.Attr`
	slog.Debug("msg", any(nil), 2)

	// Recovery from unknown value.
	slog.Debug("msg", any(nil), "a")
	slog.Debug("msg", any(nil), "a", 2)
	slog.Debug("msg", any(nil), "a", 2, "b") // want `call to slog.Debug has a missing or misplaced value`
	slog.Debug("msg", any(nil), 2, 3, 4)     // want "slog.Debug arg \\\"3\\\" should probably be a string or a slog.Attr \\(previous arg \\\"2\\\" cannot be a key\\)"

	// In these cases, an argument in key position is an interface, but we can glean useful information about it.

	// An error interface in key position is definitely invalid: it can't be a string
	// or slog.Attr.
	var err error
	slog.Error("msg", err) // want `slog.Error arg "err" should be a string or a slog.Attr`

	// slog.Attr implements fmt.Stringer, but string does not, so assume the arg is an Attr.
	var stringer fmt.Stringer
	slog.Info("msg", stringer, "a", 1)
	slog.Info("msg", stringer, 1) // want `slog.Info arg "1" should be a string or a slog.Attr`
}

func All() {
	// Test all functions and methods at least once.
	var (
		l   *slog.Logger
		r   slog.Record
		ctx context.Context
	)
	slog.Debug("msg", 1, 2) // want `slog.Debug arg "1" should be a string or a slog.Attr`
	slog.Error("msg", 1, 2) // want `slog.Error arg "1" should be a string or a slog.Attr`
	slog.Info("msg", 1, 2)  // want `slog.Info arg "1" should be a string or a slog.Attr`
	slog.Warn("msg", 1, 2)  // want `slog.Warn arg "1" should be a string or a slog.Attr`

	slog.DebugContext(ctx, "msg", 1, 2) // want `slog.DebugContext arg "1" should be a string or a slog.Attr`
	slog.ErrorContext(ctx, "msg", 1, 2) // want `slog.ErrorContext arg "1" should be a string or a slog.Attr`
	slog.InfoContext(ctx, "msg", 1, 2)  // want `slog.InfoContext arg "1" should be a string or a slog.Attr`
	slog.WarnContext(ctx, "msg", 1, 2)  // want `slog.WarnContext arg "1" should be a string or a slog.Attr`

	slog.Log(ctx, slog.LevelDebug, "msg", 1, 2) // want `slog.Log arg "1" should be a string or a slog.Attr`

	l.Debug("msg", 1, 2) // want `slog.Logger.Debug arg "1" should be a string or a slog.Attr`
	l.Error("msg", 1, 2) // want `slog.Logger.Error arg "1" should be a string or a slog.Attr`
	l.Info("msg", 1, 2)  // want `slog.Logger.Info arg "1" should be a string or a slog.Attr`
	l.Warn("msg", 1, 2)  // want `slog.Logger.Warn arg "1" should be a string or a slog.Attr`

	l.DebugContext(ctx, "msg", 1, 2) // want `slog.Logger.DebugContext arg "1" should be a string or a slog.Attr`
	l.ErrorContext(ctx, "msg", 1, 2) // want `slog.Logger.ErrorContext arg "1" should be a string or a slog.Attr`
	l.InfoContext(ctx, "msg", 1, 2)  // want `slog.Logger.InfoContext arg "1" should be a string or a slog.Attr`
	l.WarnContext(ctx, "msg", 1, 2)  // want `slog.Logger.WarnContext arg "1" should be a string or a slog.Attr`

	l.Log(ctx, slog.LevelDebug, "msg", 1, 2) // want `slog.Logger.Log arg "1" should be a string or a slog.Attr`

	_ = l.With(1, 2) // want `slog.Logger.With arg "1" should be a string or a slog.Attr`

	r.Add(1, 2) // want `slog.Record.Add arg "1" should be a string or a slog.Attr`

	_ = slog.Group("key", "a", 1, "b", 2)
	_ = slog.Group("key", "a", 1, 2, 3) // want `slog.Group arg "2" should be a string or a slog.Attr`

	slog.Error("foo", "err", errors.New("oops")) // regression test for #61228.
}

// Used in tests by package b.
var MyLogger = slog.Default()
