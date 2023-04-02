# Expression Node Types

`if` conditions are expressions. The different types of expressions we may want to
support [are listed in the Go source code][expression types].

See also [the Go `ast` package docs](https://pkg.go.dev/go/ast).

Here's a copy of the available expression types from the above source code link:

## Supported

* `Ident` (name of a `bool` variable, we add an initial `!`)
* `CallExpr` (a function call, we add an initial `!`)
* `ParenExpr`  (something in parenthesis, we add an initial `!`)
* `UnaryExpr` (`!` something, we remove the leading `!`)
* `StarExpr` (dereferencing a pointer to a `bool` variable)
* `IndexExpr` (`bools[x]`, we add an initial `!`)
* `SelectorExpr` (struct dereference like `x.booleanField`, we add an initial `!`)

## Should support

In order of importance.

* `BinaryExpr` (`a || b`, `c > 7`)

## Others, that we may or may not choose to support

* `BadExpr`
* `BasicLit`
* `CompositeLit`
* `Ellipsis`
* `FuncLit`
* `IndexListExpr` (unsure what this is, examples welcome, should we support this?)
* `KeyValueExpr`
* `SliceExpr` (unsure what this is, examples welcome, should we support this?)
* `TypeAssertExpr`

[expression types]: https://cs.opensource.google/go/go/+/refs/tags/go1.20.2:src/go/ast/ast.go;l=548-573
