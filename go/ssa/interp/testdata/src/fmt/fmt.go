package fmt

import (
	"errors"
	"strings"
)

func Sprint(args ...any) string

func Sprintln(args ...any) string {
	return Sprint(args...) + "\n"
}

func Print(args ...any) (int, error) {
	var n int
	for i, arg := range args {
		if i > 0 {
			print(" ")
			n++
		}
		msg := Sprint(arg)
		n += len(msg)
		print(msg)
	}
	return n, nil
}

func Println(args ...any) {
	Print(args...)
	println()
}

// formatting is too complex to fake
// handle the bare minimum needed for tests

func Printf(format string, args ...any) (int, error) {
	msg := Sprintf(format, args...)
	print(msg)
	return len(msg), nil
}

func Sprintf(format string, args ...any) string {
	// handle extremely simple cases that appear in tests.
	if len(format) == 0 {
		return ""
	}
	switch {
	case strings.HasPrefix("%v", format) || strings.HasPrefix("%s", format):
		return Sprint(args[0]) + Sprintf(format[2:], args[1:]...)
	case !strings.HasPrefix("%", format):
		return format[:1] + Sprintf(format[1:], args...)
	default:
		panic("unsupported format string for testing Sprintf")
	}
}

func Errorf(format string, args ...any) error {
	msg := Sprintf(format, args...)
	return errors.New(msg)
}
