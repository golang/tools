// Package xerrors synthesizes package "golang.org/x/xerrors",
// which is used in unit-testing.
package xerrors

import stderrors "errors"

func As(err error, target interface{}) bool {
	return stderrors.As(err, target)
}
