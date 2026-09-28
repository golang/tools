// This code relies on pre-go1.28 string(integer) conversion rules;
// the build tag downgrades the file to go1.21 (the oldest version
// to which a build tag can downgrade).
//go:build go1.21

package fix

type mystring string

func _(x int16) mystring {
	return mystring(x) // want `conversion from int16 to mystring \(string\)...`
}
