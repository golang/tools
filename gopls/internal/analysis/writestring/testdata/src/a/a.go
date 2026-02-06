package a

import (
	"bufio"
	"bytes"
	"hash/maphash"
	"os"
	"strings"
)

func _(v string) {
	var s strings.Builder
	a := "a"
	b := "b"
	s.WriteString(a + b) // want "Inefficient string concatenation in call to WriteString"

	var p *string
	s.WriteString(a + ("b" + *p)) // want "Inefficient string concatenation in call to WriteString"

	type S struct{ B bytes.Buffer }
	var sb S
	sb.B.WriteString(a + b) // want "Inefficient string concatenation in call to WriteString"

	var bufw bufio.Writer
	const k = "k"
	bufw.WriteString(a + getString() + "b") // want "Inefficient string concatenation in call to WriteString"
	bufw.WriteString(k + "b" + v + "c")     // want "Inefficient string concatenation in call to WriteString"

	var h maphash.Hash
	h.WriteString(a + b) // want "Inefficient string concatenation in call to WriteString"

	var buf bytes.Buffer
	buf.WriteString(a)            // nope - nothing to extract
	buf.WriteString("a" + "b")    // nope - no need to split because the concatenation is resolved without allocations
	f(buf.WriteString(a + b))     // nope - inside function call
	_, _ = buf.WriteString(a + b) // nope - inside assignment
	getBuf().WriteString(a + b)   // nope - function call in receiver
	var bufs []bytes.Buffer
	bufs[getIndex()].WriteString(a + b) // nope - index expression in receiver

	var f *os.File
	f.WriteString(a + b) // nope - os.File is not an allowed writer for this analyzer
}

func f(i int, err error) {}

func getIndex() int {
	return 0
}

func getString() string {
	return "string"
}

func getBuf() *bytes.Buffer { return &bytes.Buffer{} }
