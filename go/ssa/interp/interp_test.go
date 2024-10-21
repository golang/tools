// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package interp_test

// This test runs the SSA interpreter over sample Go programs.
// Because the interpreter requires intrinsics for assembly
// functions and many low-level runtime routines, it is inherently
// not robust to evolutionary change in the standard library.
// Therefore the test cases are restricted to programs that
// use a fake standard library in testdata/src containing a tiny
// subset of simple functions useful for writing assertions.
//
// We no longer attempt to interpret any real standard packages such as
// fmt or testing, as it proved too fragile.

import (
	"bytes"
	"fmt"
	"go/build"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/interp"
	"golang.org/x/tools/go/ssa/ssautil"
	"golang.org/x/tools/internal/testenv"
)

// Each line contains a space-separated list of $GOROOT/test/
// filenames comprising the main package of a program.
// They are ordered quickest-first, roughly.
//
// If a test in this list fails spuriously, remove it.
var gorootTestTests = []string{
	"235.go",
	"alias1.go",
	"func5.go",
	"func6.go",
	"func7.go",
	"func8.go",
	"helloworld.go",
	"varinit.go",
	"escape3.go",
	"initcomma.go",
	"cmp.go",
	"compos.go",
	"turing.go",
	"indirect.go",
	"complit.go",
	"for.go",
	"struct0.go",
	"intcvt.go",
	"printbig.go",
	"deferprint.go",
	"escape.go",
	"range.go",
	"const4.go",
	"float_lit.go",
	"bigalg.go",
	"decl.go",
	"if.go",
	"named.go",
	"bigmap.go",
	"func.go",
	"reorder2.go",
	"gc.go",
	"simassign.go",
	"iota.go",
	"nilptr2.go",
	"utf.go",
	"method.go",
	"char_lit.go",
	"env.go",
	"int_lit.go",
	"string_lit.go",
	"defer.go",
	"typeswitch.go",
	"stringrange.go",
	"reorder.go",
	"method3.go",
	"literal.go",
	"nul1.go", // doesn't actually assert anything (errorcheckoutput)
	"zerodivide.go",
	"convert.go",
	"convT2X.go",
	"switch.go",
	"ddd.go",
	"blank.go", // partly disabled
	"closedchan.go",
	"divide.go",
	"rename.go",
	"nil.go",
	"recover1.go",
	"recover2.go",
	"recover3.go",
	"typeswitch1.go",
	"floatcmp.go",
	"crlf.go", // doesn't actually assert anything (runoutput)
}

// These are files in go.tools/go/ssa/interp/testdata/.
var testdataTests = []string{
	"boundmeth.go",
	"complit.go",
	"convert.go",
	"coverage.go",
	"deepequal.go",
	"defer.go",
	"fieldprom.go",
	"forvarlifetime_old.go",
	"ifaceconv.go",
	"ifaceprom.go",
	"initorder.go",
	"methprom.go",
	"mrvchain.go",
	"range.go",
	"rangeoverint.go",
	"recover.go",
	"reflect.go",
	"slice2arrayptr.go",
	"static.go",
	"width32.go",
	"rangevarlifetime_old.go",
	"fixedbugs/issue52342.go",
	"fixedbugs/issue55115.go",
	"fixedbugs/issue52835.go",
	"fixedbugs/issue55086.go",
	"fixedbugs/issue66783.go",
	"fixedbugs/issue69929.go",
	"typeassert.go",
	"zeros.go",
	"slice2array.go",
	"minmax.go",
	"rangevarlifetime_go122.go",
	"forvarlifetime_go122.go",
}

func init() {
	// GOROOT/test used to assume that GOOS and GOARCH were explicitly set in the
	// environment, so do that here for TestGorootTest.
	os.Setenv("GOOS", runtime.GOOS)
	os.Setenv("GOARCH", runtime.GOARCH)
}

// run runs a single test. On success it returns the captured std{out,err}.
func run(t *testing.T, input string, goroot string) string {
	testenv.NeedsExec(t) // really we just need os.Pipe, but os/exec uses pipes

	t.Logf("Input: %s\n", input)

	start := time.Now()

	ctx := build.Default // copy
	ctx.GOROOT = goroot
	ctx.GOOS = runtime.GOOS
	ctx.GOARCH = runtime.GOARCH
	if filepath.Base(input) == "width32.go" && unsafe.Sizeof(int(0)) > 4 {
		t.Skipf("skipping: width32.go checks behavior for a 32-bit int")
	}

	gover := ""
	if p := testenv.Go1Point(); p > 0 {
		gover = fmt.Sprintf("go1.%d", p)
	}

	conf := loader.Config{Build: &ctx, TypeChecker: types.Config{GoVersion: gover}}
	if _, err := conf.FromArgs([]string{input}, true); err != nil {
		t.Fatalf("FromArgs(%s) failed: %s", input, err)
	}

	conf.Import("runtime")

	// Print a helpful hint if we don't make it to the end.
	var hint string
	defer func() {
		t.Logf("Duration: %v", time.Since(start))
		if hint != "" {
			t.Log("FAIL")
			t.Log(hint)
		} else {
			t.Log("PASS")
		}
	}()

	hint = fmt.Sprintf("To dump SSA representation, run:\n%% go build golang.org/x/tools/cmd/ssadump && ./ssadump -test -build=CFP %s\n", input)

	iprog, err := conf.Load()
	if err != nil {
		t.Fatalf("conf.Load(%s) failed: %s", input, err)
	}

	bmode := ssa.InstantiateGenerics | ssa.SanityCheckFunctions
	// bmode |= ssa.PrintFunctions // enable for debugging
	prog := ssautil.CreateProgram(iprog, bmode)
	prog.Build()

	mainPkg := prog.Package(iprog.Created[0].Pkg)
	if mainPkg == nil {
		t.Fatalf("not a main package: %s", input)
	}

	sizes := types.SizesFor("gc", ctx.GOARCH)
	if sizes.Sizeof(types.Typ[types.Int]) < 4 {
		panic("bogus SizesFor")
	}
	hint = fmt.Sprintf("To trace execution, run:\n%% go build golang.org/x/tools/cmd/ssadump && ./ssadump -build=C -test -run --interp=T %s\n", input)

	// Capture anything written by the interpreter to os.Std{out,err}
	// by temporarily redirecting them to a buffer via a pipe.
	//
	// While capturing is in effect, we must not write any
	// test-related stuff to stderr (including log.Print, t.Log, etc).
	var restore func() string // restore files and log+return the mixed out/err.
	{
		// Connect std{out,err} to pipe.
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("can't create pipe for stderr: %v", err)
		}
		savedStdout := os.Stdout
		savedStderr := os.Stderr
		os.Stdout = w
		os.Stderr = w

		// Buffer what is written.
		var buf strings.Builder
		done := make(chan struct{})
		go func() {
			if _, err := io.Copy(&buf, r); err != nil {
				fmt.Fprintf(savedStderr, "io.Copy: %v", err)
			}
			close(done)
		}()

		// Finally, restore the files and log what was captured.
		restore = func() string {
			os.Stdout = savedStdout
			os.Stderr = savedStderr
			w.Close()
			<-done
			captured := buf.String()
			t.Logf("Interpreter's stdout+stderr:\n%s", captured)
			return captured
		}
	}

	var imode interp.Mode // default mode
	// imode |= interp.DisableRecover // enable for debugging
	// imode |= interp.EnableTracing // enable for debugging
	exitCode := interp.Interpret(mainPkg, imode, sizes, input, []string{})
	capturedOutput := restore()
	if exitCode != 0 {
		t.Fatalf("interpreting %s: exit code was %d", input, exitCode)
	}
	// $GOROOT/test tests use this convention:
	if strings.Contains(capturedOutput, "BUG") {
		t.Fatalf("interpreting %s: exited zero but output contained 'BUG'", input)
	}

	hint = "" // call off the hounds

	return capturedOutput
}

// makeGoroot copies testdata/src into the "src" directory of a temporary
// location to mimic GOROOT/src, and adds a file "runtime/consts.go" containing
// declarations for GOOS and GOARCH that match the GOOS and GOARCH of this test.
//
// It returns the directory that should be used for GOROOT.
func makeGoroot(t *testing.T) string {
	goroot := t.TempDir()
	src := filepath.Join(goroot, "src")

	err := filepath.Walk("testdata/src", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel("testdata/src", path)
		if err != nil {
			return err
		}
		targ := filepath.Join(src, rel)

		if info.IsDir() {
			return os.Mkdir(targ, info.Mode().Perm()|0700)
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(targ, b, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}

	constsGo := fmt.Sprintf(`package runtime
const GOOS = %q
const GOARCH = %q
`, runtime.GOOS, runtime.GOARCH)
	err = os.WriteFile(filepath.Join(src, "runtime/consts.go"), []byte(constsGo), 0644)
	if err != nil {
		t.Fatal(err)
	}

	return goroot
}

// TestTestdataFiles runs the interpreter on testdata/*.go.
func TestTestdataFiles(t *testing.T) {
	goroot := makeGoroot(t)
	for _, input := range testdataTests {
		t.Run(input, func(t *testing.T) {
			run(t, filepath.Join("testdata", input), goroot)
		})
	}
}

// TestGorootTest runs the interpreter on $GOROOT/test/*.go.
func TestGorootTest(t *testing.T) {
	testenv.NeedsGOROOTDir(t, "test")

	goroot := makeGoroot(t)
	for _, input := range gorootTestTests {
		t.Run(input, func(t *testing.T) {
			run(t, filepath.Join(build.Default.GOROOT, "test", input), goroot)
		})
	}
}

// TestTypeparamTest runs the interpreter on runnable examples
// in $GOROOT/test/typeparam/*.go.

func TestTypeparamTest(t *testing.T) {
	testenv.NeedsGOROOTDir(t, "test")

	if runtime.GOARCH == "wasm" {
		// See ssa/TestTypeparamTest.
		t.Skip("Consistent flakes on wasm (e.g. https://go.dev/issues/64726)")
	}

	goroot := makeGoroot(t)

	// Skip known failures for the given reason.
	// TODO(taking): Address these.
	skip := map[string]string{
		"chans.go":      "interp tests do not support runtime.SetFinalizer",
		"issue23536.go": "unknown reason",
		"issue48042.go": "interp tests do not handle reflect.Value.SetInt",
		"issue47716.go": "interp tests do not handle unsafe.Sizeof",
		"issue50419.go": "interp tests do not handle dispatch to String() correctly",
		"issue51733.go": "interp does not handle unsafe casts",
		"ordered.go":    "math.NaN() comparisons not being handled correctly",
		"orderedmap.go": "interp tests do not support runtime.SetFinalizer",
		"stringer.go":   "unknown reason",
		"issue48317.go": "interp tests do not support encoding/json",
		"issue48318.go": "interp tests do not support encoding/json",
		"issue58513.go": "interp tests do not support runtime.Caller",
	}
	// Collect all of the .go files in dir that are runnable.
	dir := filepath.Join(build.Default.GOROOT, "test", "typeparam")
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range list {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue // Consider standalone go files.
		}
		t.Run(entry.Name(), func(t *testing.T) {
			input := filepath.Join(dir, entry.Name())
			src, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}

			// Only build test files that can be compiled, or compiled and run.
			if !bytes.HasPrefix(src, []byte("// run")) || bytes.HasPrefix(src, []byte("// rundir")) {
				t.Logf("Not a `// run` file: %s", entry.Name())
				return
			}

			if reason := skip[entry.Name()]; reason != "" {
				t.Skipf("skipping: %s", reason)
			}

			run(t, input, goroot)
		})
	}
}
