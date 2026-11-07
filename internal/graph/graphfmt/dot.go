// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package graphfmt serializes graphs to external representations.
package graphfmt

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"golang.org/x/tools/internal/graph"
)

// Dot contains options for generating a Graphviz Dot graph from a
// [graph.Graph].
type Dot[NodeID comparable] struct {
	// Name is the name given to the graph. Usually this can be
	// left blank.
	Name string

	// Label returns the string to use as a label for the given
	// node. If nil, nodes are labeled with their node numbers.
	Label func(node NodeID) string

	// NodeAttrs, if non-nil, returns a set of attributes for a
	// node. If this includes a "label" attribute, it overrides
	// the label returned by Label.
	NodeAttrs func(node NodeID) []DotAttr

	// EdgeAttrs, if non-nil, returns a set of attributes for an
	// edge.
	EdgeAttrs func(from, to NodeID) []DotAttr

	// ClusterOf, if non-nil, returns the cluster of a node, or nil if the node
	// should not be in a cluster. Multiple nodes with the same cluster will be
	// arranged together and enclosed in a box.
	ClusterOf func(node NodeID) *DotCluster
}

// DotAttr is an attribute for a Dot node or edge.
type DotAttr struct {
	Name string

	// Val is the value of this attribute. It may be a string
	// (which will be escaped), bool, int, uint, float64 or
	// DotLiteral.
	Val any
}

// DotLiteral is a string literal that should be passed to dot
// unescaped.
type DotLiteral string

// DotCluster represents a cluster of nodes arranged together.
type DotCluster struct {
	Label string
	Attrs []DotAttr
}

func defaultLabel[NodeID comparable](node NodeID) string {
	return fmt.Sprint(node)
}

// Sprint returns the Dot form of g as a string.
func (d Dot[NodeID]) Sprint(g graph.Graph[NodeID]) string {
	w := new(strings.Builder)

	nodeLabel := d.Label
	if nodeLabel == nil {
		nodeLabel = defaultLabel
	}

	fmt.Fprintf(w, "digraph %s {\n", dotString(d.Name))

	// Collect nodes by cluster.
	type cluster struct {
		c     *DotCluster
		nodes []NodeID
	}
	var clusters []cluster
	clusterIDs := make(map[*DotCluster]int)
	nodeNums := make(map[NodeID]int)
	for nid := range g.Nodes() {
		var c *DotCluster
		if d.ClusterOf != nil {
			c = d.ClusterOf(nid)
		}
		id, ok := clusterIDs[c]
		if !ok {
			id = len(clusters)
			clusterIDs[c] = id
			clusters = append(clusters, cluster{c: c})
		}
		clusters[id].nodes = append(clusters[id].nodes, nid)
		nodeNums[nid] = len(nodeNums)
	}

	// Emit each cluster.
	for cid, c := range clusters {
		if c.c != nil {
			fmt.Fprintf(w, "subgraph cluster_%d {\n", cid)
			if attrs := formatAttrs(c.c.Attrs, c.c.Label); attrs != "" {
				fmt.Fprintf(w, "graph %s;", attrs)
			}
		}

		// Emit nodes. We don't emit edges yet because an edge may have a
		// forward-reference, which could define the target node in the wrong
		// cluster.
		for _, nid := range c.nodes {
			// Define node.
			var attrList []DotAttr
			var label string
			if d.NodeAttrs != nil {
				attrList = d.NodeAttrs(nid)
			}
			if nodeLabel != nil {
				label = nodeLabel(nid)
			}
			fmt.Fprintf(w, "n%d%s;\n", nodeNums[nid], formatAttrs(attrList, label))
		}

		if c.c != nil {
			fmt.Fprintf(w, "}\n")
		}
	}

	// Emit edges.
	for nid := range g.Nodes() {
		for succ := range g.Out(nid) {
			var attrs string
			if d.EdgeAttrs != nil {
				attrs = formatAttrs(d.EdgeAttrs(nid, succ), "")
			}
			fmt.Fprintf(w, "n%d -> n%d%s;\n", nodeNums[nid], nodeNums[succ], attrs)
		}
	}

	fmt.Fprintf(w, "}\n")

	return w.String()
}

// SVG attempts to render g to an SVG.
func (d Dot[NodeID]) SVG(w io.Writer, g graph.Graph[NodeID]) error {
	// Check that we can run Dot at all.
	dot := exec.Command("dot", "-V")
	if err := dot.Run(); err != nil {
		return fmt.Errorf("cannot run dot: %s", err)
	}

	// Convert to SVG.
	//
	// TODO: Consider lifting nice graph viewer from go.dev/cl/192706
	dot = exec.Command("dot", "-Tsvg", "-")
	in, err := dot.StdinPipe()
	if err != nil {
		return fmt.Errorf("running dot: %s", err)
	}
	dot.Stdout = w
	if err := dot.Start(); err != nil {
		return fmt.Errorf("running dot: %s", err)
	}
	_, err = in.Write([]byte(d.Sprint(g)))
	in.Close()
	if err != nil {
		// Let Dot exit, but ignore any errors from it.
		dot.Wait()
		return err
	}
	if err := dot.Wait(); err != nil {
		if err, ok := err.(*exec.ExitError); ok && len(err.Stderr) > 0 {
			return fmt.Errorf("running dot: %s\n%s", err, err.Stderr)
		}
		return fmt.Errorf("running dot: %s", err)
	}
	return nil
}

var dotStringer = strings.NewReplacer(
	"\n", `\n`,
	`\`, `\\`,
	`"`, `\"`,
	`{`, `\{`,
	`}`, `\}`,
	`<`, `\<`,
	`>`, `\>`,
	`|`, `\|`,
)

// dotString returns s as a quoted dot string.
func dotString(s string) string {
	var buf strings.Builder
	buf.WriteByte('"')
	dotStringer.WriteString(&buf, s)
	buf.WriteByte('"')
	return buf.String()
}

// formatAttrs formats attrs as a dot attribute set, including the surrounding
// brackets. If "label" is not present in attrs and the label argument is not
// "", it adds the given label attribute. If attrs is empty and label is "", it
// returns an empty string.
func formatAttrs(attrs []DotAttr, label string) string {
	if len(attrs) == 0 && label == "" {
		return ""
	}
	var buf strings.Builder
	buf.WriteString(" [")
	haveLabel := false
	for i, attr := range attrs {
		if i > 0 {
			buf.WriteString(",")
		}
		formatAttr(&buf, attr)
		if attr.Name == "label" {
			haveLabel = true
		}
	}
	if !haveLabel && label != "" {
		if len(attrs) > 0 {
			buf.WriteString(",")
		}
		formatAttr(&buf, DotAttr{"label", label})
	}
	buf.WriteString("]")
	return buf.String()
}

func formatAttr(buf *strings.Builder, attr DotAttr) {
	buf.WriteString(attr.Name)
	buf.WriteString("=")
	switch val := attr.Val.(type) {
	case string:
		buf.WriteString(dotString(val))
	case bool, int, uint, float64:
		fmt.Fprintf(buf, "%v", val)
	case DotLiteral:
		buf.WriteString(string(val))
	default:
		panic(fmt.Sprintf("dot attribute %s had unknown type %T", attr.Name, attr.Val))
	}
}
