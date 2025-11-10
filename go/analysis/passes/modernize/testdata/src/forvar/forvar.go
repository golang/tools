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
		v := v // want "copying variable is unneeded"
		go f(k)
		go f(v)
	}
	for k, v := range m {
		v := v // want "copying variable is unneeded"
		k := k // want "copying variable is unneeded"
		go f(k)
		go f(v)
	}
	for k, v := range m {
		k, v := k, v // want "copying variable is unneeded"
		go f(k)
		go f(v)
	}
	for k, v := range m {
		v, k := v, k // want "copying variable is unneeded"
		go f(k)
		go f(v)
	}
	for i := range s {
		/* hi */ i := i // want "copying variable is unneeded"
		go f(i)
	}
	for v := range m {
		if v := v; true { // want "copying variable is unneeded"
			print(v)
		}
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
	for k, v := range m { // nope, LHS and RHS differ
		v, k := k, v
		go f(k)
		go f(v)
	}
	for k, v := range m { // nope, not a simple redecl
		k, v, x := k, v, 1
		go f(k)
		go f(v)
		go f(x)
	}
	for i := range s { // nope, not a simple redecl
		i := (i)
		go f(i)
	}
	for i := range s { // nope, not a simple redecl
		i := i + 1
		go f(i)
	}
	for v := range m {
		if v := v; true { // nope, would merge distinct outer and inner variables v
			print(v)
		}
		print(v)
	}
}

func f(n int) {}
