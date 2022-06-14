# Sublime Text

Use the [LSP] package. After installing it using Package Control, do the following:

* Open the **Command Palette**
* Find and run the command **Package Control: Install Package**
* Select the **LSP-gopls** item. See [LSP-gopls] for package information

Finally, you should familiarise yourself with the LSP package's *Settings* and *Key Bindings*. Find them under the menu item **Preferences > Package Settings > LSP**.

## Examples

LSP and LSP-gopls settings can also be adjusted on a per-project basis to override global settings.
```
{
    "folders": [
        {
            "path": "/path/to/a/folder/one"
        },
        {
            // If you happen to be working on Go itself, this can be helpful; go-dev/bin should be on PATH.
            "path": "/path/to/your/go-dev/src/cmd"
        }
     ],
    "settings": {
        "LSP": {
            "LSP-gopls": {
                // To use a specific version of gopls with Sublime Text LSP (e.g., to try new features in development)
                "command": [
                    "/path/to/your/go/bin/gopls"
                ],
                "env": {
                    "PATH": "/path/to/your/go-dev/bin:/path/to/your/go/bin",
                    "GOPATH": "",
                },
                "settings": {
                    "experimentalWorkspaceModule": true
                }
            }
        },
        // This will apply for all languages in this project that have
        // LSP servers, not just Go, however cannot enable just for Go.
        "lsp_format_on_save": true,
    }
}
```

Usually changes to these settings are recognized after saving the project file, but it may sometimes be necessary to either restart the server(s) (**Tools > LSP > Restart Servers**) or quit and restart Sublime Text itself.

[LSP]: https://packagecontrol.io/packages/
[LSP-gopls]: https://packagecontrol.io/packages/LSP-gopls
