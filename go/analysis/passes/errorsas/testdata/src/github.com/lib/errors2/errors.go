// Package errors2 contains errors.As-like function,
// which is used in unit-testing.
package errors2

import stderrors "errors"

func As(err error, target interface{}) bool {
	return stderrors.As(err, target)
}
