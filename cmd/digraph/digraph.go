// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main // import "golang.org/x/tools/cmd/digraph"

// TODO(adonovan):
// - support input files other than stdin
// - support alternative formats (AT&T GraphViz, CSV, etc),
//   a comment syntax, etc.
// - allow queries to nest, like Blaze query language.

import (
	"bufio"
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/internal/graph"
	"golang.org/x/tools/internal/graph/graphfmt"
)

func usage() {
	// Extract the content of the /* ... */ comment in doc.go.
	_, after, _ := strings.Cut(doc, "/*")
	doc, _, _ := strings.Cut(after, "*/")
	io.WriteString(flag.CommandLine.Output(), doc)
	flag.PrintDefaults()

	os.Exit(2)
}

//go:embed doc.go
var doc string

func main() {
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
	}

	if err := doDigraph(args[0], args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "digraph: %s\n", err)
		os.Exit(1)
	}
}

type nodelist []string

func (l nodelist) println(sep string) {
	for i, node := range l {
		if i > 0 {
			fmt.Fprint(stdout, sep)
		}
		fmt.Fprint(stdout, node)
	}
	fmt.Fprintln(stdout)
}

type nodeset map[string]bool

func (s nodeset) sort() nodelist {
	nodes := make(nodelist, len(s))
	var i int
	for node := range s {
		nodes[i] = node
		i++
	}
	sort.Strings(nodes)
	return nodes
}

// A digraph maps nodes to the non-nil set of their immediate successors.
type digraph map[string]nodeset

func (g digraph) Nodes() iter.Seq[string] {
	return slices.Values(slices.Sorted(maps.Keys(g)))
}

func (g digraph) NumNodes() int {
	return len(g)
}

func (g digraph) Out(node string) iter.Seq[string] {
	// Out must be deterministic.
	return slices.Values(slices.Sorted(maps.Keys(g[node])))
}

var _ graph.Graph[string] = digraph{}

func (g digraph) addNode(node string) nodeset {
	edges := g[node]
	if edges == nil {
		edges = make(nodeset)
		g[node] = edges
	}
	return edges
}

func (g digraph) addEdges(from string, to ...string) {
	edges := g.addNode(from)
	for _, to := range to {
		g.addNode(to)
		edges[to] = true
	}
}

func (g digraph) nodelist() nodelist {
	return nodelist(slices.Collect(g.Nodes()))
}

func (g digraph) sccs() []nodeset {
	var sccs []nodeset
	for _, comp := range graph.SCCs(g) {
		scc := make(nodeset)
		for _, node := range comp {
			scc[node] = true
		}
		if len(scc) == 1 && !g[comp[0]][comp[0]] {
			continue // trivial SCC without even a self-edge
		}
		sccs = append(sccs, scc)
	}
	return sccs
}

func (g digraph) allpaths(from, to string) error {
	seen := graph.AllPaths(g, from, to)

	// For each marked node, collect its marked successors.
	var edges []string
	for n := range seen {
		for succ := range g[n] {
			if seen[succ] {
				edges = append(edges, n+" "+succ)
			}
		}
	}

	// Sort (so that this method is deterministic) and print edges.
	sort.Strings(edges)
	for _, e := range edges {
		fmt.Fprintln(stdout, e)
	}

	return nil
}

func (g digraph) somepath(from, to string) error {
	path := graph.ShortestPath(g, from, to)
	if path == nil {
		return fmt.Errorf("no path from %q to %q", from, to)
	}
	for i := 0; i < len(path)-1; i++ {
		fmt.Fprintln(stdout, path[i]+" "+path[i+1])
	}
	return nil
}

func parse(rd io.Reader) (digraph, error) {
	g := make(digraph)

	var linenum int
	// We avoid bufio.Scanner as it imposes a (configurable) limit
	// on line length, whereas Reader.ReadString does not.
	in := bufio.NewReader(rd)
	for {
		linenum++
		line, err := in.ReadString('\n')
		eof := false
		if err == io.EOF {
			eof = true
		} else if err != nil {
			return nil, err
		}
		// Split into words, honoring double-quotes per Go spec.
		words, err := split(line)
		if err != nil {
			return nil, fmt.Errorf("at line %d: %v", linenum, err)
		}
		if len(words) > 0 {
			g.addEdges(words[0], words[1:]...)
		}
		if eof {
			break
		}
	}
	return g, nil
}

// Overridable for redirection.
var stdin io.Reader = os.Stdin
var stdout io.Writer = os.Stdout

func doDigraph(cmd string, args []string) error {
	// Parse the input graph.
	g, err := parse(stdin)
	if err != nil {
		return err
	}

	// Parse the command line.
	switch cmd {
	case "nodes":
		if len(args) != 0 {
			return fmt.Errorf("usage: digraph nodes")
		}
		g.nodelist().println("\n")

	case "degree":
		if len(args) != 0 {
			return fmt.Errorf("usage: digraph degree")
		}
		nodes := make(nodeset)
		for node := range g {
			nodes[node] = true
		}
		rev := graph.Transpose(g)
		for _, node := range nodes.sort() {
			inDegree := 0
			for range rev.Out(node) {
				inDegree++
			}
			fmt.Fprintf(stdout, "%d\t%d\t%s\n", inDegree, len(g[node]), node)
		}

	case "transpose":
		if len(args) != 0 {
			return fmt.Errorf("usage: digraph transpose")
		}
		var revEdges []string
		rev := graph.Transpose(g)
		for node := range rev.Nodes() {
			for succ := range rev.Out(node) {
				revEdges = append(revEdges, fmt.Sprintf("%s %s", node, succ))
			}
		}
		sort.Strings(revEdges) // make output deterministic
		for _, e := range revEdges {
			fmt.Fprintln(stdout, e)
		}

	case "succs", "preds":
		if len(args) == 0 {
			return fmt.Errorf("usage: digraph %s <node> ... ", cmd)
		}
		var gr graph.Graph[string] = g
		if cmd == "preds" {
			gr = graph.Transpose(g)
		}
		result := make(nodeset)
		for _, root := range args {
			if g[root] == nil {
				return fmt.Errorf("no such node %q", root)
			}
			for succ := range gr.Out(root) {
				result[succ] = true
			}
		}
		result.sort().println("\n")

	case "forward", "reverse":
		if len(args) == 0 {
			return fmt.Errorf("usage: digraph %s <node> ... ", cmd)
		}
		roots := make(nodeset)
		for _, root := range args {
			if g[root] == nil {
				return fmt.Errorf("no such node %q", root)
			}
			roots[root] = true
		}
		var gr graph.Graph[string] = g
		if cmd == "reverse" {
			gr = graph.Transpose(g)
		}
		nodeset(graph.Reachable(gr, roots.sort()...)).sort().println("\n")

	case "somepath":
		if len(args) != 2 {
			return fmt.Errorf("usage: digraph somepath <from> <to>")
		}
		from, to := args[0], args[1]
		if g[from] == nil {
			return fmt.Errorf("no such 'from' node %q", from)
		}
		if g[to] == nil {
			return fmt.Errorf("no such 'to' node %q", to)
		}
		if err := g.somepath(from, to); err != nil {
			return err
		}

	case "allpaths":
		if len(args) != 2 {
			return fmt.Errorf("usage: digraph allpaths <from> <to>")
		}
		from, to := args[0], args[1]
		if g[from] == nil {
			return fmt.Errorf("no such 'from' node %q", from)
		}
		if g[to] == nil {
			return fmt.Errorf("no such 'to' node %q", to)
		}
		if err := g.allpaths(from, to); err != nil {
			return err
		}

	case "sccs":
		if len(args) != 0 {
			return fmt.Errorf("usage: digraph sccs")
		}
		buf := new(bytes.Buffer)
		oldStdout := stdout
		stdout = buf
		for _, scc := range g.sccs() {
			scc.sort().println(" ")
		}
		lines := strings.SplitAfter(buf.String(), "\n")
		sort.Strings(lines)
		stdout = oldStdout
		io.WriteString(stdout, strings.Join(lines, ""))

	case "scc":
		if len(args) != 1 {
			return fmt.Errorf("usage: digraph scc <node>")
		}
		node := args[0]
		if g[node] == nil {
			return fmt.Errorf("no such node %q", node)
		}
		for _, scc := range g.sccs() {
			if scc[node] {
				scc.sort().println("\n")
				break
			}
		}

	case "focus":
		if len(args) != 1 {
			return fmt.Errorf("usage: digraph focus <node>")
		}
		node := args[0]
		if g[node] == nil {
			return fmt.Errorf("no such node %q", node)
		}

		edges := make(map[string]struct{})
		for from := range graph.Reachable(g, node) {
			for to := range g[from] {
				edges[fmt.Sprintf("%s %s", from, to)] = struct{}{}
			}
		}

		gtrans := graph.Transpose(g)
		for from := range graph.Reachable(gtrans, node) {
			for to := range gtrans.Out(from) {
				edges[fmt.Sprintf("%s %s", to, from)] = struct{}{}
			}
		}

		edgesSorted := make([]string, 0, len(edges))
		for e := range edges {
			edgesSorted = append(edgesSorted, e)
		}
		sort.Strings(edgesSorted)
		fmt.Fprintln(stdout, strings.Join(edgesSorted, "\n"))

	case "to":
		if len(args) != 1 || args[0] != "dot" {
			return fmt.Errorf("usage: digraph to dot")
		}
		stdout.Write([]byte(graphfmt.Dot[string]{}.Sprint(g)))

	default:
		return fmt.Errorf("no such command %q", cmd)
	}

	return nil
}

// -- Utilities --------------------------------------------------------

// split splits a line into words, which are generally separated by
// spaces, but Go-style double-quoted string literals are also supported.
// (This approximates the behaviour of the Bourne shell.)
//
//	`one "two three"` -> ["one" "two three"]
//	`a"\n"b` -> ["a\nb"]
func split(line string) ([]string, error) {
	var (
		words   []string
		inWord  bool
		current bytes.Buffer
	)

	for len(line) > 0 {
		r, size := utf8.DecodeRuneInString(line)
		if unicode.IsSpace(r) {
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		} else if r == '"' {
			var ok bool
			size, ok = quotedLength(line)
			if !ok {
				return nil, errors.New("invalid quotation")
			}
			s, err := strconv.Unquote(line[:size])
			if err != nil {
				return nil, err
			}
			current.WriteString(s)
			inWord = true
		} else {
			current.WriteRune(r)
			inWord = true
		}
		line = line[size:]
	}
	if inWord {
		words = append(words, current.String())
	}
	return words, nil
}

// quotedLength returns the length in bytes of the prefix of input that
// contain a possibly-valid double-quoted Go string literal.
//
// On success, n is at least two (""); input[:n] may be passed to
// strconv.Unquote to interpret its value, and input[n:] contains the
// rest of the input.
//
// On failure, quotedLength returns false, and the entire input can be
// passed to strconv.Unquote if an informative error message is desired.
//
// quotedLength does not and need not detect all errors, such as
// invalid hex or octal escape sequences, since it assumes
// strconv.Unquote will be applied to the prefix.  It guarantees only
// that if there is a prefix of input containing a valid string literal,
// its length is returned.
//
// TODO(adonovan): move this into a strconv-like utility package.
func quotedLength(input string) (n int, ok bool) {
	var offset int

	// next returns the rune at offset, or -1 on EOF.
	// offset advances to just after that rune.
	next := func() rune {
		if offset < len(input) {
			r, size := utf8.DecodeRuneInString(input[offset:])
			offset += size
			return r
		}
		return -1
	}

	if next() != '"' {
		return // error: not a quotation
	}

	for {
		r := next()
		if r == '\n' || r < 0 {
			return // error: string literal not terminated
		}
		if r == '"' {
			return offset, true // success
		}
		if r == '\\' {
			var skip int
			switch next() {
			case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', '"':
				skip = 0
			case '0', '1', '2', '3', '4', '5', '6', '7':
				skip = 2
			case 'x':
				skip = 2
			case 'u':
				skip = 4
			case 'U':
				skip = 8
			default:
				return // error: invalid escape
			}

			for i := 0; i < skip; i++ {
				next()
			}
		}
	}
}
