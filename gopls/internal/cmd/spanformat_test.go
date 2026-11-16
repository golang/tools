// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
)

func TestSpanFormat(t *testing.T) {
	formats := []string{"%v", "%#v", "%+v"}

	// Element 0 is the input, and the elements 0-2 are the expected
	// output in [%v %#v %+v] formats. Thus the first must be in
	// canonical form (invariant under parseSpan + fmt.Sprint).
	// The '#' form displays offsets; the '+' form outputs a URI.
	// If len=4, element 0 is a noncanonical input and 1-3 are expected outputs.
	for _, test := range [][]string{
		{"C:/file_a", "C:/file_a", "file:///C:/file_a:#0"},
		{"C:/file_b:1:2", "C:/file_b:1:2", "file:///C:/file_b:1:2"},
		{"C:/file_c:1000", "C:/file_c:1000", "file:///C:/file_c:1000:1"},
		{"C:/file_d:14:9", "C:/file_d:14:9", "file:///C:/file_d:14:9"},
		{"C:/file_e:1:2-7", "C:/file_e:1:2-7", "file:///C:/file_e:1:2-1:7"},
		{"C:/file_f:500-502", "C:/file_f:500-502", "file:///C:/file_f:500:1-502:1"},
		{"C:/file_f:500:1-502:1", // not canonical
			"C:/file_f:500-502", "C:/file_f:500-502", "file:///C:/file_f:500:1-502:1"},
		{"C:/file_g:3:7-8", "C:/file_g:3:7-8", "file:///C:/file_g:3:7-3:8"},
		{"C:/file_g:3:7-3:8", // not canonical
			"C:/file_g:3:7-8", "C:/file_g:3:7-8", "file:///C:/file_g:3:7-3:8"},
		{"C:/file_h:3:7-4:8", "C:/file_h:3:7-4:8", "file:///C:/file_h:3:7-4:8"},
		{"C:/file_i:#100", "C:/file_i:#100", "file:///C:/file_i:#100"},
		{"C:/file_i:#0", // not canonical: offset 0 is start of file
			"C:/file_i", "C:/file_i", "file:///C:/file_i:#0"},
		{"C:/file_i:1:1", // not canonical: 1:1 is start of file
			"C:/file_i", "C:/file_i", "file:///C:/file_i:1:1"},
		{"C:/file_j:#0-#10", "C:/file_j:#0-#10", "file:///C:/file_j:#0-0#10"},
		{"C:/file_j:#26-#28", "C:/file_j:#26-#28", "file:///C:/file_j:#26-0#28"}, // 0#28?
		{"C:/file_m:3-4:8", // not canonical: start has line only, end has line:col
			"C:/file_m:3:1-4:8", "C:/file_m:3:1-4:8", "file:///C:/file_m:3:1-4:8"},
		// Special characters (':', '#', '-') in directory names.
		{"C:/a:b/c-d.go:3:4", "C:/a:b/c-d.go:3:4", "file:///C:/a:b/c-d.go:3:4"},
		{"C:/my-dir/my-file.go:1:1-5", "C:/my-dir/my-file.go:1:1-5", "file:///C:/my-dir/my-file.go:1:1-1:5"},
	} {
		input := test[0]
		spn, err := parseSpanErr(input)
		if err != nil {
			t.Errorf("parseSpanErr(%q) unexpected error: %v", input, err)
			continue
		}
		wants := test[0:3]
		if len(test) == 4 {
			wants = test[1:4]
		}
		for i, format := range formats {
			want := toPath(wants[i])
			if got := fmt.Sprintf(format, spn); got != want {
				t.Errorf("Sprintf(%q, %q) = %q, want %q", format, input, got, want)
			}
		}
	}

	// Internally constructed spans (e.g. from offsetSpan) carry both line/col
	// and byte offset; verify all three formats on such a span.
	both := newSpan(protocol.URIFromPath("C:/file_h"), newPoint(3, 7, 26), newPoint(4, 8, 37))
	for i, want := range []string{"C:/file_h:3:7-4:8", "C:/file_h:#26-#37", "file:///C:/file_h:3:7#26-4:8#37"} {
		want = toPath(want)
		if got := fmt.Sprintf(formats[i], both); got != want {
			t.Errorf("Sprintf(%q, both) = %q, want %q", formats[i], got, want)
		}
	}
}

func TestParseSpanErrors(t *testing.T) {
	for _, tc := range []struct {
		input   string
		wantErr string
	}{
		// Empty input, URIs, or missing file name
		{input: "", wantErr: "empty input"},
		{input: "file:///path/to/file.go:1:2", wantErr: "URI not supported"},
		{input: ":1:2", wantErr: "missing file name"},
		{input: ":10", wantErr: "missing file name"},
		{input: "dir/", wantErr: "missing file name"},
		{input: "dir/:1:2", wantErr: "missing file name"},
		{input: "dir/:10", wantErr: "missing file name"},

		// '#' without preceding ':'
		{input: "file.go#10", wantErr: "'#' must be preceded by ':'"},
		{input: "file1#10:20", wantErr: "'#' must be preceded by ':'"},

		// Missing or malformed coordinates and offsets
		{input: "file.go:", wantErr: "missing position after ':'"},
		{input: "file.go:abc", wantErr: `invalid line/column number "abc"`},
		{input: "file.go:+1", wantErr: `invalid line/column number "+1"`},
		{input: "file.go:--1:2", wantErr: `invalid line number "--1"`},
		{input: "file.go:1:abc", wantErr: `invalid column number "abc"`},
		{input: "file.go:1:", wantErr: "missing column number after ':'"},
		{input: "file.go::2", wantErr: "missing line number before ':'"},
		{input: "file.go:1:2:3", wantErr: "too many colons"},
		{input: "file.go:#", wantErr: "missing offset after '#'"},
		{input: "file.go:#abc", wantErr: `invalid offset "abc"`},
		{input: "file.go:#10#20", wantErr: "multiple '#'"},

		// Malformed range separators
		{input: "file.go:1:2-", wantErr: "missing end position after '-'"},
		{input: "file.go:1-2-3", wantErr: "multiple '-' range separators"},

		// Zero or negative line, column, or offset
		{input: "file.go:0", wantErr: "line/column number must be >= 1 (got 0)"},
		{input: "file.go:0:1", wantErr: "line number must be >= 1 (got 0)"},
		{input: "file.go:1:0", wantErr: ": column number must be >= 1 (got 0)"},
		{input: "file.go:#-1", wantErr: "offset must be >= 0 (got -1)"},
		{input: "file.go:0-2", wantErr: "line/column number must be >= 1 (got 0)"},
		{input: "file.go:1--2", wantErr: "line/column number must be >= 1 (got -2)"},

		// Reversed ranges (end < start)
		{input: "file.go:10-5", wantErr: "end line (5) is before start line (10)"},
		{input: "file.go:3:8-7", wantErr: "end column (7) is before start column (8) on line 3"},
		{input: "file.go:#28-#26", wantErr: "end offset (26) is before start offset (28)"},

		// Mixing line/column and '#offset'
		{input: "file.go:1:2#10", wantErr: "cannot specify both line/column and '#offset'"},
		{input: "file.go:#26-28", wantErr: "cannot mix line/column and '#offset' in a range"},
		{input: "file.go:3:7-#28", wantErr: "cannot mix line/column and '#offset' in a range"},
	} {
		spn, err := parseSpanErr(tc.input)
		if err == nil {
			t.Errorf("parseSpanErr(%q) = %+v, want error containing %q", tc.input, spn, tc.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("parseSpanErr(%q) error = %q, want error containing %q", tc.input, err.Error(), tc.wantErr)
		}
	}
}

func TestSpanFlagValue(t *testing.T) {
	var zero locationFlag
	if zero.IsValid() || zero.HasPosition() || zero.HasOffset() || zero.String() != "" {
		t.Errorf("zero spanFlag: IsValid=%v HasPosition=%v HasOffset=%v String=%q; want false, false, false, \"\"",
			zero.IsValid(), zero.HasPosition(), zero.HasOffset(), zero.String())
	}

	newFlagSet := func(s *locationFlag) *flag.FlagSet {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.Var(s, "span", "a span flag")
		return fs
	}

	// Valid range and point inputs.
	for _, tc := range []struct {
		input     string
		want      string
		wantPoint bool
	}{
		{"C:/file_e:1:2-7", "C:/file_e:1:2-7", false},
		{"C:/file_b:14:9", "C:/file_b:14:9", true},
	} {
		var s locationFlag
		if err := newFlagSet(&s).Parse([]string{"-span=" + tc.input}); err != nil {
			t.Fatalf("fs.Parse(-span=%q) unexpected error: %v", tc.input, err)
		}
		if !s.IsValid() || s.IsPoint() != tc.wantPoint {
			t.Errorf("after -span=%q: IsValid=%v IsPoint=%v; want true, %v", tc.input, s.IsValid(), s.IsPoint(), tc.wantPoint)
		}
		if got, want := s.String(), toPath(tc.want); got != want {
			t.Errorf("after -span=%q: String() = %q, want %q", tc.input, got, want)
		}
	}

	// Invalid input propagates the error without stuttering "invalid span", and leaves flag unset.
	var s locationFlag
	err := newFlagSet(&s).Parse([]string{"-span=file.go:abc"})
	if err == nil || !strings.Contains(err.Error(), `invalid line/column number "abc"`) || !strings.Contains(err.Error(), "for flag -span") {
		t.Errorf("fs.Parse(-span=file.go:abc) error = %v, want flag error with parse reason", err)
	}
	if err != nil && strings.Contains(err.Error(), "invalid span") {
		t.Errorf("fs.Parse(-span=file.go:abc) error = %v, should not stutter 'invalid span'", err)
	}
	if s.IsValid() {
		t.Errorf("after failed fs.Parse, flag is valid")
	}

	// printFlagDefaults uses HelpType and HelpString, and omits "(default ...)".
	fs := newFlagSet(&s)
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	printFlagDefaults(fs)
	got := strings.Join(strings.Fields(buf.String()), " ")
	want := "-span=location a span flag. " + s.HelpString()
	if got != want {
		t.Errorf("printFlagDefaults =\n  %q\nwant:\n  %q", got, want)
	}
}

// TestParseSpanFallback checks that parseSpan treats input that
// parseSpanErr rejects as a file path with no position.
func TestParseSpanFallback(t *testing.T) {
	if _, err := parseSpanErr("C:/file.go:10-5"); err == nil {
		t.Fatal("parseSpanErr(\"C:/file.go:10-5\") succeeded, want error")
	}
	if got, want := fmt.Sprint(parseSpan("C:/file.go:10-5")), toPath("C:/file.go:10-5"); got != want {
		t.Errorf("parseSpan(invalid) = %q, want %q", got, want)
	}
	if got, want := fmt.Sprint(parseSpan("C:/file.go:3:7-4:8")), toPath("C:/file.go:3:7-4:8"); got != want {
		t.Errorf("parseSpan(valid) = %q, want %q", got, want)
	}
}

func toPath(value string) string {
	if strings.HasPrefix(value, "file://") {
		return value
	}
	return filepath.FromSlash(value)
}
