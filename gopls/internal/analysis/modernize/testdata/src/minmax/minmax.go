package minmax

func ifmin(a, b int) {
	x := a
	if a < b { // want "if statement can be modernized using max"
		x = b
	}
	print(x)
}

func ifmax(a, b int) {
	x := a
	if a > b { // want "if statement can be modernized using min"
		x = b
	}
	print(x)
}

func ifminvariant(a, b int) {
	x := a
	if x > b { // want "if statement can be modernized using min"
		x = b
	}
	print(x)
}

func ifmaxvariant(a, b int) {
	x := b
	if a < x { // want "if statement can be modernized using min"
		x = a
	}
	print(x)
}

func ifelsemin(a, b int) {
	var x int
	if a <= b { // want "if/else statement can be modernized using min"
		x = a
	} else {
		x = b
	}
	print(x)
}

func ifelsemax(a, b int) {
	var x int
	if a >= b { // want "if/else statement can be modernized using max"
		x = a
	} else {
		x = b
	}
	print(x)
}

func shadowed() int {
	hour, min := 3600, 60

	var time int
	if hour < min { // silent: the built-in min function is shadowed here
		time = hour
	} else {
		time = min
	}
	return time
}
