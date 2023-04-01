package invertifcondition

import (
	"fmt"
)

func SemicolonOr() {
	if n, err := fmt.Println("x"); err != nil || n > 0 { //@suggestedfix(re"if n, err := fmt.Println..x..; err != nil .. n > 0", "refactor.rewrite", "")
		fmt.Println("A")
	} else {
		fmt.Println("B")
	}
}
