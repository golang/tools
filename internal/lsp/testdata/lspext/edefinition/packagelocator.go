package edefinition

import (
	"fmt"

	"golang.org/x/tools/internal/jsonrpc2"
)

// There is a bug in this test framework that it can't get the identifier from the imports nonofficial package in the
// test process. Like can get the identifier 'Println' from "fmt", but can't get the identifier 'Direction' from 'jsonrpc2'.
func pkgloc() { //@packagelocator("loc", "edefinition", "golang.org/x/tools/internal/lsp/lspext/edefinition")
	var d jsonrpc2.Direction // @packagelocator("Dir", "jsonrpc2", "golang.org/x/tools/internal/jsonrpc2")
	fmt.Println(d.String())  //@packagelocator("rintln", "fmt", "fmt")
}
