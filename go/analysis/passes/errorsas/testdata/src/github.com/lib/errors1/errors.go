// Package errors1 contains errors.As-like function,
// which is used in unit-testing.
package errors1

import stderrors "errors"

func As(err error, target interface{}) bool {
	return stderrors.As(err, target)
}
