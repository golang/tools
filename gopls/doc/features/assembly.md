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

The following requests are currently supported:

- Definition (`textDocument/definition`): on a reference to a symbol,
  returns the location of its declaration. For example, a Definition
  request on the `sigpanic` symbol in this file in
  GOROOT/src/runtime/asm.s:

  ```asm
  	JMP	·sigpanic<ABIInternal>(SB)
  ```

  returns the location of the function declaration in
  GOROOT/src/runtime/signal_unix.go:

  ```go
  //go:linkname sigpanic
  func sigpanic() {
  ```

- References (`textDocument/references`): finds all references to the
  symbol under the cursor. It supports symbols declared in Go, symbols
  declared only in assembly, and control labels. Assembly-only symbols
  are searched within the package, file-local `<>` symbols within their
  source file, and labels within the enclosing TEXT function.

- Hover (`textDocument/hover`): reports the signature and doc comment
  of the symbol's Go declaration.

- DocumentHighlight (`textDocument/documentHighlight`): highlights all
  occurrences of the symbol, control label, or machine register under
  the cursor. Labels and registers are scoped to the enclosing TEXT
  function, and occurrences are classified as reads or writes.
  Register highlighting requires the file name to carry a GOARCH
  suffix (e.g. `foo_amd64.s`) and currently supports x86 (amd64, 386)
  and arm64.

See also issue https://go.dev/issue/71754, which tracks the development of LSP
features in Go assembly files.
