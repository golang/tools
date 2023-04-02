package invertifcondition

import (
	"fmt"
)

func RemoveElse() {
	b := true
	if !b { //@suggestedfix("if !b", "refactor.rewrite", "")
		fmt.Println("A")
	} else {
		return
	}
}
