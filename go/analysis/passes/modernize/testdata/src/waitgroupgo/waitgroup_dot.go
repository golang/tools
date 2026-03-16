package waitgroup

import (
	"fmt"
	. "sync"
)

// supported case for pattern 1.
func _() {
	var wg WaitGroup
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
