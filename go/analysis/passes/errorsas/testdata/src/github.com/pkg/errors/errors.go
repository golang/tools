// Package errors synthesizes package "github.com/pkg/errors",
// which is used in unit-testing.
package errors

import stderrors "errors"

func As(err error, target interface{}) bool {
	return stderrors.As(err, target)
}
