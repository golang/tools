package atomic

import myatomic "sync/atomic"

func _() {
	var x int32 // want "var x int32 may be simplified using atomic.Int32"
	for range 100 {
		go myatomic.AddInt32(&x, 1)
	}
}
