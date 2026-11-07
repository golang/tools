// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package driverutil_test

import (
	"bytes"
	"encoding/json"
	"go/token"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/internal/analysis/driverutil"
)

func TestJSONEndPositions(t *testing.T) {
	fset := token.NewFileSet()
	file := fset.AddFile("example.go", -1, 30)
	file.SetLines([]int{0, 10, 20})

	for _, test := range []struct {
		name       string
		end        token.Pos
		relatedEnd token.Pos
		wantEnd    string
		wantRelEnd string
	}{
		{"both present", file.Pos(23), file.Pos(16), "example.go:3:4", "example.go:2:7"},
		{"both omitted", token.NoPos, token.NoPos, "example.go:1:3", "example.go:2:3"},
		{"diagnostic only", file.Pos(23), token.NoPos, "example.go:3:4", "example.go:2:3"},
		{"related only", token.NoPos, file.Pos(16), "example.go:1:3", "example.go:2:7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree := make(driverutil.JSONTree)
			tree.Add(fset, "pkg", "analyzer", []analysis.Diagnostic{{
				Pos:     file.Pos(2),
				End:     test.end,
				Message: "primary diagnostic",
				Related: []analysis.RelatedInformation{{
					Pos:     file.Pos(12),
					End:     test.relatedEnd,
					Message: "related information",
				}},
			}}, nil)

			var out bytes.Buffer
			if err := tree.Print(&out); err != nil {
				t.Fatal(err)
			}
			var got map[string]map[string][]struct {
				Posn    string `json:"posn"`
				End     string `json:"end"`
				Related []struct {
					Posn string `json:"posn"`
					End  string `json:"end"`
				} `json:"related"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			diagnostics := got["pkg"]["analyzer"]
			if len(diagnostics) != 1 || len(diagnostics[0].Related) != 1 {
				t.Fatalf("unexpected diagnostics: %s", &out)
			}
			diag := diagnostics[0]
			if diag.Posn != "example.go:1:3" || diag.End != test.wantEnd {
				t.Errorf("diagnostic range = %s to %s, want example.go:1:3 to %s", diag.Posn, diag.End, test.wantEnd)
			}
			related := diag.Related[0]
			if related.Posn != "example.go:2:3" || related.End != test.wantRelEnd {
				t.Errorf("related range = %s to %s, want example.go:2:3 to %s", related.Posn, related.End, test.wantRelEnd)
			}
		})
	}
}
