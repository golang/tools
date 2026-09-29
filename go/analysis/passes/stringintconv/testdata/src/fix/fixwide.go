// This code relies on pre-go1.28 string(integer) conversion rules;
// the build tag downgrades the file to go1.21 (the oldest version
// to which a build tag can downgrade).
//go:build go1.21

package fix

const (
	small int64 = 'x'
	large int64 = 1 << 40
)

func _(i int, i64 int64, u uint, u32 uint32, i8 int8, up uintptr) {
	println(string(i))     // want `conversion from int to string yields...`
	println(string(i64))   // want `conversion from int64 to string yields...`
	println(string(u))     // want `conversion from uint to string yields...`
	println(string(up))    // want `conversion from uintptr to string yields...`
	println(string(u32))   // want `conversion from uint32 to string yields...`
	println(string(i8))    // want `conversion from int8 to string yields...`
	println(string(small)) // want `conversion from int64 to string yields...`
	println(string(large)) // want `conversion from int64 to string yields...`
}

type myint int64

func (myint) String() string { return "" }

func _(m myint) {
	// No fix is offered, since Sprintf might call a method (such as Format).
	println(string(m)) // want `conversion from myint \(int64\) to string yields...`

	// No fix is offered, since the constant overflows int32 and has no type.
	println(string(1 << 40)) // want `conversion from untyped int to string yields...`
}
