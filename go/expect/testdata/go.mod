module αfake1α //@mark(αMarker, "αfake1α")

go 1.14

require golang.org/modfile v0.0.0 //@mark(βMarker, "require golang.org/modfile v0.0.0")
//@mark(IndirectMarker, "// indirect")
require example.com/extramodule v1.0.0 // indirect