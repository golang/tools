package w

import (
	"fmt"
	"log"
)

type CustomError struct {
	Msg string
}

func (e CustomError) Error() string {
	return e.Msg
}

type E int

func (*E) Error() string {
	return ""
}

func PrintfTests() {
	fmt.Errorf("%w", &CustomError{Msg: "custom error"}) // want `%w wants operand of error type CustomError, not pointer type \*CustomError \(defeats errors.Is\)`
	fmt.Errorf("%w", CustomError{Msg: "custom error"})
	var err *CustomError
	fmt.Errorf("%w", err) // want `%w wants operand of error type CustomError, not pointer type \*CustomError \(defeats errors.Is\)`

	var e *E
	fmt.Errorf("%w", e) // nope - Error method is attached to the pointer type *E

	log.Printf("%w", &CustomError{Msg: "custom error"}) // want `log.Printf does not support error-wrapping directive %w`
}
