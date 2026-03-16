package atomic

import (
	"log"
	"sync/atomic"
)

type X struct {
	x int32 // want "var x int32 may be simplified using atomic.Int32"
}

type Z struct {
	y int64 // want "var y int64 may be simplified using atomic.Int64"
	z int64
}

func (wrapper *Z) fix() {
	var x int32 // want "var x int32 may be simplified using atomic.Int32"
	for range 100 {
		go atomic.AddInt32(&x, 1)
	}

	var x2 int32 = 5 // nope: can't assign an int to an atomic.Int32
	for range 100 {
		go atomic.AddInt32(&x2, 1)
	}

	var y X
	for range 100 {
		go atomic.CompareAndSwapInt32(&y.x, 2, 3)
	}

	atomic.CompareAndSwapInt64(&wrapper.y, 2, 3)

	var z int32
	_ = z
	if z == 0 { // nope: cannot rewrite rvalue use (unsynchronized load)
		go atomic.LoadInt32(&z)
		log.Print(z)
	}
}

type Y int32

func (y Y) dontfix(x int32) (result int32) {
	atomic.AddInt32(&x, 1)           // nope - v is a type param
	atomic.StoreInt32(&result, 100)  // nope - v is a return value
	atomic.AddInt32((*int32)(&y), 1) // nope - v is a receiver var
	w := Z{
		z: 1,
	}
	atomic.AddInt64(&w.z, 1) // nope - cannot fix initial value assignment
	return
}
