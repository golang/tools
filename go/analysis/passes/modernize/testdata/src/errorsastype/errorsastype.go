package errorsastype

import (
	"errors"
	"os"
)

var packagePathErr *os.PathError

func _(err error) {
	if errors.As(err, &packagePathErr) { // nope: packagePathErr is not declared by a local statement
		print(packagePathErr)
	}

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
		if errors.As(err, &patherr) { // want `errors.As can be simplified using AsType\[\*os.PathError\]`
			print("not a use of patherr")
		}
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
	// Negated case.
	{
		var patherr *os.PathError
		if !errors.As(err, &patherr) { // want `errors.As can be simplified using AsType\[\*os.PathError\]`
			print(patherr)
		}
	}
	{
		var patherr *os.PathError
		var linkerr *os.LinkError
		if errors.As(err, &patherr) { // want `errors.As can be simplified using AsType\[\*os.PathError\]`
			print(patherr)
		} else if !errors.As(err, &linkerr) { // want `errors.As can be simplified using AsType\[\*os.LinkError\]`
			print("not a use of linkerr")
		}
	}
	{
		var patherr *os.PathError
		if !errors.As(err, &patherr) { // want `errors.As can be simplified using AsType\[\*os.PathError\]`
			print("not a use of patherr")
		} else {
			print(patherr)
		}
	}
	{
		var patherr *os.PathError = &os.PathError{}
		if !errors.As(err, &patherr) { // nope: would change the value of patherr observed by the print statement
			print(patherr)
		}
	}
	{
		type Foo interface {
			Bar() string
		}
		var target Foo
		if errors.As(err, &target) { // nope: target doesn't satisfy error
			print(target)
		}
	}
	{
		type FooError interface {
			Bar() string
			error
		}
		var target FooError
		if errors.As(err, &target) { // want `errors.As can be simplified using AsType\[FooError\]`
			print(target)
		}
	}
}
