package foo

type StructFoo struct { //@item(StructFoo, "StructFoo", "struct{...}", "struct")
	Value int //@item(Value, "Value", "int", "field")
}

// TODO(rstambler): Create pre-set builtins?
/* Error() */ //@item(Error, "Error()", "string", "method")

func Foo() { //@item(Foo, "Foo()", "", "func")
	var err error
	err.Error() //@complete("E", Error)
}

func _() {
	var sFoo StructFoo           //@complete("t", StructFoo)
	if x := sFoo; x.Value == 1 { //@complete("V", Value),typdef("sFoo", StructFoo)
		return
	}
}

type IntFoo int //@item(IntFoo, "IntFoo", "int", "type"),complete("", Foo, IntFoo, StructFoo)
