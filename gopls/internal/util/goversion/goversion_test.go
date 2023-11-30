// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goversion_test

import (
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/util/goversion"
)

func TestMessage(t *testing.T) {
	tests := []struct {
		goVersion    int
		fromBuild    bool
		wantContains []string // string fragments that we expect to see
		wantIsError  bool     // an error, not a mere warning
	}{
		{-1, false, nil, false},
		{12, false, []string{"1.12", "not supported", "upgrade to Go 1.18", "install gopls v0.7.5"}, true},
		{13, false, []string{"1.13", "not supported", "upgrade to Go 1.18", "install gopls v0.9.5"}, true},
		{15, false, []string{"1.15", "not supported", "upgrade to Go 1.18", "install gopls v0.9.5"}, true},
		{15, true, []string{"Gopls was built with Go version 1.15", "not supported", "upgrade to Go 1.18", "install gopls v0.9.5"}, true},
		{16, false, []string{"1.16", "will be unsupported by gopls v0.13.0", "upgrade to Go 1.18", "install gopls v0.11.0"}, false},
		{17, false, []string{"1.17", "will be unsupported by gopls v0.13.0", "upgrade to Go 1.18", "install gopls v0.11.0"}, false},
		{17, true, []string{"Gopls was built with Go version 1.17", "will be unsupported by gopls v0.13.0", "upgrade to Go 1.18", "install gopls v0.11.0"}, false},
	}

	for _, test := range tests {
		gotMsg, gotIsError := goversion.Message(test.goVersion, test.fromBuild)

		if len(test.wantContains) == 0 && gotMsg != "" {
			t.Errorf("versionMessage(%d) = %q, want \"\"", test.goVersion, gotMsg)
		}

		for _, want := range test.wantContains {
			if !strings.Contains(gotMsg, want) {
				t.Errorf("versionMessage(%d) = %q, want containing %q", test.goVersion, gotMsg, want)
			}
		}

		if gotIsError != test.wantIsError {
			t.Errorf("versionMessage(%d) isError = %v, want %v", test.goVersion, gotIsError, test.wantIsError)
		}
	}
}
