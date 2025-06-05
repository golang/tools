// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

// This file produces the "Browse GOARCH assembly of f" HTML report.
//
// See also:
// - ./codeaction.go - computes the symbol and offers the CodeAction command.
// - ../server/command.go - handles the command by opening a web page.
// - ../server/server.go - handles the HTTP request and calls this function.
//
// For language-server behavior in Go assembly language files,
// see [golang.org/x/tools/gopls/internal/goasm].

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/util/morestrings"
)

// AssemblyHTML returns an HTML document containing an assembly listing of the selected function.
//
// TODO(adonovan): cross-link jumps and block labels, like github.com/aclements/objbrowse.
//
// See gopls/internal/test/integration/misc/webserver_test.go for tests.
func AssemblyHTML(ctx context.Context, snapshot *cache.Snapshot, w http.ResponseWriter, pkg *cache.Package, symbol string, web Web) {
	// Prepare to compile the package with -S, and capture its stderr stream.
	// We use "go test -c" not "go build" as it covers all three packages
	// (p, "p [p.test]", "p_test [p.test]") in the directory, if they exist.
	// (See also compileropt.go.)
	inv, cleanupInvocation, err := snapshot.GoCommandInvocation(cache.NoNetwork, pkg.Metadata().CompiledGoFiles[0].DirPath(),
		"test", []string{
			"-c",
			"-o", os.DevNull,
			"-gcflags=-S",
			".",
		})
	if err != nil {
		// e.g. failed to write overlays (rare)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cleanupInvocation()

	escape := html.EscapeString

	// Emit the start of the report.
	titleHTML := fmt.Sprintf("%s assembly for %s",
		escape(snapshot.View().GOARCH()),
		escape(symbol))
	io.WriteString(w, `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>`+titleHTML+`</title>
  <link rel="stylesheet" href="/assets/common.css">
  <script src="/assets/common.js"></script>
</head>
<body>
<h1>`+titleHTML+`</h1>
<p>
  <a href='https://go.dev/doc/asm'>A Quick Guide to Go's Assembler</a>
</p>
<p>
  Experimental. <a href='https://github.com/golang/go/issues/67478'>Contributions welcome!</a>
</p>
<p>
  Click on a source line marker <code>L1234</code> to navigate your editor there.
  (VS Code users: please upvote <a href='https://github.com/microsoft/vscode/issues/208093'>#208093</a>)
</p>
<p id='compiling'>Compiling...</p>
<pre>
`)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// At this point errors must be reported by writing HTML.
	// To do this, set "status" return early.

	var buf bytes.Buffer
	status := "Reload the page to recompile."
	defer func() {
		// Update the "Compiling..." message.
		fmt.Fprintf(&buf, `
</pre>
<script>
document.getElementById('compiling').innerText = %q;
</script>
</body>`, status)
		w.Write(buf.Bytes())
	}()

	// Compile the package.
	_, stderr, err, _ := snapshot.View().GoCommandRunner().RunRaw(ctx, *inv)
	if err != nil {
		status = fmt.Sprintf("compilation failed: %v", err)
		return
	}

	// Write the rest of the report.
	content := stderr.String()

	// insnRx matches an assembly instruction line.
	// Submatch groups are: (offset-hex-dec, file-line-column, instruction).
	insnRx := regexp.MustCompile(`^(\s+0x[0-9a-f ]+)\(([^)]*)\)\s+(.*)$`)

	// Parse the functions of interest out of the listing.
	// Each function is of the form:
	//
	//     symbol STEXT k=v...
	//         0x0000 00000 (/file.go:123) NOP...
	//         ...
	//
	// Allow matches of symbol, symbol.func1, symbol.deferwrap, etc.
	on := false
	for line := range strings.SplitSeq(content, "\n") {
		// start of function symbol?
		if strings.Contains(line, " STEXT ") {
			on = strings.HasPrefix(line, symbol) &&
				(line[len(symbol)] == ' ' || line[len(symbol)] == '.')
		}
		if !on {
			continue // within uninteresting symbol
		}

		// In lines of the form
		//   "\t0x0000 00000 (/file.go:123) NOP..."
		// replace the "(/file.go:123)" portion with an "L0123" source link.
		// Skip filenames of the form "<foo>".
		if parts := insnRx.FindStringSubmatch(line); parts != nil {
			link := "     " // if unknown
			if file, linenum, ok := morestrings.CutLast(parts[2], ":"); ok && !strings.HasPrefix(file, "<") {
				if linenum, err := strconv.Atoi(linenum); err == nil {
					text := fmt.Sprintf("L%04d", linenum)
					link = sourceLink(text, web.SrcURL(file, linenum, 1))
				}
			}
			fmt.Fprintf(&buf, "%s\t%s\t%s", escape(parts[1]), link, escape(parts[3]))
		} else {
			buf.WriteString(escape(line))
		}
		buf.WriteByte('\n')
	}
}
