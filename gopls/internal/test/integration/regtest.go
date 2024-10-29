// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package integration

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cmd"
	"golang.org/x/tools/internal/drivertest"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/memoize"
	"golang.org/x/tools/internal/testenv"
	"golang.org/x/tools/internal/tool"
)

var (
	runSubprocessTests       = flag.Bool("enable_gopls_subprocess_tests", false, "run integration tests against a gopls subprocess (default: in-process)")
	goplsBinaryPath          = flag.String("gopls_test_binary", "", "path to the gopls binary for use as a remote, for use with the -enable_gopls_subprocess_tests flag")
	timeout                  = flag.Duration("timeout", defaultTimeout(), "if nonzero, default timeout for each integration test; defaults to GOPLS_INTEGRATION_TEST_TIMEOUT")
	skipCleanup              = flag.Bool("skip_cleanup", false, "whether to skip cleaning up temp directories")
	printGoroutinesOnFailure = flag.Bool("print_goroutines", false, "whether to print goroutines info on failure")
	printLogs                = flag.Bool("print_logs", false, "whether to print LSP logs")
)

func defaultTimeout() time.Duration {
	s := os.Getenv("GOPLS_INTEGRATION_TEST_TIMEOUT")
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid GOPLS_INTEGRATION_TEST_TIMEOUT %q: %v\n", s, err)
		os.Exit(2)
	}
	return d
}

var runner *Runner

func Run(t *testing.T, files string, f TestFunc) {
	runner.Run(t, files, f)
}

func WithOptions(opts ...RunOption) configuredRunner {
	return configuredRunner{opts: opts}
}

type configuredRunner struct {
	opts []RunOption
}

func (r configuredRunner) Run(t *testing.T, files string, f TestFunc) {
	// Print a warning if the test's temporary directory is not
	// suitable as a workspace folder, as this may lead to
	// otherwise-cryptic failures. This situation typically occurs
	// when an arbitrary string (e.g. "foo.") is used as a subtest
	// name, on a platform with filename restrictions (e.g. no
	// trailing period on Windows).
	tmp := t.TempDir()
	if err := cache.CheckPathValid(tmp); err != nil {
		t.Logf("Warning: testing.T.TempDir(%s) is not valid as a workspace folder: %s",
			tmp, err)
	}

	runner.Run(t, files, f, r.opts...)
}

// RunMultiple runs a test multiple times, with different options.
// The runner should be constructed with [WithOptions].
//
// TODO(rfindley): replace Modes with selective use of RunMultiple.
type RunMultiple []struct {
	Name   string
	Runner interface {
		Run(t *testing.T, files string, f TestFunc)
	}
}

func (r RunMultiple) Run(t *testing.T, files string, f TestFunc) {
	for _, runner := range r {
		t.Run(runner.Name, func(t *testing.T) {
			runner.Runner.Run(t, files, f)
		})
	}
}

// DefaultModes returns the default modes to run for each regression test (they
// may be reconfigured by the tests themselves).
func DefaultModes() Mode {
	modes := Default
	if !testing.Short() {
		// TODO(rfindley): we should just run a few select integration tests in
		// "Forwarded" mode, and call it a day. No need to run every single test in
		// two ways.
		modes |= Forwarded
	}
	if *runSubprocessTests {
		modes |= SeparateProcess
	}
	return modes
}

var runFromMain = false // true if Main has been called

// Main sets up and tears down the shared integration test state.
func Main(m *testing.M) (code int) {
	// Provide an entrypoint for tests that use a fake go/packages driver.
	drivertest.RunIfChild()

	defer func() {
		if runner != nil {
			if err := runner.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "closing test runner: %v\n", err)
				// Cleanup is broken in go1.12 and earlier, and sometimes flakes on
				// Windows due to file locking, but this is OK for our CI.
				//
				// Fail on go1.13+, except for windows and android which have shutdown problems.
				if testenv.Go1Point() >= 13 && runtime.GOOS != "windows" && runtime.GOOS != "android" {
					if code == 0 {
						code = 1
					}
				}
			}
		}
	}()

	runFromMain = true

	// golang/go#54461: enable additional debugging around hanging Go commands.
	gocommand.DebugHangingGoCommands = true

	// If this magic environment variable is set, run gopls instead of the test
	// suite. See the documentation for runTestAsGoplsEnvvar for more details.
	if os.Getenv(runTestAsGoplsEnvvar) == "true" {
		tool.Main(context.Background(), cmd.New(), os.Args[1:])
		return 0
	}

	if !testenv.HasExec() {
		fmt.Printf("skipping all tests: exec not supported on %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return 0
	}
	testenv.ExitIfSmallMachine()

	flag.Parse()

	// Disable GOPACKAGESDRIVER, as it can cause spurious test failures.
	os.Setenv("GOPACKAGESDRIVER", "off")

	if skipReason := checkBuilder(); skipReason != "" {
		fmt.Printf("Skipping all tests: %s\n", skipReason)
		return 0
	}

	if err := testenv.HasTool("go"); err != nil {
		fmt.Println("Missing go command")
		return 1
	}

	runner = &Runner{
		DefaultModes:             DefaultModes(),
		Timeout:                  *timeout,
		PrintGoroutinesOnFailure: *printGoroutinesOnFailure,
		SkipCleanup:              *skipCleanup,
		store:                    memoize.NewStore(memoize.NeverEvict),
	}

	runner.goplsPath = *goplsBinaryPath
	if runner.goplsPath == "" {
		var err error
		runner.goplsPath, err = os.Executable()
		if err != nil {
			panic(fmt.Sprintf("finding test binary path: %v", err))
		}
	}

	dir, err := os.MkdirTemp("", "gopls-test-")
	if err != nil {
		panic(fmt.Errorf("creating temp directory: %v", err))
	}
	runner.tempDir = dir

	FilterToolchainPathAndGOROOT()

	return m.Run()
}

// FilterToolchainPathAndGOROOT updates the PATH and GOROOT environment
// variables for the current process to effectively revert the changes made by
// the go command when performing a toolchain switch in the context of `go
// test` (see golang/go#68005).
//
// It does this by looking through PATH for a go command that is NOT a
// toolchain go command, and adjusting PATH to find that go command. Then it
// unsets GOROOT in order to use the default GOROOT for that go command.
//
// TODO(rfindley): this is very much a hack, so that our 1.21 and 1.22 builders
// actually exercise integration with older go commands. In golang/go#69321, we
// hope to do better.
func FilterToolchainPathAndGOROOT() {
	if localGo, first := findLocalGo(); localGo != "" && !first {
		dir := filepath.Dir(localGo)
		path := os.Getenv("PATH")
		os.Setenv("PATH", dir+string(os.PathListSeparator)+path)
		os.Unsetenv("GOROOT") // Remove the GOROOT value that was added by toolchain switch.
	}
}

// findLocalGo returns a path to a local (=non-toolchain) Go version, or the
// empty string if none is found.
//
// The second result reports if path matches the result of exec.LookPath.
func findLocalGo() (path string, first bool) {
	paths := filepath.SplitList(os.Getenv("PATH"))
	for _, path := range paths {
		// Use a simple heuristic to filter out toolchain paths.
		if strings.Contains(path, "toolchain@v0.0.1-go") && filepath.Base(path) == "bin" {
			continue // toolchain path
		}
		fullPath := filepath.Join(path, "go")
		fi, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		if fi.Mode()&0111 != 0 {
			first := false
			pathGo, err := exec.LookPath("go")
			if err == nil {
				first = fullPath == pathGo
			}
			return fullPath, first
		}
	}
	return "", false
}
