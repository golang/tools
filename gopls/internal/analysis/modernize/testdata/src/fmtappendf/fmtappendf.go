package fmtappendf

import (
	"fmt"
)

func two() string {
	return "two"
}

func bye() {
	bye := []byte(fmt.Sprintf("bye %d", 1)) // want "Replace .*Sprintf.* with fmt.Appendf"
	print(bye)
}

func funcsandvars() {
	one := "one"
	bye := []byte(fmt.Sprintf("bye %d %s %s", 1, two(), one)) // want "Replace .*Sprintf.* with fmt.Appendf"
	print(bye)
}

func typealias() {
	type b = byte
	type bt = []byte
	bye := []b(fmt.Sprintf("bye %d", 1)) // want "Replace .*Sprintf.* with fmt.Appendf"
	print(bye)
	bye = bt(fmt.Sprintf("bye %d", 1)) // want "Replace .*Sprintf.* with fmt.Appendf"
	print(bye)
}

func otherprints() {
	sprint := []byte(fmt.Sprint("bye %d", 1)) // want "Replace .*Sprintf.* with fmt.Appendf"
	print(sprint)
	sprintln := []byte(fmt.Sprintln("bye %d", 1)) // want "Replace .*Sprintf.* with fmt.Appendf"
	print(sprintln)
}
