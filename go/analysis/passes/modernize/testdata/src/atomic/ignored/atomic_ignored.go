//go:build !go1.19

package ignored

import "sync/atomic"

func _() {
	for range 100 {
		go atomic.AddInt32(&x, 1)
	}
}
