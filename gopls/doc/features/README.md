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

Contributors should [update this documentation](../contributing.md#documentation)
when making significant changes to existing features or when adding new ones.

- [Passive](passive.md): features that are always on and require no special action
  - [Hover](passive.md#hover): information about the symbol under the cursor
  - [Signature Help](passive.md#signature-help): type information about the enclosing function call
  - [Document Highlight](passive.md#document-highlight): highlight identifiers referring to the same symbol
  - [Inlay Hint](passive.md#inlay-hint): show implicit names of struct fields and parameter names
  - [Semantic Tokens](passive.md#semantic-tokens): report syntax information used by editors to color the text
  - [Folding Range](passive.md#folding-range): report text regions that can be "folded" (expanded/collapsed) in an editor
  - [Document Link](passive.md#document-link): extracts URLs from doc comments, strings in current file so client can linkify
- [Diagnostics](diagnostics.md): compile errors and static analysis findings
- [Navigation](navigation.md): navigation of cross-references, types, and symbols
  - [Definition](navigation.md#definition): go to definition of selected symbol
  - [Type Definition](navigation.md#type-definition): go to definition of type of selected symbol
  - [References](navigation.md#references): list references to selected symbol
  - [Implementation](navigation.md#implementation): show "implements" relationships of selected type
  - [Document Symbol](navigation.md#document-symbol): outline of symbols defined in current file
  - [Symbol](navigation.md#symbol): fuzzy search for symbol by name
  - [Selection Range](navigation.md#selection-range): select enclosing unit of syntax
  - [Call Hierarchy](navigation.md#call-hierarchy): show outgoing/incoming calls to the current function
- [Completion](completion.md): context-aware completion of identifiers, statements
- [Code transformation](transformation.md): fixes and refactorings
  - [Formatting](transformation.md#formatting): format the source code
  - [Rename](transformation.md#rename): rename a symbol or package
  - [Organize imports](transformation.md#source.organizeImports): organize the import declaration
  - [Extract](transformation.md#refactor.extract): extract selection to a new file/function/variable
  - [Inline](transformation.md#refactor.inline.call): inline a call to a function or method
  - [Miscellaneous rewrites](transformation.md#refactor.rewrite): various Go-specific refactorings
  - [Add test for func](transformation.md#source.addTest): create a test for the selected function
- [Web-based queries](web.md): commands that open a browser page
  - [Package documentation](web.md#doc): browse documentation for current Go package
  - [Free symbols](web.md#freesymbols): show symbols used by a selected block of code
  - [Assembly](web.md#assembly): show listing of assembly code for selected function
- Support for non-Go files:
  - [Template files](templates.md): files parsed by `text/template` and `html/template`
  - [go.mod and go.work files](modfiles.md): Go module and workspace manifests
- [Command-line interface](../command-line.md): CLI for debugging and scripting (unstable)

You can find this page from within your editor by executing the
`gopls.doc.features` [code action](transformation.md#code-actions),
which opens it in a web browser.
In VS Code, you can find it on the Quick fix menu.
