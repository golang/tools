# Gopls: Using Helix

Configuring `gopls` to work with Helix is rather straightforward. Install `gopls`, and then add it to the `PATH` variable. If it is in the `PATH` variable, Helix will be able to detect it automatically.

The documentation explaining how to install the default language servers for Helix can be found [here](https://github.com/helix-editor/helix/wiki/How-to-install-the-default-language-servers)

## Installing `gopls`

The first step is to install `gopls` on your machine.
You can follow installation instructions [here](https://github.com/golang/tools/tree/master/gopls#installation).

## Setting your path to include `gopls`

Set your `PATH` environment variable to point to `gopls`.
If you used `go install` to download `gopls`, it should be in `$GOPATH/bin`.
If you don't have `GOPATH` set, you can use `go env GOPATH` to find it.

## Additional information

You can find more information about how to set up the LSP formatter [here](https://github.com/helix-editor/helix/wiki/How-to-install-the-default-language-servers#autoformatting).

It is possible to use `hx --health go` to see that the language server is properly set up.

### Configuration

The settings for `gopls` can be configured in the `languages.toml` file.
The official Helix documentation for this can be found [here](https://docs.helix-editor.com/languages.html)

Configuration pertaining to `gopls` should be in the table `language-server.gopls`.

#### How to set flags

To set flags, add them to the `args` array in the `language-server.gopls` section of the `languages.toml` file.

#### How to set LSP configuration

Configuration options can be set in the `language-server.gopls.config` section of the `languages.toml` file, or in the `config` key of the `language-server.gopls` section of the `languages.toml` file.

#### A minimal config example

In the `~/.config/helix/languages.toml` file, the following snippet would set up `gopls` with a logfile located at `/tmp/gopls.log` and enable staticcheck.

```toml
[language-server.gopls]
command = "gopls"
args = ["-logfile=/tmp/gopls.log",  "serve"]
[language-server.gopls.config]
"ui.diagnostic.staticcheck" = true
```


