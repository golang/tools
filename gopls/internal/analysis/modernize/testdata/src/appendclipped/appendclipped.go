package appendclipped

import (
	"os"
	"slices"
)

type (
	Bytes  []byte
	Bytes2 []byte
)

func _(s, other []string) {
	print(append([]string{}, s...))              // want "Replace append with slices.Clone"
	print(append([]string(nil), s...))           // want "Replace append with slices.Clone"
	print(append(Bytes(nil), Bytes{1, 2, 3}...)) // want "Replace append with slices.Clone"
	print(append(other[:0:0], s...))             // want "Replace append with slices.Clone"
	print(append(other[:0:0], os.Environ()...))  // want "Redundant clone of os.Environ()"
	print(append(other[:0], s...))               // nope: intent may be to mutate other

	print(append(append(append([]string{}, s...), other...), other...))             // want "Replace append with slices.Concat"
	print(append(append(append([]string(nil), s...), other...), other...))          // want "Replace append with slices.Concat"
	print(append(append(Bytes(nil), Bytes{1, 2, 3}...), Bytes{4, 5, 6}...))         // want "Replace append with slices.Concat"
	print(append(append(append(other[:0:0], s...), other...), other...))            // want "Replace append with slices.Concat"
	print(append(append(append(other[:0:0], os.Environ()...), other...), other...)) // want "Replace append with slices.Concat"
	print(append(append(other[:len(other):len(other)], s...), other...))            // want "Replace append with slices.Concat"
	print(append(append(slices.Clip(other), s...), other...))                       // want "Replace append with slices.Concat"
	print(append(append(append(other[:0], s...), other...), other...))              // nope: intent may be to mutate other
}

var (
	_ Bytes  = append(Bytes(nil), []byte(nil)...) // nope: correct fix requires Clone[Bytes] (#73661)
	_ Bytes  = append([]byte(nil), Bytes(nil)...) // nope: correct fix requires Clone[Bytes] (#73661)
	_ Bytes2 = append([]byte(nil), Bytes(nil)...) // nope: correct fix requires Clone[Bytes2] (#73661)
)
