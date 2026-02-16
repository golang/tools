package reflecttypefor

import (
	"io"
	"reflect"
	"time"
)

var (
	x any
	_ = reflect.TypeOf(x)                 // nope (dynamic)
	_ = reflect.TypeOf(0)                 // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(nil)               // nope (likely a mistake)
	_ = reflect.TypeOf(uint(0))           // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(error(nil))        // nope (likely a mistake)
	_ = reflect.TypeOf((*error)(nil))     // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(io.Reader(nil))    // nope (likely a mistake)
	_ = reflect.TypeOf((*io.Reader)(nil)) // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(*new(time.Time))   // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(time.Time{})       // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = reflect.TypeOf(time.Duration(0))  // want "reflect.TypeOf call can be simplified using TypeFor"
)

// Eliminate local var if we deleted its last use.
func _() {
	// Test for shadowed nil
	nil := "nil"
	_ = reflect.TypeOf(nil) // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = nil                 // shadowed nil has multiple uses

	var zero string
	_ = reflect.TypeOf(zero) // want "reflect.TypeOf call can be simplified using TypeFor"

	var z2 string
	_ = reflect.TypeOf(z2) // want "reflect.TypeOf call can be simplified using TypeFor"
	_ = z2                 // z2 has multiple uses
}
