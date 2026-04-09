---
title: "Gopls: The language server for Go"
---
<!--
  This is the main landing page for gopls users.

  To preview locally edited markdown files, use:
    $ GOLANGORG_LOCAL_X_TOOLS=$(pwd) go run golang.org/x/website/cmd/golangorg@master &
    $ open http://localhost:6060/go.dev/gopls
-->

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
completion, diagnostics, analysis, and refactoring, and a number of
additional features not found in other language servers.

See the [Index of features](features/) for complete
documentation on what Gopls can do for you.

## Editors

To get started with `gopls`, install an LSP plugin in your editor of choice.

<!-- TODO: be more consistent about editor (e.g. Emacs) vs. client (e.g. eglot). -->

* [Acme](https://github.com/9fans/acme-lsp/blob/master/README.md)
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
(On a UNIX machine, you can use the command `killall gopls`.)

Learn more in the
[advanced installation instructions](advanced.md#installing-unreleased-versions).

## Releases

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

## Support policy

Gopls is maintained by engineers on the
[Go tools team](https://github.com/orgs/golang/teams/tools-team/members),
who actively monitor the
[Go](https://github.com/golang/go/issues?q=is%3Aissue+is%3Aopen+label%3Agopls)
and
[VS Code Go](https://github.com/golang/vscode-go/issues) issue trackers.

### Supported Go versions

When using gopls, there are three distinct versions of Go to be aware of:
the `go` toolchain used to build gopls,
the `go` toolchain on the `PATH` when gopls is running,
and the version of Go in which your source code is written.

To build gopls, you must use a toolchain supporting Go 1.21 or later.
Go 1.21 was the first version of Go to support [forward
compatibility](https://go.dev/blog/toolchain), which ensures that any
necessary toolchain upgrades are handled automatically, just like any
other dependency.

While running, gopls executes the `go` command found using `$PATH` to
obtain information about your workspace. Gopls follows the [Go Release
Policy](https://go.dev/doc/devel/release#policy), meaning that we
support only the two most recent major Go releases.
Run `go version` to check this version.
(Supporting older versions caused significant maintenance
[friction](https://go.dev/issue/50825). If you are unable to use a
supported toolchain, you can install an older version of gopls.)

Gopls can analyze code written using any version of Go, and will
tailor its diagnostics and other behavior to the appropriate version
of Go for each source file.
The file's Go version is determined by the `go` directive in the
enclosing go.mod file and by any build tags such as `//go:build
go1.25` within the file itself.

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
