package waitgroup

import (
	"fmt"
	sync1 "sync"
)

func _() {
	var wg sync1.WaitGroup
	wg.Add(1)
	go func() { // want "Goroutine creation can be simplified using WaitGroup.Go"
		defer wg.Done()
		fmt.Println()
	}()

	wg.Add(1)
	go func() { // want "Goroutine creation can be simplified using WaitGroup.Go"
		fmt.Println()
		wg.Done()
	}()
}
