---
title: "Gopls: Using Zed"
---

## Install `gopls`

To use `gopls` with [Zed](https://zed.dev/), first
[install the `gopls` executable](../index.md#installation) and ensure that the directory
containing the resulting binary (either `$(go env GOBIN)` or `$(go env
GOPATH)/bin`) is in your `PATH`.

## That's it

Zed has a built-in LSP client and knows to run `gopls` when visiting a
Go source file, so most features work right out of the box.

Zed does not yet support external `window/showDocument` requests,
so web-based features will not work;
see [Zed issue 24852](https://github.com/zed-industries/zed/discussions/24852).
