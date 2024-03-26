# Sublime Text

Setting up Sublime Text for Golang development.

## Installation

Make sure Go is installed and available in your `PATH`. Follow the [Go documentation][golang-installation] to get Go up and running quickly.
You can verify that Go is properly installed and available by typing `go help` in a terminal. The command should print a help text instead of an error message.

Use [Package Control] to install the following packages:

- [LSP] provides language server support in Sublime Text
- [LSP-gopls] the helper package for gopls
- (optionally) [Gomod] and [Golang Build] for `go.mod` file syntax highlighting and a Go build system

gopls is automatically installed and activated when a `.go` file is opened.

## Configuration

Here are some ways to configure the package and the language server. See [the documentation][gopls-settings] for all available settings.

### Global configuration
- Configure the LSP package by navigating to `Preferences > Package Settings > LSP > Settings` or by executing the `Preferences: LSP Settings` command in the command palette.
- Configure gopls by navigating to `Preferences > Package Settings > LSP > Servers > LSP-gopls` or by executing the `Preferences: LSP-gopls Settings` command in the command palette.

### Project-specific configuration
From the command palette run `Project: Edit Project` and add your settings in:

```js
{
  "settings": {
    "LSP": {
      "gopls": {
        "settings": {
          // Put your settings here
        }
      }
    }
  }
}
```

### Formatting

It is recommended to auto-format Go files using the language server when saving.
This can be enabled either globally in the LSP settings (see above) or for the Go syntax only.

To enable formatting for the Go syntax only, open a Go file and open the syntax-specific settings by navigating to `Preferences > Settings - Syntax Specific`.

Add `"lsp_format_on_save": true` to the outermost curly braces:

```js
// These settings override both User and Default settings for the Go syntax
{
  "lsp_format_on_save": true,
}
```

### Custom gopls executable

You can use a custom gopls executable by setting the path in the LSP-gopls settings (see above).
```js
{
  "command": [
    "path/to/custom/gopls"
  ],
}
```

### Custom environment

You can modify the environment variables that are presented to gopls by modifying the `env` setting in the LSP-gopls settings (see above).
```js
{
  "env": {
    "PATH": "/path/to/your/go-dev/bin:/path/to/your/go/bin",
    "GOPATH": "/path/to/your/gopath",
    // add more environment variables here 
  },
}
```


[Package Control]: https://packagecontrol.io/installation
[LSP]: https://packagecontrol.io/packages/LSP
[LSP-gopls]: https://packagecontrol.io/packages/LSP-gopls
[Gomod]: https://packagecontrol.io/packages/Gomod
[Golang Build]: https://packagecontrol.io/packages/Golang%20Build 
[golang-installation]: https://golang.org/doc/install
[gopls-settings]: https://github.com/golang/tools/blob/master/gopls/doc/settings.md
