package ignored

import (
	"sync/atomic"
)

var x int32 // don't fix - package has ignored files

func _() {
	for range 100 {
		go atomic.AddInt32(&x, 1)
	}
}
