# Gopls: Command-line interface

The `gopls` command provides a number of subcommands that expose much
of the server's functionality. However, the interface is currently
**experimental** and **subject to change at any point.**
It is not efficient, complete, flexible, or officially supported.

Its primary use is as a debugging aid.
For example, this command reports the location of references to the
symbol at the specified file/line/column:

```
$ gopls references ./gopls/main.go:35:8
Log: Loading packages...
Info: Finished loading packages.
/home/gopher/xtools/go/packages/gopackages/main.go:27:7-11
/home/gopher/xtools/gopls/internal/cmd/integration_test.go:1062:7-11
/home/gopher/xtools/gopls/internal/test/integration/bench/bench_test.go:59:8-12
/home/gopher/xtools/gopls/internal/test/integration/regtest.go:140:8-12
/home/gopher/xtools/gopls/main.go:35:7-11
```

See golang/go#63693 for a discussion of its future.

Learn about available commands and flags by running `gopls help`.

Positions within files are specified as `file.go:line:column` triples,
where the line and column start at 1, and columns are measured in
bytes of the UTF-8 encoding.
Alternatively, positions may be specified by the byte offset within
the UTF-8 encoding of the file, starting from zero, for example
`file.go:#1234`.
(When working in non-ASCII files, beware that your editor may report a
position's offset within its file using a different measure such as
UTF-16 codes, Unicode code points, or graphemes).
