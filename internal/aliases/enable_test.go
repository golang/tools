// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aliases_test

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/internal/aliases"
	"golang.org/x/tools/internal/testenv"
)

func init() {
	if os.Getenv("ConditionallyEnableGoTypesAlias_CHILD") == "1" {
		go aliases.ConditionallyEnableGoTypesAlias() // Throw in an extra call. Should be fine.
		aliases.ConditionallyEnableGoTypesAlias()
	}
}

func TestConditionallyEnableGoTypesAlias(t *testing.T) {
	if !(runtime.GOOS == "linux" || runtime.GOOS == "darwin") {
		t.Skipf("skipping fork/exec test on this platform")
	}

	if os.Getenv("ConditionallyEnableGoTypesAlias_CHILD") == "1" {
		// child process
		enabled := aliases.Enabled()
		fmt.Printf("gotypesalias is enabled %v", enabled)
		return
	}

	testenv.NeedsTool(t, "go")

	var wants map[string]string
	const (
		enabled  = "gotypesalias is enabled true"
		disabled = "gotypesalias is enabled false"
	)
	goversion := testenv.Go1Point()
	if goversion <= 22 {
		wants = map[string]string{
			"":  disabled,
			"0": disabled,
			"1": enabled,
		}
	} else {
		wants = map[string]string{
			"":  enabled,
			"0": disabled,
			"1": enabled,
		}
	}

	for _, test := range []string{"", "0", "1"} {
		cmd := exec.Command(os.Args[0], "-test.run=TestConditionallyEnableGoTypesAlias")
		cmd.Env = append(os.Environ(), "ConditionallyEnableGoTypesAlias_CHILD=1")
		if test != "" {
			cmd.Env = append(cmd.Env, fmt.Sprintf("GODEBUG=gotypesalias=%s", test))
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Error(err)
		}
		want := wants[test]
		if !strings.Contains(string(out), want) {
			t.Errorf("gotypesalias=%q: want %s", test, want)
			t.Logf("(go 1.%d) %q: out=<<%s>>", goversion, test, out)
		}
	}
}
