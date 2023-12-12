// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package goversion_test

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/gopls/internal/util/goversion"
)

func TestMessage(t *testing.T) {
	// Note(rfindley): this test is a change detector, as it must be updated
	// whenever we deprecate a version.
	//
	// However, I chose to leave it as is since it gives us confidence in error
	// messages served for Go versions that we no longer support (and therefore
	// no longer run in CI).
	type test struct {
		goVersion    int
		fromBuild    bool
		wantContains []string // string fragments that we expect to see
		wantIsError  bool     // an error, not a mere warning
	}

	deprecated := func(goVersion int, lastVersion string) test {
		return test{
			goVersion: goVersion,
			fromBuild: false,
			wantContains: []string{
				fmt.Sprintf("Found Go version 1.%d", goVersion),
				"not supported",
				fmt.Sprintf("upgrade to Go 1.%d", goversion.OldestSupported()),
				fmt.Sprintf("install gopls %s", lastVersion),
			},
			wantIsError: true,
		}
	}

	tests := []struct {
		goVersion    int
		fromBuild    bool
		wantContains []string // string fragments that we expect to see
		wantIsError  bool     // an error, not a mere warning
	}{
		{-1, false, nil, false},
		deprecated(12, "v0.7.5"),
		deprecated(13, "v0.9.5"),
		deprecated(15, "v0.9.5"),
		deprecated(16, "v0.11.0"),
		deprecated(17, "v0.11.0"),
		{18, false, []string{"Found Go version 1.18", "unsupported by gopls v0.16.0", "upgrade to Go 1.19", "install gopls v0.14.2"}, false},
		{18, true, []string{"Gopls was built with Go version 1.18", "unsupported by gopls v0.16.0", "upgrade to Go 1.19", "install gopls v0.14.2"}, false},
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
