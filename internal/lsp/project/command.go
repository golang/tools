package project

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// invokeGo returns the stdout of a go command invocation.
func invokeGo(ctx context.Context, dir string, args ...string) (*bytes.Buffer, error) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			// Catastrophic error:
			// - executable not found
			// - context cancellation
			return nil, fmt.Errorf("couldn't exec 'go %v': %s %T", args, err, err)
		}

		// Old go version?
		if strings.Contains(stderr.String(), "flag provided but not defined") {
			return nil, fmt.Errorf("unsupported version of go: %s: %s", exitErr, stderr)
		}
	}

	//if len(stderr.Bytes()) != 0 {
	//	return nil, fmt.Errorf("'go %v' failed: %s", args, string(stderr.Bytes()))
	//}

	return stdout, nil
}
