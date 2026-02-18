// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package excfg

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	internalastutil "golang.org/x/tools/internal/astutil"
)

// String formats the control-flow graph for ease of debugging.
func (cfg *CFG) String() string {
	var buf strings.Builder
	for _, b := range cfg.Blocks {
		fmt.Fprintf(&buf, "%s: %s, CFG block .%d\n", b, b.Kind, b.CFGBlock)
		fmt.Fprintf(&buf, "\t%s\n", formatBlockNode(cfg, b))
		if len(b.Succs) > 0 {
			fmt.Fprintf(&buf, "\tsuccs:")
			for _, succ := range b.Succs {
				fmt.Fprintf(&buf, " %s", succ)
			}
			buf.WriteByte('\n')
		}
		buf.WriteByte('\n')
	}
	return buf.String()
}

func (b *Block) String() string {
	return fmt.Sprintf("B%d", b.Index)
}

func formatBlockNode(cfg *CFG, b *Block) string {
	// Replace sub-expressions with their block names.
	subs := cfg.Subexprs(b)
	subMap := make(map[ast.Node]*Block)
	for _, sub := range subs {
		subMap[sub.Node] = sub
	}

	// We can't necessarily modify the AST in place, so we defensively clone it.
	n := internalastutil.CloneNode(b.Node)
	// The tricky part is that subMap is indexed by pre, but we need to modify
	// post. To solve this, we number the nodes in a first pass over the
	// pre-clone, and then use the same numbering to modify the post-clone.
	apply := func(n ast.Node, f func(i int, c *astutil.Cursor) bool) {
		i := 0
		astutil.Apply(n, func(c *astutil.Cursor) bool {
			visit := f(i, c)
			i++
			return visit
		}, nil)
	}
	// First pass: number nodes and collect numbered rewrites.
	type rewrite struct {
		i int
		b *Block
	}
	var rewrites []rewrite
	apply(b.Node, func(i int, c *astutil.Cursor) bool {
		node := c.Node()
		if sub, ok := subMap[node]; ok {
			rewrites = append(rewrites, rewrite{i, sub})
			return false
		}
		return true
	})
	// Second pass: apply numbered rewrites.
	apply(n, func(i int, c *astutil.Cursor) bool {
		if len(rewrites) > 0 && rewrites[0].i == i {
			id := &ast.Ident{Name: rewrites[0].b.String()}
			c.Replace(id)
			rewrites = rewrites[1:]
			return false
		}
		return true
	})

	// Format.
	var buf bytes.Buffer
	format.Node(&buf, cfg.Fset, n)
	return string(bytes.Replace(buf.Bytes(), []byte("\n"), []byte("\n\t"), -1))
}
