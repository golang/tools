package extract

import "errors"

func _() (int, string, error) {
	x := 1
	y := "hello"
	z := "bye" //@mark(exSt3, "z")
	if y == z {
		return x, y, errors.New("same")
	} else {
		z = "hi"
		return x, z, nil
	} //@mark(exEn3, "}")
	return x, z, nil
	//@extractfunc(exSt3, exEn3)
}
