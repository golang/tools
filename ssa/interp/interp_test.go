// +build !windows,!plan9

package interp_test

import (
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.google.com/p/go.tools/importer"
	"code.google.com/p/go.tools/ssa"
	"code.google.com/p/go.tools/ssa/interp"
)

// Each line contains a space-separated list of $GOROOT/test/
// filenames comprising the main package of a program.
// They are ordered quickest-first, roughly.
//
// TODO(adonovan): integrate into the $GOROOT/test driver scripts,
// golden file checking, etc.
var gorootTests = []string{
	"235.go",
	"alias1.go",
	"chancap.go",
	"func5.go",
	"func6.go",
	"func7.go",
	"func8.go",
	"helloworld.go",
	"varinit.go",
	"escape3.go",
	"initcomma.go",
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
	"closure.go",
	"gc.go",
	"simassign.go",
	"iota.go",
	"goprint.go", // doesn't actually assert anything
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
	"literal.go",
	"nul1.go",
	"zerodivide.go",
	"convert.go",
	"convT2X.go",
	"initialize.go",
	"ddd.go",
	"blank.go", // partly disabled; TODO(adonovan): skip blank fields in struct{_} equivalence.
	"map.go",
	"bom.go",
	"closedchan.go",
	"divide.go",
	"rename.go",
	"const3.go",
	"nil.go",
	"recover.go", // partly disabled; TODO(adonovan): fix.
	// Slow tests follow.
	"cmplxdivide.go cmplxdivide1.go",
	"append.go",
	"crlf.go", // doesn't actually assert anything
	"typeswitch1.go",
	"floatcmp.go",
	"gc1.go",

	// Working, but not worth enabling:
	// "gc2.go",       // works, but slow, and cheats on the memory check.
	// "sigchld.go",   // works, but only on POSIX.
	// "peano.go",     // works only up to n=9, and slow even then.
	// "stack.go",     // works, but too slow (~30s) by default.
	// "solitaire.go", // works, but too slow (~30s).
	// "const.go",     // works but for but one bug: constant folder doesn't consider representations.
	// "init1.go",     // too slow (80s) and not that interesting. Cheats on ReadMemStats check too.

	// Typechecker failures:
	// "switch.go",            // bug re: switch ... { case 1.0:... case 1:... }
	// "rune.go",              // error re: rune as index
	// "64bit.go",             // error re: comparison
	// "cmp.go",               // error re: comparison
	// "rotate.go rotate0.go", // error re: shifts
	// "rotate.go rotate1.go", // error re: shifts
	// "rotate.go rotate2.go", // error re: shifts
	// "rotate.go rotate3.go", // error re: shifts
	// "run.go",               // produces wrong constant for bufio.runeError; also, not really a test.

	// Broken.  TODO(adonovan): fix.
	// copy.go         // very slow; but with N=4 quickly crashes, slice index out of range.
	// nilptr.go       // interp: V > uintptr not implemented. Slow test, lots of mem
	// recover1.go     // error: "spurious recover"
	// recover2.go     // panic: interface conversion: string is not error: missing method Error
	// recover3.go     // logic errors: panicked with wrong Error.
	// method3.go      // Fails dynamically; (*T).f vs (T).f are distinct methods.
	// args.go         // works, but requires specific os.Args from the driver.
	// index.go        // a template, not a real test.
	// mallocfin.go    // SetFinalizer not implemented.

	// TODO(adonovan): add tests from $GOROOT/test/* subtrees:
	// bench chan bugs fixedbugs interface ken.
}

// These are files in go.tools/ssa/interp/testdata/.
var testdataTests = []string{
	"coverage.go",
	"mrvchain.go",
	"boundmeth.go",
	"ifaceprom.go",
}

func run(t *testing.T, dir, input string) bool {
	fmt.Printf("Input: %s\n", input)

	var inputs []string
	for _, i := range strings.Split(input, " ") {
		inputs = append(inputs, dir+i)
	}

	impctx := &importer.Context{
		Loader: importer.MakeGoBuildLoader(nil),
	}
	imp := importer.New(impctx)
	files, err := importer.ParseFiles(imp.Fset, ".", inputs...)
	if err != nil {
		t.Errorf("ssa.ParseFiles(%s) failed: %s", inputs, err.Error())
		return false
	}

	// Print a helpful hint if we don't make it to the end.
	var hint string
	defer func() {
		if hint != "" {
			fmt.Println("FAIL")
			fmt.Println(hint)
		} else {
			fmt.Println("PASS")
		}
	}()

	hint = fmt.Sprintf("To dump SSA representation, run:\n%% go run exp/ssa/ssadump.go -build=CFP %s\n", input)
	info, err := imp.CreateSourcePackage("main", files)
	if err != nil {
		t.Errorf("ssa.Builder.CreatePackage(%s) failed: %s", inputs, err.Error())
		return false
	}

	prog := ssa.NewProgram(imp.Fset, ssa.SanityCheckFunctions)
	prog.CreatePackages(imp)
	prog.BuildAll()

	hint = fmt.Sprintf("To trace execution, run:\n%% go run exp/ssa/ssadump.go -build=C -run --interp=T %s\n", input)
	if exitCode := interp.Interpret(prog.Package(info.Pkg), 0, inputs[0], []string{}); exitCode != 0 {
		t.Errorf("interp.Interpret(%s) exited with code %d, want zero", inputs, exitCode)
		return false
	}

	hint = "" // call off the hounds
	return true
}

const slash = string(os.PathSeparator)

// TestInterp runs the interpreter on a selection of small Go programs.
func TestInterp(t *testing.T) {
	var failures []string

	for _, input := range testdataTests {
		if !run(t, "testdata"+slash, input) {
			failures = append(failures, input)
		}
	}

	if !testing.Short() {
		for _, input := range gorootTests {
			if !run(t, filepath.Join(build.Default.GOROOT, "test")+slash, input) {
				failures = append(failures, input)
			}
		}
	}

	if failures != nil {
		fmt.Println("The following tests failed:")
		for _, f := range failures {
			fmt.Printf("\t%s\n", f)
		}
	}
}
