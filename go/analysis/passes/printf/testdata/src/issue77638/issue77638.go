package issue77638

// Regression test for a wrapper that calls both fmt.Errorf and
// fmt.Sprintf being classified by whichever call was seen last.
// Such a wrapper must be Errorf-like, so %w is accepted.

import (
	"errors"
	"fmt"
	"strings"
)

func Errorf(format string, args ...any) error { // want Errorf:"errorfWrapper"
	if strings.Contains(format, "%w") {
		return fmt.Errorf(format, args...)
	}
	return errors.New(fmt.Sprintf(format, args...))
}

func Errorf2(format string, args ...any) error { // want Errorf2:"errorfWrapper"
	if !strings.Contains(format, "%w") {
		return errors.New(fmt.Sprintf(format, args...))
	}
	return fmt.Errorf(format, args...)
}

func Wrap(format string, args ...any) error { // want Wrap:"errorfWrapper"
	return Errorf(format, args...)
}

func _(err error) {
	_ = Errorf("x: %w", err)
	_ = Errorf2("x: %w", err)
	_ = Wrap("x: %w", err)
	_ = Errorf("x: %d", "s")  // want `issue77638.Errorf format %d has arg "s" of wrong type string`
	_ = Errorf2("x: %d", "s") // want `issue77638.Errorf2 format %d has arg "s" of wrong type string`
	_ = Wrap("x: %d", "s")    // want `issue77638.Wrap format %d has arg "s" of wrong type string`
}
