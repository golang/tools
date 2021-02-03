// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package hooks

import (
	"bytes"
	"io/ioutil"
	"os/exec"
	"runtime"
	"testing"

	"golang.org/x/tools/internal/testenv"
)

func TestLicenses(t *testing.T) {
	// License text differs for older Go versions because staticcheck isn't
	// supported for those versions.
	testenv.NeedsGo1Point(t, 15)

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("generating licenses only works on Unixes")
	}
	tmp, err := ioutil.TempFile("", "")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	if out, err := exec.Command("./gen-licenses.sh", tmp.Name()).CombinedOutput(); err != nil {
		t.Fatalf("generating licenses failed: %q, %v", out, err)
	}

	got, err := ioutil.ReadFile(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	want, err := ioutil.ReadFile("licenses.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("combined license text needs updating. Run: `go generate ./internal/hooks` from the gopls module.")
	}
}
