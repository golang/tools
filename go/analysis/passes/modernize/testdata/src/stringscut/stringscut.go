package stringscut

import (
	"bytes"
	"strings"
)

func basic(s string) bool {
	s = "reassigned"
	i := strings.Index(s, "=") // want "strings.Index can be simplified using strings.Cut"
	print(s[:i])
	return i >= 0
}

func basic_contains(s string) bool {
	s = "reassigned"
	i := strings.Index(s, "=") // want "strings.Index can be simplified using strings.Contains"
	return i >= 0
}

func contains_variety(s, sub string) {
	i := strings.Index(s, sub) // want "strings.Index can be simplified using strings.Contains"
	if i >= 0 {
		print("found")
	}
	if i < 0 {
		print("not found")
	}
	if i <= -1 {
		print("not found")
	}
}

func basic_contains_bytes(s string) bool {
	i := strings.IndexByte(s, '=') // want "strings.IndexByte can be simplified using strings.Contains"
	return i < 0
}

func basic_contains_bytes_byte(s []byte) bool {
	i := bytes.IndexByte(s, 22) // want "bytes.IndexByte can be simplified using bytes.Contains"
	return i < 0
}

func skip_var_decl(s string) bool {
	var i int
	i = strings.Index(s, "=") // don't modernize - i might be reassigned
	print(s[:i])
	return i >= 0
}

func basic_substr_arg(s string, substr string) bool {
	i := strings.Index(s, substr) // want "strings.Index can be simplified using strings.Cut"
	print(s[i+len(substr):])
	return i >= 0
}

func wrong_len_arg(s string, substr string) bool {
	i := strings.Index(s, substr) // don't modernize since i+len(s) is not valid
	print(s[i+len(s):])
	return i >= 0
}

func basic_strings_byte(s string) bool {
	i := strings.IndexByte(s, '+') // want "strings.IndexByte can be simplified using strings.Cut"
	print(s[:i])
	return i >= 0
}

func basic_strings_byte_int(s string) bool {
	i := strings.IndexByte(s, 55) // want "strings.IndexByte can be simplified using strings.Cut"
	print(s[:i])
	return i >= 0
}

func basic_strings_byte_var(s string) bool {
	b := byte('b')
	i := strings.IndexByte(s, b) // want "strings.IndexByte can be simplified using strings.Cut"
	print(s[:i])
	return i >= 0
}

func basic_bytes(b []byte) []byte {
	i := bytes.Index(b, []byte("str")) // want "bytes.Index can be simplified using bytes.Cut"
	if i >= 0 {
		return b[:i]
	} else {
		return b[i+3:]
	}
}

func basic_index_bytes(b []byte) string {
	i := bytes.IndexByte(b, 's') // want "bytes.IndexByte can be simplified using bytes.Cut"
	if i >= 0 {
		return string(b[:i])
	} else {
		return string(b[i+1:])
	}
}

func const_substr_len(s string) bool {
	i := strings.Index(s, "=") // want "strings.Index can be simplified using strings.Cut"
	r := s[i+len("="):]
	return len(r) > 0
}

func const_for_len(s string) bool {
	i := strings.Index(s, "=") // want "strings.Index can be simplified using strings.Cut"
	r := s[i+1:]
	return len(r) > 0
}

func index(s string) bool {
	i := strings.Index(s, "=") // want "strings.Index can be simplified using strings.Cut"
	if i < 0 {
		return false
	}
	if i >= 0 {
		return true
	}
	print(s[:i])
	return true
}

func index_flipped(s string) bool {
	i := strings.Index(s, "=") // want "strings.Index can be simplified using strings.Cut"
	if 0 > i {
		return false
	}
	if 0 <= i {
		return true
	}
	print(s[:i])
	return true
}

func invalid_index(s string) bool {
	i := strings.Index(s, "=") // don't modernize since i is used in an "invalid" binaryexpr
	if 0 > i {
		return false
	}
	if i < 10 {
		return true
	}
	return true
}

func invalid_slice(s string) string {
	i := strings.Index(s, "=") // don't modernize since i is used in an "invalid" slice index
	if i >= 0 {
		return s[i+4:]
	}
	return ""
}

func index_and_before_after(s string) string {
	substr := "="
	i := strings.Index(s, substr) // want "strings.Index can be simplified using strings.Cut"
	if i == -1 {
		print("test")
	}
	if i < 0 {
		return ""
	} else {
		if i >= 0 {
			return s[:i]
		} else {
			return s[i+len(substr):]
		}
	}
	if -1 == i {
		return s[len(substr)+i:]
	}
	return "final"
}

func idx_var_init(s string) (string, string) {
	var idx = strings.Index(s, "=") // want "strings.Index can be simplified using strings.Cut"
	return s[0:idx], s
}

func idx_reassigned(s string) string {
	idx := strings.Index(s, "=") // don't modernize since idx gets reassigned
	idx = 10
	return s[:idx]
}

func idx_printed(s string) string {
	idx := strings.Index(s, "=") // don't modernize since idx is used
	print(idx)
	return s[:idx]
}

func idx_aliased(s string) string {
	idx := strings.Index(s, "=") // don't modernize since idx gets aliased
	i := idx
	return s[:i]
}

func idx_aliased_var(s string) string {
	idx := strings.Index(s, "=") // don't modernize since idx gets aliased
	var i = idx
	print(i)
	return s[:idx]
}

func s_modified(s string) string {
	idx := strings.Index(s, "=") // don't modernize since s gets modified
	s = "newstring"
	return s[:idx]
}

func s_modified_no_params() string {
	s := "string"
	idx := strings.Index(s, "=") // don't modernize since s gets modified
	s = "newstring"
	return s[:idx]
}

func s_in_func_call() string {
	s := "string"
	substr := "substr"
	idx := strings.Index(s, substr) // want "strings.Index can be simplified using strings.Cut"
	function(s)
	return s[:idx]
}

func s_pointer() string {
	s := "string"
	idx := strings.Index(s, "s")
	ptr := &s // don't modernize since s may get modified
	reference_str(ptr)
	return s[:idx]
}

func s_pointer_before_call() string {
	s := "string"
	ptr := &s // don't modernize since s may get modified
	reference_str(ptr)
	idx := strings.Index(s, "s")
	return s[:idx]
}

func idx_used_before(s string, sub string) string {
	var index int
	reference_int(&index)
	index = strings.Index(s, sub) // don't modernize since index may get modified
	blank()
	if index >= 0 {
		return s[:index]
	}
	return ""
}

func idx_used_other_substr(s string, sub string) string {
	otherstr := "other"
	i := strings.Index(s, sub)
	print(otherstr[:i]) // don't modernize since i used in another slice expr
	if i >= 0 {
		return s[:i]
	} else {
		return ""
	}
}

func idx_gtr_zero_invalid(s string, sub string) string {
	i := strings.Index(s, sub)
	if i > 0 { // don't modernize since this is a stronger claim than i >= 0
		return s[:i]
	}
	return ""
}

func idx_gtreq_one_invalid(s string, sub string) string {
	i := strings.Index(s, sub)
	if i >= 1 { // don't modernize since this is a stronger claim than i >= 0
		return s[:i]
	}
	return ""
}

func idx_gtr_negone(s string, sub string) string {
	i := strings.Index(s, sub) // want "strings.Index can be simplified using strings.Cut"
	if i > -1 {
		return s[:i]
	}
	if i != -1 {
		return s
	}
	return ""
}

func function(s string) {}

func reference_str(s *string) {}

func reference_int(i *int) {}

func blank() {}
