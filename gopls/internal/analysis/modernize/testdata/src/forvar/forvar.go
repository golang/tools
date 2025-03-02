package forvar

func _(m map[int]int, s []int) {
	// changed
	for i := range s {
		i := i // want "copying variable is unneeded"
		go f(i)
	}
	for _, v := range s {
		v := v // want "copying variable is unneeded"
		go f(v)
	}
	for k, v := range m {
		k := k // want "copying variable is unneeded"
		v := v // nope: report only the first redeclaration
		go f(k)
		go f(v)
	}
	for _, v := range m {
		v := v // want "copying variable is unneeded"
		go f(v)
	}
	for i := range s {
		/* hi */ i := i // want "copying variable is unneeded"
		go f(i)
	}
	// nope
	var i, k, v int

	for i = range s { // nope, scope change
		i := i
		go f(i)
	}
	for _, v = range s { // nope, scope change
		v := v
		go f(v)
	}
	for k = range m { // nope, scope change
		k := k
		go f(k)
	}
	for k, v = range m { // nope, scope change
		k := k
		v := v
		go f(k)
		go f(v)
	}
	for _, v = range m { // nope, scope change
		v := v
		go f(v)
	}
	for _, v = range m { // nope, not x := x
		v := i
		go f(v)
	}
	for i := range s {
		i := (i)
		go f(i)
	}
}

func f(n int) {}
