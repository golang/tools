package bytescutprefix

import (
	"bytes"
)

func _() {
	if bytes.HasPrefix(bss, bspre) { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a := bytes.TrimPrefix(bss, bspre)
		_ = a
	}
	if bytes.HasPrefix([]byte(""), []byte("")) { // want "HasPrefix \\+ TrimPrefix can be simplified to CutPrefix"
		a := bytes.TrimPrefix([]byte(""), []byte(""))
		_ = a
	}

	if bytes.HasSuffix(bss, bssuf) { // want "HasSuffix \\+ TrimSuffix can be simplified to CutSuffix"
		a := bytes.TrimSuffix(bss, bssuf)
		_ = a
	}
	if bytes.HasSuffix([]byte(""), []byte("")) { // want "HasSuffix \\+ TrimSuffix can be simplified to CutSuffix"
		a := bytes.TrimSuffix([]byte(""), []byte(""))
		_ = a
	}
}
