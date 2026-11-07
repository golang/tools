package a

import "sync"

func f() {
	var wg sync.WaitGroup
	wg.Add(1) // ok
	go func() {
		wg.Add(1) // want "WaitGroup.Add called from inside new goroutine"
		// ...
		wg.Add(1) // ok
	}()
	wg.Add(1) // ok
}
