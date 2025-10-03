package errorsastype

import (
	. "errors"
	"os"
)

func _(err error) {
	var patherr *os.PathError
	if As(err, &patherr) { // want `errors.As can be simplified using AsType\[\*os.PathError\]`
		print(patherr)
	}
}
