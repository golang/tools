package full

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
// Format: fullsym(Location, Name, Kind, ContainerName, Qname, PkgLoc.Version, PkgLoc.Name, PkgLoc.RepoURI)

// Test struct declaration and the field declarations.
type Circle struct { //@fullsym("cle", "Circle", 23, "", "full.Circle", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	r    float64  //@fullsym("r", "r", 8, "Circle", "full.Circle.r", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	Node struct { //@fullsym("Node", "Node", 8, "Circle", "full.Circle.Node", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
		x int
		y int
	}
}

// Test function declaration and variables.
func func1() int { //@fullsym("c1", "func1", 12, "", "full.func1", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	lhs := 10
	const rhs int = 20
	return lhs + rhs
}

// Test method declaration and field access.
func (c *Circle) method1() { //@fullsym("method1", "method1", 6, "struct_alias", "full.struct_alias.method1", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	fmt.Print(c.r)

	fmt.Print(c.Node.x)
}

// Test the interface declaration and method declaration.
type Shape interface { //@fullsym("ha", "Shape", 11, "", "full.Shape", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	Area() int //@fullsym("Are", "Area", 6, "Shape", "full.Shape.Area", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
}

// Test method call and the variabel declaration.
func func2() { //@fullsym("c", "func2", 12, "", "full.func2", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	var c Circle
	c.method1()
}

// Test the struct declared in a function.
func structInFunc() { //@fullsym("stru", "structInFunc", 12, "", "full.structInFunc", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	type A struct {
		x int
		y int
	}

	a := &A{x: 10, y: 20} //@fullsym("x", "x", 8, "", "full.structInFunc.A.x", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	fmt.Println(a.x, a.y)
}

// Test the local variable declared in the method declaration.
func (c Circle) method2() { //@fullsym("me", "method2", 6, "struct_alias", "full.struct_alias.method2", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	num := 10
	fmt.Println(num)
}

// Test the variable declared in an anonymous functions case#1.
func func4() { //@fullsym("func", "func4", 12, "", "full.func4", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
	f := func(x int) int {
		num := 10
		return x + num
	}
	fmt.Println(f(1))
}

type struct_alias = Circle //@fullsym("str", "struct_alias", 23, "", "full.struct_alias", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
type struct_tydef Circle   //@fullsym("tydef", "struct_tydef", 23, "", "full.struct_tydef", "", "full", "golang.org/x/tools/internal/lsp/lspext/full")
