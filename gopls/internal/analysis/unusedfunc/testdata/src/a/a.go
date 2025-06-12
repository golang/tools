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

// -- types without methods --

type ExportedType2 int

// self-references don't count
type unusedUnexportedType2 struct{ *unusedUnexportedType2 } // want `type "unusedUnexportedType2" is unused`

type (
	one int
	two one // want `type "two" is unused`
)

// -- generic methods --

type g[T any] int

func (g[T]) method() {} // want `method "method" is unused`

// -- constants --

const unusedConst = 1 // want `const "unusedConst" is unused`

const (
	unusedEnum = iota
)

const (
	constOne       = 1
	unusedConstTwo = constOne // want `const "unusedConstTwo" is unused`
)
