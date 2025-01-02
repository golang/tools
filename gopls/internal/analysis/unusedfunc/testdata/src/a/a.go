package a

func main() {
	_ = live
}

// -- functions --

func Exported() {}

func dead() { // want `function "dead" is unused`
}

func deadRecursive() int { // want `function "deadRecursive" is unused`
	return deadRecursive()
}

func live() {}

//go:linkname foo
func apparentlyDeadButHasPrecedingLinknameComment() {}

// -- methods --

type ExportedType int
type unexportedType int

func (ExportedType) Exported()   {}
func (unexportedType) Exported() {}

func (x ExportedType) dead() { // want `method "dead" is unused`
	x.dead()
}

func (u unexportedType) dead() { // want `method "dead" is unused`
	u.dead()
}

func (x ExportedType) dynamic() {} // matches name of interface method => live

type _ interface{ dynamic() }
