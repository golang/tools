// The gorename command performs precise type-safe renaming of
// identifiers in Go source code.  See the -help message or Usage
// constant for details.
package main

import (
	"flag"
	"fmt"
	"go/build"
	"os"
	"runtime"

	"code.google.com/p/go.tools/refactor/rename"
)

var (
	offsetFlag = flag.String("offset", "", "file and byte offset of identifier to be renamed, e.g. 'file.go:#123'.  For use by editors.")
	fromFlag   = flag.String("from", "", "identifier to be renamed; see -help for formats")
	toFlag     = flag.String("to", "", "new name for identifier")
	helpFlag   = flag.Bool("help", false, "show usage message")
)

func init() {
	flag.BoolVar(&rename.Force, "force", false, "proceed, even if conflicts were reported")
	flag.BoolVar(&rename.DryRun, "dryrun", false, "show the change, but do not apply it")
	flag.BoolVar(&rename.Verbose, "v", false, "print verbose information")

	// If $GOMAXPROCS isn't set, use the full capacity of the machine.
	// For small machines, use at least 4 threads.
	if os.Getenv("GOMAXPROCS") == "" {
		n := runtime.NumCPU()
		if n < 4 {
			n = 4
		}
		runtime.GOMAXPROCS(n)
	}
}

const Usage = `gorename: precise type-safe renaming of identifiers in Go source code.

Usage:

 gorename (-from <spec> | -offset <file>:#<byte-offset>) -to <name> [-force]

You must specify the object (named entity) to rename using the -offset
or -from flag.  Exactly one must be specified.

Flags:

-offset    specifies the filename and byte offset of an identifier to rename.
           This form is intended for use by text editors.

-from      specifies the object to rename using a query notation;
           This form is intended for interactive use at the command line.
` + rename.FromFlagUsage + `

-to        the new name.

-force     causes the renaming to proceed even if conflicts were reported.
           The resulting program may be ill-formed, or experience a change
           in behaviour.

           WARNING: this flag may even cause the renaming tool to crash.
           (In due course this bug will be fixed by moving certain
           analyses into the type-checker.)

-v         enables verbose logging.

gorename automatically computes the set of packages that might be
affected.  For a local renaming, this is just the package specified by
-from or -offset, but for a potentially exported name, gorename scans
the workspace ($GOROOT and $GOPATH).

gorename rejects any renaming that would create a conflict at the point
of declaration, or a reference conflict (ambiguity or shadowing), or
anything else that could cause the resulting program not to compile.
Currently, it also rejects any method renaming that would change the
assignability relation between types and interfaces.


Examples:

% gorename -offset file.go:#123 -to foo

  Rename the object whose identifer is at byte offset 123 within file file.go.

% gorename -from '(bytes.Buffer).Len' -to Size

  Rename the "Len" method of the *bytes.Buffer type to "Size".

---- TODO ----

Correctness:
- implement remaining safety checks.
- handle dot imports correctly
- document limitations (reflection, 'implements' guesswork).
- sketch a proof of exhaustiveness.

Features:
- support running on programs containing errors (loader.Config.AllowErrors)
- allow users to specify a scope other than "global" (to avoid being
  stuck by neglected packages in $GOPATH that don't build).
- support renaming the package clause (no object)
- support renaming an import path (no ident or object)
  (requires filesystem + SCM updates).
- detect and reject edits to autogenerated files (cgo, protobufs)
  and optionally $GOROOT packages.
- report all conflicts, or at least all qualitatively distinct ones.
  Sometimes we stop to avoid redundancy, but
  it may give a disproportionate sense of safety in -force mode.
- support renaming all instances of a pattern, e.g.
  all receiver vars of a given type,
  all local variables of a given type,
  all PkgNames for a given package.
- emit JSON output for other editors and tools.
- integration support for editors other than Emacs.
`

func main() {
	flag.Parse()
	if len(flag.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "Error: surplus arguments.\n")
		os.Exit(1)
	}

	if *helpFlag || (*offsetFlag == "" && *fromFlag == "" && *toFlag == "") {
		fmt.Println(Usage)
		return
	}

	if err := rename.Main(&build.Default, *offsetFlag, *fromFlag, *toFlag); err != nil {
		if err != rename.ConflictError {
			fmt.Fprintf(os.Stderr, "Error: %s.\n", err)
		}
		os.Exit(1)
	}
}
