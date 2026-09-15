// This code relies on pre-1.28 string(integer) conversion rules.
//go:build !go1.28

package fix

import . "fmt"

func _(x uint64) {
	Println(string(x)) // want `conversion from uint64 to string yields...`
}
