# Gopls: Index of features

This page provides an index of all supported features of gopls that
are accessible through the [language server protocol](https://microsoft.github.io/language-server-protocol/) (LSP).
It is intended for:
- **users of gopls** learning its capabilities so that they get the most out of their editor;
- **editor maintainers** adding or improving Go support in an LSP-capable editor; and
- **contributors to gopls** trying to understand how it works.

In an ideal world, Go users would not need to know that gopls or even
LSP exists, as their LSP-enabled editors would implement every facet
of the protocol and expose each feature in a natural and discoverable
way. In reality, editors vary widely in their support for LSP, so
unfortunately these documents necessarily involve many details of the
protocol.

We also list [settings](../settings.md) that affect each feature.

Most features are illustrated with reference to VS Code, but we will
briefly mention whether each feature is supported in other popular
clients, and if so, how to find it. We welcome contributions, edits,
and updates from users of any editor.

- [Passive](passive.md): features that are always on and require no special action
  - [Hover](passive.md#Hover): information about the symbol under the cursor
  - [SignatureHelp](passive.md#SignatureHelp): type information about the enclosing function call
  - [DocumentHighlight](passive.md#DocumentHighlight): highlight identifiers referring to the same symbol
  - [InlayHint](passive.md#InlayHint): show implicit names of struct fields and parameter names
  - [SemanticTokens](passive.md#SemanticTokens): report syntax information used by editors to color the text
  - [FoldingRange](passive.md#FoldingRange): report text regions that can be "folded" (expanded/collapsed) in an editor
  - [DocumentLink](passive.md#DocumentLink): extracts URLs from doc comments, strings in current file so client can linkify
- [Diagnostics](diagnostics.md): compile errors and static analysis findings
  - TODO: expand subindex
- [Navigation](navigation.md): navigation of cross-references, types, and symbols
  - [Definition](navigation.md#Definition): go to definition of selected symbol
  - [TypeDefinition](navigation.md#TypeDefinition): go to definition of type of selected symbol
  - [References](navigation.md#References): list references to selected symbol
  - [Implementation](navigation.md#Implementation): show "implements" relationships of selected type
  - [DocumentSymbol](passive.md#DocumentSymbol): outline of symbols defined in current file
  - [Symbol](navigation.md#Symbol): fuzzy search for symbol by name
  - [SelectionRange](navigation.md#SelectionRange): select enclosing unit of syntax
  - [CallHierarchy](navigation.md#CallHierarchy): show outgoing/incoming calls to the current function
- [Completion](completion.md): context-aware completion of identifiers, statements
- [Code transformation](transformation.md): fixes and refactorings
  - [Formatting](transformation.md#Formatting): format the source code
  - [Rename](transformation.md#Rename): rename a symbol or package
  - [Organize imports](transformation.md#OrganizeImports): organize the import declaration
  - [Extract](transformation.md#Extract): extract selection to a new file/function/variable
  - [Inline](transformation.md#Inline): inline a call to a function or method
  - [Miscellaneous rewrites](transformation.md#Rewrite): various Go-specific refactorings
- [Web-based queries](web.md): commands that open a browser page
  - [Package documentation](web.md#doc): browse documentation for current Go package
  - [Free symbols](web.md#freesymbols): show symbols used by a selected block of code
  - [Assembly](web.md#assembly): show listing of assembly code for selected function
- Support for non-Go files:
  - [Template files](templates.md): files parsed by `text/template` and `html/template`
  - [go.mod and go.work files](modfiles.md): Go module and workspace manifests
- [Command-line interface](../command-line.md): CLI for debugging and scripting (unstable)
- [Non-standard commands](../commands.md): gopls-specific RPC protocol extensions (unstable)
