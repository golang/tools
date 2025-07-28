---
title: "Gopls: The language server for Go"
---
<!--
  This is the main landing page for gopls users.

  To preview locally edited markdown files, use:
    $ GOLANGORG_LOCAL_X_TOOLS=$(pwd) go run golang.org/x/website/cmd/golangorg@master &
    $ open http://localhost:6060/go.dev/gopls
-->

[![PkgGoDev](https://pkg.go.dev/badge/golang.org/x/tools/gopls)](https://pkg.go.dev/golang.org/x/tools/gopls)

`gopls` (pronounced "Go please") is the official [language
server](https://langserver.org) for Go, developed by the Go team. It
provides a wide variety of [IDE features](features/) to any
[LSP](https://microsoft.github.io/language-server-protocol/)-compatible
editor.

<!--TODO(rfindley): Add gifs here.-->

You should not need to interact with `gopls` directly--it will be automatically
integrated into your editor. The specific features and settings vary slightly
by editor, so we recommend that you proceed to the
[documentation for your editor](#editors) below.
Also, the gopls documentation for each feature describes whether it is
supported in each client editor.

This documentation (https://go.dev/gopls) describes the most recent release of gopls.
To preview documentation for the release under development, visit https://tip.golang.org/gopls.

## Features

Gopls supports a wide range of standard LSP features for navigation,
completion, diagnostics, analysis, and refactoring, and number of
additional features not found in other language servers.

See the [Index of features](features/) for complete
documentation on what Gopls can do for you.

## Editors

To get started with `gopls`, install an LSP plugin in your editor of choice.

<!-- TODO: be more consistent about editor (e.g. Emacs) vs. client (e.g. eglot). -->

* [Acme](https://github.com/fhs/acme-lsp/blob/master/README.md)
* [Atom](https://github.com/MordFustang21/ide-gopls/blob/master/README.md)
* [Emacs](editor/emacs.md)
* [Helix](editor/helix.md)
* [Lapce](https://github.com/lapce-community/lapce-go/blob/master/README.md)
* [Sublime Text](editor/sublime.md)
* [VS Code](https://github.com/golang/vscode-go/blob/master/README.md)
* [Vim or Neovim](editor/vim.md)
* [Zed](editor/zed.md)

If you use `gopls` with an editor that is not on this list, please send us a CL
[updating this documentation](contributing.md).

## Installation

To install the latest stable release of `gopls`, run the following command:

```sh
go install golang.org/x/tools/gopls@latest
```

Some editors, such as VS Code, will handle this step for you, and
ensure that Gopls is updated when a new stable version is released.

After updating, you may need to restart running Gopls processes to
observe the effect. Each client has its own way to restart the server.
(On a UNIX machine, you can use the commmand `killall gopls`.)

Learn more in the
[advanced installation instructions](advanced.md#installing-unreleased-versions).

## Release policy

Gopls [releases](release/) follow [semantic versioning](http://semver.org), with
major changes and new features introduced only in new minor versions
(i.e. versions of the form `v*.N.0` for some N). Subsequent patch
releases contain only cherry-picked fixes or superficial updates.

In order to align with the
[Go release timeline](https://github.com/golang/go/wiki/Go-Release-Cycle#timeline),
we aim to release a new minor version of Gopls approximately every three
months, with patch releases approximately every month, according to the
following table:

| Month   | Version(s)   |
| ----    | -------      |
| Jan     | `v*.<N+0>.0` |
| Jan-Mar | `v*.<N+0>.*` |
| Apr     | `v*.<N+1>.0` |
| Apr-Jun | `v*.<N+1>.*` |
| Jul     | `v*.<N+2>.0` |
| Jul-Sep | `v*.<N+2>.*` |
| Oct     | `v*.<N+3>.0` |
| Oct-Dec | `v*.<N+3>.*` |

For more background on this policy, see https://go.dev/issue/55267.

## Setting up your workspace

`gopls` supports both Go module, multi-module and GOPATH modes. See the
[workspace documentation](workspace.md) for information on supported
workspace layouts.

## Configuration

You can configure `gopls` to change your editor experience or view additional
debugging information. Configuration options will be made available by your
editor, so see your [editor's instructions](#editors) for specific details. A
full list of `gopls` settings can be found in the [settings documentation](settings.md).

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
[Go Release Policy](https://golang.org/devel/release.html#policy), meaning
that it officially supports only the two most recent major Go releases.

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
[go/packages driver](https://pkg.go.dev/golang.org/x/tools/go/packages#hdr-The_driver_protocol).
See [bazelbuild/rules_go#512](https://github.com/bazelbuild/rules_go/issues/512)
for more information.
You can follow [these instructions](https://github.com/bazelbuild/rules_go/wiki/Editor-setup)
to configure your `gopls` to work with Bazel.

### Troubleshooting

If you are having issues with `gopls`, please follow the steps described in the
[troubleshooting guide](troubleshooting.md).

## Additional information

* [Command-line interface](command-line.md)
* [Advanced topics](advanced.md)
* [Open issues](https://github.com/golang/go/issues?q=is%3Aissue+is%3Aopen+label%3Agopls)
* [Contributing to `gopls`](contributing.md)
