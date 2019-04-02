package main

var x = 42 //@symbol("x", "x", 13)

const y = 43 //@symbol("y", "y", 14)

type Foo struct { //@symbol("Foo", "Foo", 23)
	Quux
	Bar int
	baz string
}

type Quux struct { //@symbol("Quux", "Quux", 23)
	X float64
}

func (f Foo) Baz() string { //@symbol("Baz", "Baz", 6)
	return f.baz
}

func main() { //@symbol("main", "main", 12)

}

type Stringer interface { //@symbol("Stringer", "Stringer", 11)
	String() string
}

type struct_alias = Foo //@symbol("struct_alias", "struct_alias", 23)
type struct_tydef Foo   //@symbol("struct_tydef", "struct_tydef", 23)

type interface_alias = Stringer //@symbol("interface_alias", "interface_alias", 11)
type interface_tydef Stringer   //@symbol("interface_tydef", "interface_tydef", 11)

type int_basic_alias = int //@symbol("int_basic_alias", "int_basic_alias", 16)
type int_tydef int         //@symbol("int_tydef", "int_tydef", 16)

type float_basic_alias = float64 //@symbol("float_basic_alias", "float_basic_alias", 16)
type float_basic_tydef float64   //@symbol("float_basic_tydef", "float_basic_tydef", 16)

type bool_basic_alias = bool //@symbol("bool_basic_alias", "bool_basic_alias", 17)
type bool_basic_tydef bool   //@symbol("bool_basic_tydef", "bool_basic_tydef", 17)

type string_basic_alias = string //@symbol("string_basic_alias", "string_basic_alias", 15)
type string_basic_tydef string   //@symbol("string_basic_tydef", "string_basic_tydef", 15)

type int_slice_alias = []int //@symbol("int_slice_alias", "int_slice_alias", 18)
type int_slice_tydef = []int //@symbol("int_slice_tydef", "int_slice_tydef", 18)

type string_slice_alias = []string //@symbol("string_slice_alias", "string_slice_alias", 18)
type string_slice_tydef = []string //@symbol("string_slice_tydef", "string_slice_tydef", 18)

type int_array_alias = []int //@symbol("int_array_alias", "int_array_alias", 18)
type int_array_tydef = []int //@symbol("int_array_tydef", "int_array_tydef", 18)

type string_array_alias = []string //@symbol("string_array_alias", "string_array_alias", 18)
type string_array_tydef = []string //@symbol("string_array_tydef", "string_array_tydef", 18)
