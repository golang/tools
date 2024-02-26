# Setting up your workspace

**In general, `gopls` should work when you open a Go file contained in your
workspace folder**. If it isn't working for you, or if you want to better
understand how gopls models your workspace, please read on.

## Workspace builds

`gopls` supports both Go module and GOPATH modes. However, it needs a defined
scope in which language features like references, rename, and implementation
should operate. Put differently, gopls needs to infer which `go build`
invocations you would use to build your workspace, including the working
directory, environment, and build flags.

Starting with `gopls@v0.15.0`, gopls will try to guess the builds you are
working on based on the set of open files. When you open a file in a workspace
folder, gopls will check whether the file is contained in a module, `go.work`
workspace, or GOPATH directory, and configure the build accordingly.
Additionally, if you open a file that is constrained to a different operating
system or architecture, for example opening `foo_windows.go` when working on
Linux, gopls will create a scope with `GOOS` and `GOARCH` set to a value that
matches the file.

For example, suppose we had a repository with three modules: `moda`, `modb`,
and `modc`, and a `go.work` file using modules `moda` and `modb`. If we open
the files `moda/a.go`, `modb/b.go`, `moda/a_windows.go`, and `modc/c.go`, gopls
will automatically create three builds:

![Zero Config gopls](zeroconfig.png)

This allows `gopls` to _just work_ when you open a Go file, but it does come with
several caveats:

- This causes gopls to do more work, since it is now tracking three builds
  instead of one. However, the recent
  [scalability redesign](https://go.dev/blog/gopls-scalability)
  allows much of this work to be avoided through efficient caching.
- In some cases this may cause gopls to do more work, since gopls is now
  tracking three builds instead of one. However, the recent
  [scalability redesign](https://go.dev/blog/gopls-scalability) allows us
  to avoid most of this work by efficient caching.
- For operations originating from a given file, including finding references
  and implementations, gopls executes the operation in
  _the default build for that file_. For example, finding references to
  a symbol `S` from `foo_linux.go` will return references from the Linux build,
  and finding references to the same symbol `S` from `foo_windows.go` will
  return references from the Windows build. This is done for performance
  reasons, as in the common case one build is sufficient, but may lead to
  surprising results. Issues [#65757](https://go.dev/issue/65757) and
  [#65755](https://go.dev/issue/65755) propose improvements to this behavior.
- When selecting a `GOOS/GOARCH` combination to match a build-constrained file,
  `gopls` will choose the first matching combination from
  [this list](https://cs.opensource.google/go/x/tools/+/master:gopls/internal/cache/port.go;l=30;drc=f872b3d6f05822d290bc7bdd29db090fd9d89f5c).
  In some cases, that may be surprising.
- When working in a `GOOS/GOARCH` constrained file that does not match your
  default toolchain, `CGO_ENABLED=0` is implicitly set. This means that `gopls`
  will not work in files including `import "C"`. Issue
  [#65758](https://go.dev/issue/65758) may lead to improvements in this
  behavior.
- `gopls` is not able to guess build flags that include arbitrary user-defined
  build constraints. For example, if you are trying to work on a file that is
  constrained by the build directive `//go:build special`, gopls will not guess
  that it needs to create a build with `"buildFlags": ["-tags=special"]`. Issue
  [#65089](https://go.dev/issue/65089) proposes a heuristic by which gopls
  could handle this automatically.

We hope that you provide feedback on this behavior by upvoting or commenting
the issues mentioned above, or opening a [new issue](https://go.dev/issue/new)
for other improvements you'd like to see.

## When to use a `go.work` file for development

Starting with Go 1.18, the `go` command has native support for multi-module
workspaces, via [`go.work`](https://go.dev/ref/mod#workspaces) files. `gopls`
will recognize these files if they are present in your workspace.

Use a `go.work` file when:

- You want to work on multiple modules simultaneously in a single logical
  build, for example if you want changes to one module to be reflected in
  another.
- You want to improve `gopls'` memory usage or performance by reducing the number
  of builds it must track.
- You want `gopls` to know which modules you are working on in a multi-module
  workspace, without opening any files. For example, if you want to use
  `workspace/symbol` queries before any files are open.
- You are using `gopls@v0.14.2` or earlier, and want to work on multiple
  modules.

For example, suppose this repo is checked out into the `$WORK/tools` directory,
and [`x/mod`](https://pkg.go.dev/golang.org/x/mod) is checked out into
`$WORK/mod`, and you are working on a new `x/mod` API for editing `go.mod`
files that you want to simultaneously integrate into `gopls`.

You can work on both `golang.org/x/tools/gopls` and `golang.org/x/mod`
simultaneously by creating a `go.work` file:

```sh
cd $WORK
go work init
go work use tools/gopls mod
```

...followed by opening the `$WORK` directory in your editor.

## When to manually configure `GOOS`, `GOARCH`, or `-tags`

As described in the first section, `gopls@v0.15.0` and later will try to
configure a new build scope automatically when you open a file that doesn't
match the system default operating system (`GOOS`) or architecture (`GOARCH`).

However, per the caveats listed in that section, this automatic behavior comes
with limitations. Customize your `gopls` environment by setting `GOOS` or
`GOARCH` in your
[`"build.env"`](https://github.com/golang/tools/blob/master/gopls/doc/settings.md#env-mapstringstring)
or `-tags=...` in your"
["build.buildFlags"](https://github.com/golang/tools/blob/master/gopls/doc/settings.md#buildflags-string)
when:

- You want to modify the default build environment.
- `gopls` is not guessing the `GOOS/GOARCH` combination you want to use for
  cross platform development.
- You need to work on a file that is constrained by a user-defined build tags,
  such as the build directive `//go:build special`.

## GOPATH mode

When opening a directory within your `GOPATH`, the workspace scope will be just
that directory and all directories contained within it. Note that opening
a large GOPATH directory can make gopls very slow to start.
