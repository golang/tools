# `gopls`, the Go language server

[![PkgGoDev](https://pkg.go.dev/badge/golang.org/x/tools/gopls)](https://pkg.go.dev/golang.org/x/tools/gopls)

`gopls` (pronounced "Go please") is the official Go [language server] developed
by the Go team. It provides IDE features to any [LSP]-compatible editor.

<!--TODO(rfindley): Add gifs here.-->

You should not need to interact with `gopls` directly--it will be automatically
integrated into your editor. The specific features and settings vary slightly
by editor, so we recommend that you proceed to the
[documentation for your editor](#editors) below.

## Editors

To get started with `gopls`, install an LSP plugin in your editor of choice.

* [VS Code](https://github.com/golang/vscode-go/blob/master/README.md)
* [Vim / Neovim](doc/vim.md)
* [Emacs](doc/emacs.md)
* [Atom](https://github.com/MordFustang21/ide-gopls)
* [Sublime Text](doc/subl.md)
* [Acme](https://github.com/fhs/acme-lsp)
* [Lapce](https://github.com/lapce-community/lapce-go)

If you use `gopls` with an editor that is not on this list, please send us a CL
[updating this documentation](doc/contributing.md).

## Installation

For the most part, you should not need to install or update `gopls`. Your
editor should handle that step for you.

If you do want to get the latest stable version of `gopls`, run the following
command:

```sh
go install golang.org/x/tools/gopls@latest
```

Learn more in the
[advanced installation instructions](doc/advanced.md#installing-unreleased-versions).

Learn more about gopls releases in the [release policy](doc/releases.md).

## Setting up your workspace

`gopls` supports both Go module, multi-module and GOPATH modes. See the
[workspace documentation](doc/workspace.md) for information on supported
workspace layouts.

## Configuration

You can configure `gopls` to change your editor experience or view additional
debugging information. Configuration options will be made available by your
editor, so see your [editor's instructions](#editors) for specific details. A
full list of `gopls` settings can be found in the [settings documentation](doc/settings.md).

### Environment variables

`gopls` inherits your editor's environment, so be aware of any environment
variables you configure. Some editors, such as VS Code, allow users to
selectively override the values of some environment variables.

## Support Policy

Gopls is maintained by engineers on the
[Go tools team](https://github.com/orgs/golang/teams/tools-team/members),
who actively monitor the
[Go](https://github.com/golang/go/issues?q=is%3Aissue+is%3Aopen+label%3Agopls)
and
[VS Code Go](https://github.com/golang/vscode-go/issues) issue trackers.

### Supported Go versions

`gopls` follows the
[Go Release Policy](https://golang.org/doc/devel/release.html#policy), meaning
that it officially supports only the two most recent major Go releases. Until
August 2024, the Go team will also maintain best-effort support for the last
4 major Go releases, as described in [issue #39146](https://go.dev/issues/39146).

When using gopls, there are three versions to be aware of:
1. The _gopls build go version_: the version of Go used to build gopls.
2. The _go command version_: the version of the go list command executed by
   gopls to load information about your workspace.
3. The _language version_: the version in the go directive of the current
   file's enclosing go.mod file, which determines the file's Go language
   semantics.

Starting with the release of Go 1.23.0 and gopls@v0.17.0 in August 2024, we
will only support the most recent Go version as the _gopls build go version_.
However, due to the [forward compatibility](https://go.dev/blog/toolchain)
support added in Go 1.21, as long as Go 1.21 or later are used to install
gopls, any necessary toolchain upgrade will be handled automatically, just like
any other dependency.

Additionally, starting with gopls@v0.17.0, the _go command version_ will narrow
from 4 versions to 3. This is more consistent with the Go Release Policy.

Gopls supports **all** Go versions as its _language version_, by providing
compiler errors based on the language version and filtering available standard
library symbols based on the standard library APIs available at that Go
version.

Maintaining support for building gopls with legacy versions of Go caused
[significant friction](https://go.dev/issue/50825) for gopls maintainers and
held back other improvements. If you are unable to install a supported version
of Go on your system, you can still install an older version of gopls. The
following table shows the final gopls version that supports a given Go version.
Go releases more recent than those in the table can be used with any version of
gopls.

| Go Version  | Final gopls version with support (without warnings) |
| ----------- | --------------------------------------------------- |
| Go 1.12     | [gopls@v0.7.5](https://github.com/golang/tools/releases/tag/gopls%2Fv0.7.5) |
| Go 1.15     | [gopls@v0.9.5](https://github.com/golang/tools/releases/tag/gopls%2Fv0.9.5) |
| Go 1.17     | [gopls@v0.11.0](https://github.com/golang/tools/releases/tag/gopls%2Fv0.11.0) |
| Go 1.18     | [gopls@v0.14.2](https://github.com/golang/tools/releases/tag/gopls%2Fv0.14.2) |
| Go 1.20     | [gopls@v0.15.3](https://github.com/golang/tools/releases/tag/gopls%2Fv0.15.3) |

### Supported build systems

`gopls` currently only supports the `go` command, so if you are using
a different build system, `gopls` will not work well. Bazel is not officially
supported, but may be made to work with an appropriately configured
`go/packages` driver. See
[bazelbuild/rules_go#512](https://github.com/bazelbuild/rules_go/issues/512)
for more information.
You can follow [these instructions](https://github.com/bazelbuild/rules_go/wiki/Editor-setup)
to configure your `gopls` to work with Bazel.

### Troubleshooting

If you are having issues with `gopls`, please follow the steps described in the
[troubleshooting guide](doc/troubleshooting.md).

## Additional information

* [Features](doc/features.md)
* [Command-line interface](doc/command-line.md)
* [Advanced topics](doc/advanced.md)
* [Contributing to `gopls`](doc/contributing.md)
* [Integrating `gopls` with an editor](doc/design/integrating.md)
* [Design requirements and decisions](doc/design/design.md)
* [Implementation details](doc/design/implementation.md)
* [Open issues](https://github.com/golang/go/issues?q=is%3Aissue+is%3Aopen+label%3Agopls)

[language server]: https://langserver.org
[LSP]: https://microsoft.github.io/language-server-protocol/
