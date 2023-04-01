# Expression Node Types

`if` conditions are expressions. The different types of expressions we may want to
support [are listed in the Go source code][expression types].

See also [the Go `ast` package docs](https://pkg.go.dev/go/ast).

Here's a copy of the available expression types from the above source code link:

## Should support

In order of importance.

* `BasicLit` (`true` / `false`)
* `Ident` (name of a `bool` variable)
* `CallExpr` (a function call)
* `UnaryExpr` (`!` something)
* `StarExpr` (dereferencing a pointer to a `bool` variable)
* `BinaryExpr` (`a || b`, `c > 7`)
* `ParenExpr`

## Others, that we may or may not choose to support

* `BadExpr`
* `CompositeLit`
* `Ellipsis`
* `FuncLit`
* `IndexExpr`
* `IndexListExpr`
* `KeyValueExpr`
* `SelectorExpr`
* `SliceExpr`
* `TypeAssertExpr`

[expression types]: https://cs.opensource.google/go/go/+/refs/tags/go1.20.2:src/go/ast/ast.go;l=548-573
