package invertifstatement

import (
	"fmt"
	"os"
)

func F1() {
	if len(os.Args) > 2 { // want "invert if condition"
		fmt.Println("A")
	} else {
		fmt.Println("B")
	}
}
