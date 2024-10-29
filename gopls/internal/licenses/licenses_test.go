// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package licenses_test

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestLicenses(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("generating licenses only works on Unixes")
	}
	tmp, err := os.CreateTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	if out, err := exec.Command("./gen-licenses.sh", tmp.Name()).CombinedOutput(); err != nil {
		t.Fatalf("generating licenses failed: %q, %v", out, err)
	}

	got, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("licenses.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("combined license text needs updating. Run: `go generate ./internal/licenses` from the gopls module.")
	}
}
