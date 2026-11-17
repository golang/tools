// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package present

import "testing"

func TestAddrToByteRangeLineNumbers(t *testing.T) {
	tests := []struct {
		name           string
		data           string
		addr           string
		wantLo, wantHi int
		wantErr        bool
	}{
		{"single line", "first", "1", 0, 5, false},
		{"last line", "first\nlast", "2", 6, 10, false},
		{"line range", "first\nlast", "1,2", 0, 10, false},
		{"relative line", "first\nlast", "1+1", 6, 10, false},
		{"first line", "first\nlast", "1", 0, 6, false},
		{"trailing newline", "first\nlast\n", "2", 6, 11, false},
		{"range with trailing newline", "first\nlast\n", "1,2", 0, 11, false},
		{"blank last line", "first\n\n", "2", 6, 7, false},
		{"empty file", "", "1", 0, 0, true},
		{"single line out of range", "first", "2", 0, 0, true},
		{"out of range", "first\nlast", "3", 0, 0, true},
		{"range out of range", "first\nlast", "1,3", 0, 0, true},
		{"past trailing newline", "first\nlast\n", "3", 0, 0, true},
		{"past last line", "first\nlast", "2+1", 0, 0, true},
		{"past newline", "first\n", "1+1", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lo, hi, err := addrToByteRange(tt.addr, 0, []byte(tt.data))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("addrToByteRange(%q, 0, %q) = (%d, %d, nil), want error", tt.addr, tt.data, lo, hi)
				}
				return
			}
			if err != nil {
				t.Fatalf("addrToByteRange(%q, 0, %q): %v", tt.addr, tt.data, err)
			}
			if lo != tt.wantLo || hi != tt.wantHi {
				t.Errorf("addrToByteRange(%q, 0, %q) = (%d, %d), want (%d, %d)", tt.addr, tt.data, lo, hi, tt.wantLo, tt.wantHi)
			}
		})
	}
}
