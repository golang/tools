package rangeint

func _(i int, s struct{ i int }, slice []int) {
	for i := 0; i < 10; i++ { // want "for loop can be modernized using range over int"
		println(i)
	}
	{
		var i int
		for i = 0; i < f(); i++ { // want "for loop can be modernized using range over int"
		}
		// NB: no uses of i after loop.
	}
	for i := 0; i < 10; i++ { // want "for loop can be modernized using range over int"
		// i unused within loop
	}
	for i := 0; i < len(slice); i++ { // want "for loop can be modernized using range over int"
		println(slice[i])
	}
	for i := 0; i < len(""); i++ { // want "for loop can be modernized using range over int"
		// NB: not simplified to range ""
	}

	// nope
	for i := 0; i < 10; { // nope: missing increment
	}
	for i := 0; i < 10; i-- { // nope: negative increment
	}
	for i := 0; ; i++ { // nope: missing comparison
	}
	for i := 0; i <= 10; i++ { // nope: wrong comparison
	}
	for ; i < 10; i++ { // nope: missing init
	}
	for s.i = 0; s.i < 10; s.i++ { // nope: not an ident
	}
	for i := 0; i < 10; i++ { // nope: takes address of i
		println(&i)
	}
	for i := 0; i < 10; i++ { // nope: increments i
		i++
	}
	for i := 0; i < 10; i++ { // nope: assigns i
		i = 8
	}
}

func f() int { return 0 }

// Repro for part of #71847: ("for range n is invalid if the loop body contains i++"):
func _(s string) {
	var i int                    // (this is necessary)
	for i = 0; i < len(s); i++ { // nope: loop body increments i
		if true {
			i++ // nope
		}
	}
}

// Repro for #71952: for and range loops have different final values
// on i (n and n-1, respectively) so we can't offer the fix if i is
// used after the loop.
func nopePostconditionDiffers() {
	i := 0
	for i = 0; i < 5; i++ {
		println(i)
	}
	println(i) // must print 5, not 4
}

// Non-integer untyped constants need to be explicitly converted to int.
func issue71847d() {
	const limit = 1e3            // float
	for i := 0; i < limit; i++ { // want "for loop can be modernized using range over int"
	}

	const limit2 = 1 + 0i         // complex
	for i := 0; i < limit2; i++ { // want "for loop can be modernized using range over int"
	}
}
