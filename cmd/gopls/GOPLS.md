# `GOPLS.md`

## development

1. Create a fork of <https://github.com/golang/tools>, e.g., <https://github.com/banaio/tools>.
2. Clone your fork.
3. Modify some code in the `internal/lsp` directory. A good candidate is the `Initialize` function
in `internal/lsp/server.go`.
4. Run the `cmd/gopls/install-gopls.sh` script to update the `gopls` binary.

```sh
git clone git@github.com:banaio/tools.git
cd tools
vim internal/lsp/server.go # modify some code
cmd/gopls/install-gopls.sh
```
