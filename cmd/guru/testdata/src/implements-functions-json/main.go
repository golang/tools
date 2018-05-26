package main

// Tests of 'implements' query, -output=json.
// See go.tools/guru/guru_test.go for explanation.
// See main.golden for expected query results.

import _ "lib"

func main() {
}

type FTypeA func(s string)

func ftypea(s string) {} // @implements FTypeA "ftypea"
// guru implements ../guru/testdata/src/implements-functions/main.go:#247

type FTypeB func(i int) // @implements FTypeB "FTypeB"
// guru implements ../guru/testdata/src/implements-functions/main.go:#377

func ftypeb(i int) {}
