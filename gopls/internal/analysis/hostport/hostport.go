// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package hostport defines an analyzer for calls to net.Dial with
// addresses of the form "%s:%d" or "%s:%s", which work only with IPv4.
package hostport

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/astutil/cursor"
)

const Doc = `check format of addresses passed to net.Dial

This analyzer flags code that produce network address strings using
fmt.Sprintf, as in this example:

    addr := fmt.Sprintf("%s:%d", host, 12345) // "will not work with IPv6"
    ...
    conn, err := net.Dial("tcp", addr)       // "when passed to dial here"

The analyzer suggests a fix to use the correct approach, a call to
net.JoinHostPort:

    addr := net.JoinHostPort(host, "12345")
    ...
    conn, err := net.Dial("tcp", addr)

A similar diagnostic and fix are produced for a format string of "%s:%s".
`

var Analyzer = &analysis.Analyzer{
	Name:     "hostport",
	Doc:      Doc,
	URL:      "https://pkg.go.dev/golang.org/x/tools/gopls/internal/analysis/hostport",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	// Fast path: if the package doesn't import net and fmt, skip
	// the traversal.
	if !analysisinternal.Imports(pass.Pkg, "net") ||
		!analysisinternal.Imports(pass.Pkg, "fmt") {
		return nil, nil
	}

	info := pass.TypesInfo

	// checkAddr reports a diagnostic (and returns true) if e
	// is a call of the form fmt.Sprintf("%d:%d", ...).
	// The diagnostic includes a fix.
	//
	// dialCall is non-nil if the Dial call is non-local
	// but within the same file.
	checkAddr := func(e ast.Expr, dialCall *ast.CallExpr) {
		if call, ok := e.(*ast.CallExpr); ok {
			obj := typeutil.Callee(info, call)
			if analysisinternal.IsFunctionNamed(obj, "fmt", "Sprintf") {
				// Examine format string.
				formatArg := call.Args[0]
				if tv := info.Types[formatArg]; tv.Value != nil {
					numericPort := false
					format := constant.StringVal(tv.Value)
					switch format {
					case "%s:%d":
						// Have: fmt.Sprintf("%s:%d", host, port)
						numericPort = true

					case "%s:%s":
						// Have: fmt.Sprintf("%s:%s", host, portStr)
						// Keep port string as is.

					default:
						return
					}

					// Use granular edits to preserve original formatting.
					edits := []analysis.TextEdit{
						{
							// Replace fmt.Sprintf with net.JoinHostPort.
							Pos:     call.Fun.Pos(),
							End:     call.Fun.End(),
							NewText: []byte("net.JoinHostPort"),
						},
						{
							// Delete format string.
							Pos: formatArg.Pos(),
							End: call.Args[1].Pos(),
						},
					}

					// Turn numeric port into a string.
					if numericPort {
						//  port => fmt.Sprintf("%d", port)
						//   123 => "123"
						port := call.Args[2]
						newPort := fmt.Sprintf(`fmt.Sprintf("%%d", %s)`, port)
						if port := info.Types[port].Value; port != nil {
							if i, ok := constant.Int64Val(port); ok {
								newPort = fmt.Sprintf(`"%d"`, i) // numeric constant
							}
						}

						edits = append(edits, analysis.TextEdit{
							Pos:     port.Pos(),
							End:     port.End(),
							NewText: []byte(newPort),
						})
					}

					// Refer to Dial call, if not adjacent.
					suffix := ""
					if dialCall != nil {
						suffix = fmt.Sprintf(" (passed to net.Dial at L%d)",
							safetoken.StartPosition(pass.Fset, dialCall.Pos()).Line)
					}

					pass.Report(analysis.Diagnostic{
						// Highlight the format string.
						Pos:     formatArg.Pos(),
						End:     formatArg.End(),
						Message: fmt.Sprintf("address format %q does not work with IPv6%s", format, suffix),
						SuggestedFixes: []analysis.SuggestedFix{{
							Message:   "Replace fmt.Sprintf with net.JoinHostPort",
							TextEdits: edits,
						}},
					})
				}
			}
		}
	}

	// Check address argument of each call to net.Dial et al.
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	for curCall := range cursor.Root(inspect).Preorder((*ast.CallExpr)(nil)) {
		call := curCall.Node().(*ast.CallExpr)

		obj := typeutil.Callee(info, call)
		if analysisinternal.IsFunctionNamed(obj, "net", "Dial", "DialTimeout") ||
			analysisinternal.IsMethodNamed(obj, "net", "Dialer", "Dial") {

			switch address := call.Args[1].(type) {
			case *ast.CallExpr:
				// net.Dial("tcp", fmt.Sprintf("%s:%d", ...))
				checkAddr(address, nil)

			case *ast.Ident:
				// addr := fmt.Sprintf("%s:%d", ...)
				// ...
				// net.Dial("tcp", addr)

				// Search for decl of addrVar within common ancestor of addrVar and Dial call.
				if addrVar, ok := info.Uses[address].(*types.Var); ok {
					pos := addrVar.Pos()
					for curAncestor := range curCall.Ancestors() {
						if curIdent, ok := curAncestor.FindPos(pos, pos); ok {
							// curIdent is the declaring ast.Ident of addr.
							switch parent := curIdent.Parent().Node().(type) {
							case *ast.AssignStmt:
								if len(parent.Rhs) == 1 {
									// Have: addr := fmt.Sprintf("%s:%d", ...)
									checkAddr(parent.Rhs[0], call)
								}

							case *ast.ValueSpec:
								if len(parent.Values) == 1 {
									// Have: var addr = fmt.Sprintf("%s:%d", ...)
									checkAddr(parent.Values[0], call)
								}
							}
							break
						}
					}
				}
			}
		}
	}
	return nil, nil
}
