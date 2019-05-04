package edefinition //Test fail here, edefinition("def", "edefinition", 4)

import (
	"fmt"
)

// FileSymbol          SymbolKind = 1
// ModuleSymbol        SymbolKind = 2
// NamespaceSymbol     SymbolKind = 3
// PackageSymbol       SymbolKind = 4
// ClassSymbol         SymbolKind = 5
// MethodSymbol        SymbolKind = 6
// PropertySymbol      SymbolKind = 7
// FieldSymbol         SymbolKind = 8
// ConstructorSymbol   SymbolKind = 9
// EnumSymbol          SymbolKind = 10
// InterfaceSymbol     SymbolKind = 11
// FunctionSymbol      SymbolKind = 12
// VariableSymbol      SymbolKind = 13
// ConstantSymbol      SymbolKind = 14
// StringSymbol        SymbolKind = 15
// NumberSymbol        SymbolKind = 16
// BooleanSymbol       SymbolKind = 17
// ArraySymbol         SymbolKind = 18
// ObjectSymbol        SymbolKind = 19
// KeySymbol           SymbolKind = 20
// NullSymbol          SymbolKind = 21
// EnumMemberSymbol    SymbolKind = 22
// StructSymbol        SymbolKind = 23
// EventSymbol         SymbolKind = 24
// OperatorSymbol      SymbolKind = 25
// TypeParameterSymbol SymbolKind = 26

// Note: '@qnamekind("first", "second", number)', means check the location where the first element located, the second
// element represents the qualified name, the third element represents the symbol kind.

// Test struct declaration and the field declarations.
type Circle struct { //@qnamekind("cle", "edefinition.Circle", 23)
	r    float64  //@qnamekind("r", "edefinition.Circle.r", 8)
	Node struct { //@qnamekind("e", "edefinition.Circle.Node", 8)
		x int //@qnamekind("x", "edefinition.Circle.Node.x", 8)
		y int
	}
}

// Test function declaration and variables.
func func1() int { //@qnamekind("c1", "edefinition.func1", 12)
	lhs := 10          //@qnamekind("lhs", "edefinition.func1.lhs", 13)
	const rhs int = 20 //@qnamekind("rhs", "edefinition.func1.rhs", 14)
	return lhs + rhs   //@qnamekind("rhs", "edefinition.func1.rhs", 14)
}

// Test method declaration and field access.
func (c *Circle) method1() { //edefinition("method1", "edefinition.Circle.method1", 6)
	fmt.Print(c.r) //@qnamekind("fmt", "fmt", 4),qnamekind("Pri", "fmt.Print", 12)

	fmt.Print(c.Node.x) //@qnamekind("No", "edefinition.Circle.Node", 8)
}

// Test the interface declaration and method declaration.
type Shape interface { //@qnamekind("ha", "edefinition.Shape", 11)
	Area() int //@qnamekind("Are", "edefinition.Shape.Area", 6)
}

// Test method call and the variabel declaration in a nested scope.
func func2() {
	var c Circle //@qnamekind("c", "edefinition.func2.c", 13)
	c.method1()  //@qnamekind("meth", "edefinition.Circle.method1", 6),qnamekind("c", "edefinition.func2.c", 13)

	{
		num := 20 //@qnamekind("num", "edefinition.func2.num", 13)
		fmt.Println(num)
	}
}

// Test the struct declared in a function.
func structInFunc() {
	type A struct { //@qnamekind("A", "edefinition.structInFunc.A", 23)
		x int //@qnamekind("x", "edefinition.structInFunc.A.x", 8)
		y int
	}

	a := &A{x: 10, y: 20} //@qnamekind("x", "edefinition.structInFunc.A.x", 8)
	fmt.Println(a.x, a.y)
}

// Test the named structs which have the same name and declared in different scopes.
func structInAnonyScopes() {
	{
		type A struct { //@qnamekind("A", "edefinition.structInAnonyScopes.A", 23)
			x int //@qnamekind("x", "edefinition.structInAnonyScopes.A.x", 8)
		}

		a := &A{x: 10} //@qnamekind("A", "edefinition.structInAnonyScopes.A", 23)
		fmt.Println(a)
	}

	{
		type A struct { //@qnamekind("A", "edefinition.structInAnonyScopes.A", 23)
			x int
		}

		a := &A{x: 10} //@qnamekind("A", "edefinition.structInAnonyScopes.A", 23)
		fmt.Println(a)
	}
}

// Test the local variable declared in the method declaration.
func (c *Circle) method2() {
	num := 10        //@qnamekind("nu", "edefinition.Circle.method2.num", 13)
	fmt.Println(num) //@qnamekind("Pri", "fmt.Println", 12)
}

// Test the field located in a struct which declared in a method declaration.
func (c *Circle) method3() {
	type Rectangle struct {
		x int
		y int //@qnamekind("y", "edefinition.Circle.method3.Rectangle.y", 8)
	}
}

// Test the method whose receiver has a value type.
func (c Circle) method4() float64 { //@qnamekind("tho", "edefinition.Circle.method4", 6)
	return c.r
}

// Test the variable declared in an anonymous functions case#1.
func func4() {
	f := func(x int) int {
		num := 10 //@qnamekind("num", "edefinition.func4.f.num", 13)
		return x + num
	}
	fmt.Println(f(1))
}

// Test the variable declared in an anonymous functions case#2.
func func5() func(int) int {
	return func(x int) int {
		num := 10 //@qnamekind("nu", "edefinition.func5.num", 13)
		return x + num
	}
}

// Test the variable declared in an anonymous functions case#3.
func func6() (f func(int) int) {
	f = func(x int) int {
		num := 10 //@qnamekind("n", "edefinition.func6.f.num", 13)
		return x + num
	}
	return
}

// Test the field symbol in an anonymous type case#1.
func func7() {
	var node struct {
		x int //@qnamekind("x", "edefinition.func7.node.x", 8)
		y int
	}
	fmt.Println(node)
}

// Test the field symbol in an anonymous type case#2.
func func8(pair struct {
	x int //@qnamekind("x", "edefinition.func8.pair.x", 8)
	y int
}) int {
	return pair.x + pair.y
}

// Test the field symbol in an anonymous type case#3
func func9(x int, y int) (pair struct { //@edefinition("x", "edefinition.func9.x", 13)
	x int //@qnamekind("x", "edefinition.func9.pair.x", 8)
	y int
}) {
	pair.x = x
	pair.y = y
	return
}

// Test the field symbol in a anonymous type case#4
func func10() {
	// In this case just consider the first variable's name.
	var node1, node2 struct {
		x int //@qnamekind("x", "edefinition.func10.node1.x", 8)
		y int
	}
	fmt.Println(node1)
	fmt.Println(node2)
}

// Test the named return parameters.
func func11(lhs int, rhs int) (sum int) { //@qnamekind("su", "edefinition.func11.sum", 13)
	sum = lhs + rhs
	return
}

type struct_alias = Circle //@qnamekind("str", "edefinition.struct_alias", 23)
type struct_tydef Circle   //@qnamekind("tydef", "edefinition.struct_tydef", 23)

type interface_alias = Shape //@qnamekind("interface_alias", "edefinition.interface_alias", 11)
type interface_tydef Shape   //@qnamekind("interface_tydef", "edefinition.interface_tydef", 11)

type int_basic_alias = int //@qnamekind("int_basic_alias", "edefinition.int_basic_alias", 16)
type int_tydef int         //@qnamekind("int_tydef", "edefinition.int_tydef", 16)

type float_basic_alias = float64 //@qnamekind("float_basic_alias", "edefinition.float_basic_alias", 16)
type float_basic_tydef float64   //@qnamekind("float_basic_tydef", "edefinition.float_basic_tydef", 16)

type bool_basic_alias = bool //@qnamekind("bool_basic_alias", "edefinition.bool_basic_alias", 17)
type bool_basic_tydef bool   //@qnamekind("bool_basic_tydef", "edefinition.bool_basic_tydef", 17)

type string_basic_alias = string //@qnamekind("string_basic_alias", "edefinition.string_basic_alias", 15)
type string_basic_tydef string   //@qnamekind("string_basic_tydef", "edefinition.string_basic_tydef", 15)

type int_slice_alias = []int //@qnamekind("int_slice_alias", "edefinition.int_slice_alias", 18)
type int_slice_tydef = []int //@qnamekind("int_slice_tydef", "edefinition.int_slice_tydef", 18)

type string_slice_alias = []string //@qnamekind("ing_slice_alias", "edefinition.string_slice_alias", 18)
type string_slice_tydef = []string //@qnamekind("string_slice_tydef", "edefinition.string_slice_tydef", 18)

type int_array_alias = []int //@qnamekind("int_ar", "edefinition.int_array_alias", 18)
type int_array_tydef = []int //@qnamekind("t_array_tydef", "edefinition.int_array_tydef", 18)

type string_array_alias = []string //@qnamekind("string_ar", "edefinition.string_array_alias", 18)
type string_array_tydef = []string //@qnamekind("ray_tydef", "edefinition.string_array_tydef", 18)
