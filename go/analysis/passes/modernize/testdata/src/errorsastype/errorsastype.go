package errorsastype

import (
	"errors"
	"os"
)

func _(err error) {
	{
		var patherr *os.PathError
		if errors.As(err, &patherr) { // want `errors.As can be simplified using AsType\[\*os.PathError\]`
			print(patherr)
		}
	}
	{
		var patherr *os.PathError
		print("not a use of patherr")
		if errors.As(err, &patherr) { // want `errors.As can be simplified using AsType\[\*os.PathError\]`
			print(patherr)
		}
		print("also not a use of patherr")
	}
	{
		var patherr *os.PathError
		print(patherr)
		if errors.As(err, &patherr) { // nope: patherr is used outside scope of if
			print(patherr)
		}
	}
	{
		var patherr *os.PathError
		if errors.As(err, &patherr) { // nope: patherr is used outside scope of if
			print(patherr)
		}
		print(patherr)
	}

	// Test of 'ok' var shadowing/freshness.
	const ok = 1
	{
		var patherr *os.PathError
		if errors.As(err, &patherr) { // want `errors.As can be simplified using AsType\[\*os.PathError\]`
			print(patherr)
		}
	}
	{
		var patherr *os.PathError
		if errors.As(err, &patherr) { // want `errors.As can be simplified using AsType\[\*os.PathError\]`
			print(patherr, ok)
		}
	}
}
