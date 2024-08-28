//go:build ignore
// +build ignore

package subpkg

type InterfaceF interface {
	F()
}

type A byte // instantiated but not a reflect type

func (A) F() {} // reachable: exported method of reflect type

type B int // a reflect type

func (*B) F() {} // reachable: exported method of reflect type

type B2 int // a reflect type, and *B2 also

func (B2) F() {} // reachable: exported method of reflect type

type C string

func (C) F() {} // reachable: exported by NewInterfaceF

func NewInterfaceF() InterfaceF {
	return C("")
}

type D uint // instantiated only in dead code

func (*D) F() {} // unreachable
