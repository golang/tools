---
title: "Gopls: Support for Go *.s assembly files"
---

Gopls has rudimentary support for LSP operations in Go assembly files.

Go assembly files have a `.s` file name extension. LSP clients need to
be configured to recognize `.s` files as Go assembly files, since this
file name extension is also used for assembly files in other
languages. A good heuristic is that if a file named `*.s` belongs to a
directory containing at least one `*.go` file, then the `.s` file is
Go assembly, and its appropriate language server is gopls.

Only Definition (`textDocument/definition`) requests are currently
supported. For example, a Definition request on the `sigpanic`
symbol in this file in GOROOT/src/runtime/asm.s:

```asm
	JMP	·sigpanic<ABIInternal>(SB)
```

returns the location of the function declaration in
GOROOT/src/runtime/signal_unix.go:

```go
//go:linkname sigpanic
func sigpanic() {
```

See also issue https://go.dev/issue/71754, which tracks the development of LSP
features in Go assembly files.