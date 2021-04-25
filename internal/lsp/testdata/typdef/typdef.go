package typdef

type Struct struct { //@item(Struct, "Struct", "struct{...}", "struct")
	Field string
}

type Int int //@item(Int, "Int", "int", "type")

func _() {
	var (
		value Struct
		point *Struct
	)
	_ = value //@typdef("value", Struct)
	_ = point //@typdef("point", Struct)

	var (
		array   [3]Struct
		slice   []Struct
		ch      chan Struct
		complex [3]chan *[5][]Int
	)
	_ = array   //@typdef("array", Struct)
	_ = slice   //@typdef("slice", Struct)
	_ = ch      //@typdef("ch", Struct)
	_ = complex //@typdef("complex", Int)

	var s struct {
		x struct {
			xx struct {
				field1 []Struct
				field2 []Int
			}
		}
	}
	s.x.xx.field1 //@typdef("field1", Struct)
	s.x.xx.field2 //@typdef("field2", Int)
}

func A() Int { return 0 }

func B() (Int, bool) { return 0, false }

func C() (Struct, error) { return Struct{}, nil }

func _() {
	A() //@typdef("A", Int)
	B() //@typdef("B", Int)
	C() //@typdef("C", Struct)

	f := func() Int { return 0 }
	f() //@typdef("f", Int)
}

// https://github.com/golang/go/issues/38589#issuecomment-620350922
func _() {
	type myFunc func(int) Int //@item(myFunc, "myFunc", "func", "type")

	var foo myFunc
	bar := foo() //@typdef("foo", myFunc)
}
