package fix

import . "fmt"

func _(x uint64) {
	Println(string(x)) // want `conversion from uint64 to string yields...`
}
