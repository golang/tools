package astype

import "errors"

type FooError struct{ error }

type BarError struct {
	error
	someField int
	someSlice []int
	other     *BarError
}

func (BarError) Foo() {}

func modifies[T any](m T) bool { return false }

var global = false

func _(err error) {
	if err, ok := errors.AsType[*FooError](err); ok {
		_ = err
	} else if err, ok := errors.AsType[*BarError](err); ok { // want `err passed to AsType is the zero value of \*astype\.FooError`
		_ = err
	}

	if unwrappedErr, ok := errors.AsType[*FooError](err); ok {
		_ = unwrappedErr
	} else if unwrappedErr, ok := errors.AsType[*BarError](err); ok {
		_ = unwrappedErr
	}

	if err, ok := errors.AsType[*FooError](err); ok {
		_ = err
	} else if global {
		_ = &err
	} else if err != nil {
		err = &FooError{}
	} else if modifies(err) {
		err = &FooError{}
	} else if err2 := err; modifies(&err2) && modifies(err2) {
		err = &FooError{}
	} else if err, ok := errors.AsType[*BarError](err); ok { // want `err passed to AsType is the zero value of \*astype\.FooError`
		_ = err
	}

	if err, ok := errors.AsType[interface {
		error
		Foo()
	}](err); ok {
		_ = err
	} else if err2, ok := err.(*BarError); ok {
		_ = err2
	} else if err, ok := errors.AsType[*BarError](err); ok { // want `err passed to AsType is the zero value of`
		_ = err
	}

	if err, ok := errors.AsType[BarError](err); ok {
		_ = err
	} else if err.error == nil || err.someField == 3 || len(err.someSlice) == 0 || err.someSlice[1] == 3 {
	} else if err.other.someSlice[0] == 1 {
	} else if err, ok := errors.AsType[*BarError](err); ok { // want `err passed to AsType is the zero value of`
		_ = err
	}

	if err, ok := errors.AsType[*FooError](err); ok {
		_ = err
	} else if err, ok := errors.AsType[*BarError](err); ok { // want `err passed to AsType is the zero value of \*astype\.FooError from failed prior call to AsType`
		_ = err
	} else if err, ok := errors.AsType[error](err); ok { // want `err passed to AsType is the zero value of \*astype\.BarError from failed prior call to AsType`
		_ = err
	} else if err, ok := errors.AsType[*BarError](err); ok { // want `err passed to AsType is the zero value of error from failed prior call to AsType`
		_ = err
	}
}

func errNonZero(err error) {
	if err, ok := errors.AsType[*FooError](err); global {
		_ = err
		_ = ok
	} else if err, ok := errors.AsType[*BarError](err); ok {
		_ = err
	}

	if err, ok := errors.AsType[*FooError](err); ok {
		_ = err
	} else if modifies(&err) {
	} else if err, ok := errors.AsType[*BarError](err); ok {
		_ = err
	}

	if err, ok := errors.AsType[*FooError](err); ok {
		_ = err
	} else if err2 := &err; modifies(err2) {
	} else if err, ok := errors.AsType[*BarError](err); ok {
		_ = err
	}
}

func asTypeNotChained(err error) {
	if err, ok := errors.AsType[*FooError](err); ok {
		err = &FooError{}
	} else {
		_ = err
		if err, ok := errors.AsType[*BarError](err); ok { // want `err passed to AsType is the zero value of \*astype\.FooError`
			_ = err
		}
	}

	if err, ok := errors.AsType[*FooError](err); ok {
		err = &FooError{}
	} else {
		err = &FooError{}
		if err, ok := errors.AsType[*BarError](err); ok {
			_ = err
		}
	}

	if err, ok := errors.AsType[*FooError](err); ok {
		// analyzer should not panic, if AsType is part of the ok branch.
		if err, ok := errors.AsType[*BarError](err); ok {
			_ = err
		}
	} else if global {
	}

	if err, ok := errors.AsType[*FooError](err); ok {
		_ = err
	} else if global {
		if err, ok := errors.AsType[*BarError](err); ok { // want `err passed to AsType is the zero value of \*astype\.FooError`
			_ = err
		}
	}
}

func FakeAsType[T any](err error) (T, bool) { return *new(T), true }

func fakeAsType(err error) {
	if err, ok := FakeAsType[*FooError](err); ok {
		_ = err
	} else if err, ok := errors.AsType[*BarError](err); ok {
		_ = err
	}
}
