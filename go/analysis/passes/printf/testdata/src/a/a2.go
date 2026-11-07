//go:build go1.26

package a

// Test of induction through interface assignments. (Applies only to
// interface methods declared in files that use at least Go 1.26.)

import "fmt"

type myLogger int

func (myLogger) Logf(format string, args ...any) { // want Logf:"printfWrapper"
	print(fmt.Sprintf(format, args...))
}

// Logger is assigned from myLogger.

type Logger interface {
	Logf(format string, args ...any) // want Logf:"printfWrapper"
}

var _ Logger = myLogger(0) // establishes that Logger wraps myLogger

func _(log Logger) {
	log.Logf("%s", 123) // want `\(a.Logger\).Logf format %s has arg 123 of wrong type int`
}

// Logger2 is not assigned from myLogger.

type Logger2 interface {
	Logf(format string, args ...any)
}

func _(log Logger2) {
	log.Logf("%s", 123) // nope
}
