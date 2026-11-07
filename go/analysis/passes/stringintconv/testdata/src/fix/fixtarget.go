// This code relies on pre-go1.28 string(integer) conversion rules;
// the build tag downgrades the file to go1.21 (the oldest version
// to which a build tag can downgrade).
//go:build go1.21

package fix

type str = string

// The conversion is dropped when T is string, even via an alias...
func _(x int16) str {
	return str(x) // want `conversion from int16 to str \(string\) yields...`
}

// ...but not when T is a type parameter whose only term is string.
func _[S ~string](x int16) S {
	return S(x) // want `conversion from int16 to string \(in S\) yields...`
}
