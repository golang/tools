module golang.org/x/tools/godoc

go 1.24.0

require (
	github.com/yuin/goldmark v1.7.13
	golang.org/x/tools v0.35.0 // a requirement on an already released version wouldn't build because of ambiguous import errors
)

require golang.org/x/mod v0.27.0 // indirect

replace golang.org/x/tools => ../
