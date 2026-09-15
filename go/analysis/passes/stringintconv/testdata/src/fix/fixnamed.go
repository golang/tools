// This code relies on pre-1.28 string(integer) conversion rules.
//go:build !go1.28

package fix

type mystring string

func _(x int16) mystring {
	return mystring(x) // want `conversion from int16 to mystring \(string\)...`
}
