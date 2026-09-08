// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gcexportdata_test

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/internal/testenv"
)

// TestDeps ensures that gcexportdata has minimal dependencies,
// since it is vendored into std for use by go/importer.
func TestDeps(t *testing.T) {
	cmd := testenv.Command(t, "go", "list", "-deps", "golang.org/x/tools/go/gcexportdata")
	cmd.Stdout = new(strings.Builder)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"golang.org/x/tools/go/types/objectpath": true,
		"golang.org/x/tools/internal/pkgbits":    true,
		"golang.org/x/tools/internal/gcimporter": true,
		"golang.org/x/tools/go/gcexportdata":     true,
	}
	for dep := range strings.SplitSeq(fmt.Sprint(cmd.Stdout), "\n") {
		if strings.HasPrefix(dep, "golang.org/x/tools/") && !allowed[dep] {
			t.Errorf("gcexportdata has disallowed dependency on %q", dep)
		}
	}
}
