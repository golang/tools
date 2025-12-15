package stditerators

import (
	"go/types"
	"reflect"
)

func _(tuple *types.Tuple) {
	for i := 0; i < tuple.Len(); i++ { // want "Len/At loop can simplified using Tuple.Variables iteration"
		print(tuple.At(i))
	}
}

func _(scope *types.Scope) {
	for i := 0; i < scope.NumChildren(); i++ { // want "NumChildren/Child loop can simplified using Scope.Children iteration"
		print(scope.Child(i))
	}
	{
		// tests of shadowing of preferred name at def
		const child = 0
		for i := 0; i < scope.NumChildren(); i++ { // want "NumChildren/Child loop can simplified using Scope.Children iteration"
			print(scope.Child(i))
		}
		for i := 0; i < scope.NumChildren(); i++ { // want "NumChildren/Child loop can simplified using Scope.Children iteration"
			print(scope.Child(i), child)
		}
	}
	{
		for i := 0; i < scope.NumChildren(); i++ {
			const child = 0 // nope: shadowing of fresh name at use
			print(scope.Child(i))
		}
	}
	{
		for i := 0; i < scope.NumChildren(); i++ { // want "NumChildren/Child loop can simplified using Scope.Children iteration"
			elem := scope.Child(i) // => preferred name = "elem"
			print(elem)
		}
	}
	{
		for i := 0; i < scope.NumChildren(); i++ { // want "NumChildren/Child loop can simplified using Scope.Children iteration"
			first := scope.Child(0) // the name heuristic should not be fooled by this
			print(first, scope.Child(i))
		}
	}
}

func _(union, union2 *types.Union) {
	for i := 0; i < union.Len(); i++ { // want "Len/Term loop can simplified using Union.Terms iteration"
		print(union.Term(i))
		print(union.Term(i))
	}
	for i := union.Len() - 1; i >= 0; i-- { // nope: wrong loop form
		print(union.Term(i))
	}
	for i := 0; i <= union.Len(); i++ { // nope: wrong loop form
		print(union.Term(i))
	}
	for i := 0; i <= union.Len(); i++ { // nope: use of i not in x.At(i)
		print(i, union.Term(i))
	}
	for i := 0; i <= union.Len(); i++ { // nope: x.At and x.Len have different receivers
		print(i, union2.Term(i))
	}
}

func _(tuple *types.Tuple) {
	for i := 0; i < tuple.Len(); i++ { // want "Len/At loop can simplified using Tuple.Variables iteration"
		if foo := tuple.At(i); true { // => preferred name = "foo"
			print(foo)
		}
		bar := tuple.At(i)
		print(bar)
	}
	{
		// The name v is already declared, but not
		// used in the loop, so we can use it again.
		v := 1
		print(v)

		for i := 0; i < tuple.Len(); i++ { // want "Len/At loop can simplified using Tuple.Variables iteration"
			print(tuple.At(i))
		}
	}
	{
		// The name v is used from the loop, so
		// we must choose a fresh name.
		v := 1
		for i := 0; i < tuple.Len(); i++ { // want "Len/At loop can simplified using Tuple.Variables iteration"
			print(tuple.At(i), v)
		}
	}
}

func _(t reflect.Type) {
	for i := 0; i < t.NumField(); i++ { // want "NumField/Field loop can simplified using Type.Fields iteration"
		print(t.Field(i))
	}
	for i := 0; i < t.NumMethod(); i++ { // want "NumMethod/Method loop can simplified using Type.Methods iteration"
		print(t.Method(i))
	}
	for i := 0; i < t.NumIn(); i++ { // want "NumIn/In loop can simplified using Type.Ins iteration"
		print(t.In(i))
	}
	for i := 0; i < t.NumOut(); i++ { // want "NumOut/Out loop can simplified using Type.Outs iteration"
		print(t.Out(i))
	}
}

func _(v reflect.Value) {
	for i := 0; i < v.NumField(); i++ { // want "NumField/Field loop can simplified using Value.Fields iteration"
		print(v.Field(i))
	}
	// Ideally we would use both parts of Value.Field's iter.Seq2 here.
	for i := 0; i < v.NumField(); i++ {
		print(v.Field(i), v.Type().Field(i)) // nope
	}
	for i := 0; i < v.NumMethod(); i++ { // want "NumMethod/Method loop can simplified using Value.Methods iteration"
		print(v.Method(i))
	}
}
