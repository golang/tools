package spansfiles

import "sync/atomic"

func f() {
	_ = atomic.LoadInt32(&v)
}
