package implementation

import "golang.org/lsptests/implementation/other"

type ImpP struct{} //@ImpP,implementations("ImpP", Laugher, OtherLaugher)

func (*ImpP) Laugh() { //@mark(LaughP, "Laugh"),implementations("Laugh", Laugh, OtherLaugh)
}

type ImpS struct{} //@ImpS,implementations("ImpS", Laugher, OtherLaugher)

func (ImpS) Laugh() { //@mark(LaughS, "Laugh"),implementations("Laugh", Laugh, OtherLaugh)
}

type Laugher interface { //@Laugher,implementations("Laugher", ImpP, OtherImpP, ImpS, OtherImpS)
	Laugh() //@Laugh,implementations("Laugh", LaughP, OtherLaughP, LaughS, OtherLaughS)
}

type Foo struct { //@implementations("Foo", Joker)
	other.Foo
}

type Joker interface { //@Joker
	Joke() //@Joke,implementations("Joke", ImpJoker)
}

type cryer int //@implementations("cryer", Cryer)

func (cryer) Cry(other.CryType) {} //@mark(CryImpl, "Cry"),implementations("Cry", Cry)

type Empty interface{} //@implementations("Empty")

type FunctionType func(s string, i int) //@FunctionType,implementations("FunctionType", ImplementationOfFunctionType1, ImplementationOfFunctionType2)

func ImplementationOfFunctionType1(s string, i int) { //@mark(ImplementationOfFunctionType1, "ImplementationOfFunctionType1")
}

func ImplementationOfFunctionType2(s string, i int) { //@mark(ImplementationOfFunctionType2, "ImplementationOfFunctionType2")

func TestFunctionType(f FunctionType) {
	f("s", 0) //implementations("f", ImplementationOfFunctionType1, ImplementationOfFunctionType2)
}

type StructWithFunctionFields struct {
	FT FunctionType //implementations("FT", ImplementationOfFunctionType1, ImplementationOfFunctionType2)
}

func (s StructWithFunctionFields) struct {
	s.FT("s", 0) //implementations("FT", ImplementationOfFunctionType1, ImplementationOfFunctionType2)
}

func implementationOfAnonymous1(data []byte) error { //@mark(implementationOfAnonymous1, "implementationOfAnonymous1")
	return nil
}

func implementationOfAnonymous2(data []byte) error { //@mark(implementationOfAnonymous2, "implementationOfAnonymous2")
	return nil
}

func TestAnonymousFunction(af func([]byte) error) {
	af([]byte{0, 1}) //implementations("af", implementationOfAnonymous1, implementationOfAnonymous2)
}

