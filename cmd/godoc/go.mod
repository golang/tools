module golang.org/x/tools/cmd/godoc

go 1.24.0

require golang.org/x/tools v0.35.0 // a requirement on an already released version wouldn't build because of ambiguous import errors

require (
	github.com/yuin/goldmark v1.4.13 // indirect
	golang.org/x/mod v0.27.0 // indirect
	golang.org/x/sync v0.16.0 // indirect
)

replace golang.org/x/tools => ../../
