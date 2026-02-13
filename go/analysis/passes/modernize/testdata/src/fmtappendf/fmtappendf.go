package fmtappendf

import (
	"fmt"
)

func two() string {
	return "two"
}

func bye() {
	_ = []byte(fmt.Sprintf("bye %d", 1)) // want "Replace .*Sprintf.* with fmt.Appendf"
}

func funcsandvars() {
	one := "one"
	_ = []byte(fmt.Sprintf("bye %d %s %s", 1, two(), one)) // want "Replace .*Sprintf.* with fmt.Appendf"
}

func typealias() {
	type b = byte
	type bt = []byte
	_ = []b(fmt.Sprintf("bye %d", 1)) // want "Replace .*Sprintf.* with fmt.Appendf"
	_ = bt(fmt.Sprintf("bye %d", 1))  // want "Replace .*Sprintf.* with fmt.Appendf"
}

func otherprints() {
	_ = []byte(fmt.Sprint("bye %d", 1))   // want "Replace .*Sprint.* with fmt.Append"
	_ = []byte(fmt.Sprintln("bye %d", 1)) // want "Replace .*Sprintln.* with fmt.Appendln"
}

func comma() {
	type S struct{ Bytes []byte }
	var _ = struct{ A S }{
		A: S{
			Bytes: []byte( // want "Replace .*Sprint.* with fmt.Appendf"
				fmt.Sprintf("%d", 0),
			),
		},
	}
	_ = []byte( // want "Replace .*Sprint.* with fmt.Appendf"
		fmt.Sprintf("%d", 0),
	)
}

func emptystring() {
	// empty string edge case only applies to Sprintf
	_ = []byte(fmt.Sprintln("")) // want "Replace .*Sprintln.* with fmt.Appendln"
	// nope - these return []byte{}, while the fmt.Append version returns nil
	_ = []byte(fmt.Sprintf(""))
	_ = []byte(fmt.Sprintf("%s", ""))
	_ = []byte(fmt.Sprintf("%#s", ""))
	_ = []byte(fmt.Sprintf("%s%v", "", getString()))
	// conservatively omitting a suggested fix (ignoring precision and args)
	_ = []byte(fmt.Sprintf("%.0q", "notprinted"))
	_ = []byte(fmt.Sprintf("%v", "nonempty"))
	// has non-operation characters
	_ = []byte(fmt.Sprintf("%vother", "")) // want "Replace .*Sprint.* with fmt.Appendf"
}

func getString() string {
	return ""
}
