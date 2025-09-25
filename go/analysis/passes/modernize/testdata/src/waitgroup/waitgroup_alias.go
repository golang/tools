package waitgroup

import (
	"fmt"
	sync1 "sync"
)

func _() {
	var wg sync1.WaitGroup
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
