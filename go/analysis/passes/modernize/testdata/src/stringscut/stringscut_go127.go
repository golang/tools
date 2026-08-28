//go:build go1.27

package stringscut

import (
	"bytes"
	"strings"
)

func lastindex_basic(s string) bool {
	i := strings.LastIndex(s, "=") // want "strings.LastIndex can be simplified using strings.CutLast"
	if i >= 0 {
		print(s[:i])
	}
	return i >= 0
}

func lastindex_before_after(s, substr string) bool {
	i := strings.LastIndex(s, substr) // want "strings.LastIndex can be simplified using strings.CutLast"
	if i >= 0 {
		print(s[:i], s[i+len(substr):])
	}
	return i >= 0
}

func lastindex_after_only(s, substr string) bool {
	i := strings.LastIndex(s, substr) // want "strings.LastIndex can be simplified using strings.CutLast"
	if i >= 0 {
		print(s[i+len(substr):])
	}
	return i >= 0
}

func lastindex_byte(s string) bool {
	i := strings.LastIndexByte(s, '/') // want "strings.LastIndexByte can be simplified using strings.CutLast"
	if i >= 0 {
		print(s[:i])
	}
	return i >= 0
}

func lastindex_byte_int(s string) bool {
	i := strings.LastIndexByte(s, 55) // want "strings.LastIndexByte can be simplified using strings.CutLast"
	if i >= 0 {
		print(s[:i])
	}
	return i >= 0
}

func lastindex_byte_var(s string) bool {
	b := byte('b')
	i := strings.LastIndexByte(s, b) // want "strings.LastIndexByte can be simplified using strings.CutLast"
	if i >= 0 {
		print(s[:i])
	}
	return i >= 0
}

func lastindex_bytes(b []byte) []byte {
	i := bytes.LastIndex(b, []byte("str")) // want "bytes.LastIndex can be simplified using bytes.CutLast"
	if i >= 0 {
		return b[:i]
	}
	return nil
}

func lastindex_bytes_byte(b []byte) string {
	i := bytes.LastIndexByte(b, 's') // want "bytes.LastIndexByte can be simplified using bytes.CutLast"
	if i >= 0 {
		return string(b[:i])
	}
	return ""
}

// Should NOT fire: b[i+1:] in else is not guarded.
func lastindex_bytes_byte_unguarded(b []byte) string {
	i := bytes.LastIndexByte(b, 's')
	if i >= 0 {
		return string(b[:i])
	} else {
		return string(b[i+1:])
	}
}

func lastindex_const_for_len(s string) bool {
	i := strings.LastIndex(s, "=") // want "strings.LastIndex can be simplified using strings.CutLast"
	if i >= 0 {
		r := s[i+1:]
		return len(r) > 0
	}
	return false
}

func lastindex_early_return(s string) (string, string) {
	i := strings.LastIndex(s, ":") // want "strings.LastIndex can be simplified using strings.CutLast"
	if i < 0 {
		return "", s
	}
	return s[:i], s[i+1:]
}

func lastindex_neg_else(s string) (string, string) {
	i := strings.LastIndex(s, ":") // want "strings.LastIndex can be simplified using strings.CutLast"
	if i == -1 {
		return "", s
	} else {
		return s[:i], s[i+1:]
	}
}

func lastindex_if_init(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 { // want "strings.LastIndex can be simplified using strings.CutLast"
		return s[:i]
	}
	return s
}
