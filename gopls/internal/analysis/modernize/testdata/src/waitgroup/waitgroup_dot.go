package waitgroup

import (
	"fmt"
	. "sync"
)

// supported case for pattern 1.
func _() {
	var wg WaitGroup
	wg.Add(1) // want "Goroutine creation can be simplified using WaitGroup.Go"
	go func() {
		defer wg.Done()
		fmt.Println()
	}()

	wg.Add(1) // want "Goroutine creation can be simplified using WaitGroup.Go"
	go func() {
		fmt.Println()
		wg.Done()
	}()
}
