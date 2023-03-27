package invertifcondition

import (
	"fmt"
	"os"
)

func F() {
	if len(os.Args) > 2 { // want "Invert if condition"
		fmt.Println("A")
	} else {
		fmt.Println("B")
	}

	if _, err := fmt.Println("x"); err != nil { // want "Invert if condition"
		fmt.Println("A")
	} else {
		fmt.Println("B")
	}

	if n, err := fmt.Println("x"); err != nil && n > 0 { // want "Invert if condition"
		fmt.Println("A")
	} else {
		fmt.Println("B")
	}

	if n, err := fmt.Println("x"); err != nil || n > 0 { // want "Invert if condition"
		fmt.Println("A")
	} else {
		fmt.Println("B")
	}

	// No inversion expected when there's not else clause
	if len(os.Args) > 2 {
		fmt.Println("A")
	}

	// No inversion expected for else-if, that would become unreadable
	if len(os.Args) > 2 {
		fmt.Println("A")
	} else if os.Args[0] == "X" { // want "Invert if condition"
		fmt.Println("B")
	} else {
		fmt.Println("C")
	}

	b := true
	if b { // want "Invert if condition"
		fmt.Println("A")
	} else {
		fmt.Println("B")
	}

	if os.IsPathSeparator('X') { // want "Invert if condition"
		fmt.Println("A")
	} else {
		fmt.Println("B")
	}
}
