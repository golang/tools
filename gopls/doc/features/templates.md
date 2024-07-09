# Gopls: Support for template files

Gopls provides some support for Go template files, that is, files that
are parsed by [`text/template`](https://pkg.go.dev/text/template) or
[`html/template`](https://pkg.go.dev/html/template).

## Enabling template support

Gopls recognizes template files based on their file extension, which
may be configured by the
[`templateExtensions`](../settings.md#templateExtensions) setting. If
this list is empty, template support is disabled. (This is the default
value, since Go templates don't have a canonical file extension.)

Additional configuration may be necessary to ensure that your client
chooses the correct language kind when opening template files.
Gopls recogizes both `"tmpl"` and `"gotmpl"` for template files.
For example, in `VS Code` you will also need to add an
entry to the
[`files.associations`](https://code.visualstudio.com/docs/languages/identifiers)
mapping:
```json
"files.associations": {
  ".mytemplate": "gotmpl"
},
```


## Features
In template files, template support works inside
the default `{{` delimiters. (Go template parsing
allows the user to specify other delimiters, but
gopls does not know how to do that.)

Gopls template support includes the following features:
+ **Diagnostics**: if template parsing returns an error,
it is presented as a diagnostic. (Missing functions do not produce errors.)
+ **Syntax Highlighting**: syntax highlighting is provided for template files.
+ **Definitions**: gopls provides jump-to-definition inside templates, though it does not understand scoping (all templates are considered to be in one global scope).
+ **References**: gopls provides find-references, with the same scoping limitation as definitions.
+ **Completions**: gopls will attempt to suggest completions inside templates.

TODO: also
+ Hover
+ SemanticTokens
+ Symbol search
+ DocumentHighlight


