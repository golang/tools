// This code relies on pre-go1.28 string(integer) conversion rules;
// the build tag downgrades the file to go1.21 (the oldest version
// to which a build tag can downgrade).
//go:build go1.21

package fix

func _(x uint64) {
	println(string(x)) // want `conversion from uint64 to string yields...`
}
