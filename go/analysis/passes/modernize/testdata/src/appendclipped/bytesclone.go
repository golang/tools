package appendclipped

import (
	"bytes"
)

var _ bytes.Buffer

func _(b []byte) {
	print(append([]byte{}, b...)) // want "Replace append with bytes.Clone"
}
