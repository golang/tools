// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmdtest

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/internal/lsp/cmd"
	"golang.org/x/tools/internal/lsp/tests"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/internal/tool"
)

func (r *runner) Link(t *testing.T, uri span.URI, wantLinks []tests.Link) {
	filename := uri.Filename()
	args := []string{"links", filename}
	app := cmd.New("gopls-test", r.data.Config.Dir, r.data.Exported.Config.Env, r.options)
	got := CaptureStdOut(t, func() {
		_ = tool.Run(r.ctx, app, args)
	})
	got = strings.Trim(got, "\n") // remove extra new line
	gotStrings := strings.Split(got, "\n")
	// The files that are checked include `expect.Note`'s which also include expected links.
	// For cmd testing we cannot ignore these comments so we get duplicates. So hence we select only the uniques.
	uniques := make(map[string]struct{})
	for _, v := range gotStrings {
		uniques[v] = struct{}{}
	}
	var result []string
	for k := range uniques {
		result = append(result, k)
	}
	sort.Strings(result)

	var wantStrings []string
	for _, v := range wantLinks {
		wantStrings = append(wantStrings, v.Target)
	}
	sort.Strings(wantStrings)
	if !reflect.DeepEqual(result, wantStrings) {
		t.Errorf("links not equal for %s, expected:\n%v\ngot:\n%v", filename, wantStrings, result)
	}
}
