module golang.org/x/tools/go/packages/packagestest // moribund; see https://go.dev/issue/70229

go 1.23.0

require (
	golang.org/x/tools v0.34.0
	golang.org/x/tools/go/expect v0.0.0
)

require (
	golang.org/x/mod v0.25.0 // indirect
	golang.org/x/sync v0.15.0 // indirect
)

replace (
	golang.org/x/tools => ../../..
	golang.org/x/tools/go/expect => ../../expect
)
