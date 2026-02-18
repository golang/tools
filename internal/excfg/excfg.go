// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package excfg constructs control-flow graphs of statements and expressions in
// a Go function, including intra-expression control flow.
package excfg

import (
	"go/ast"
	"go/token"
	"iter"
	"slices"
	"sync"

	"golang.org/x/tools/go/cfg"
	"golang.org/x/tools/internal/graph"
)

// CFG is a control flow graph over [ast.Node]s, including control flow within
// expressions. Beyond the statement-level control flow represented by
// [cfg.CFG], this graph contains a separate block for every statement, every
// call (even within an expression), and for each branch of short-circuiting
// operators.
//
// AST nodes that purely affect control flow may not appear in any block. For
// example, in "if x && y { ... }", neither the "if" statement nor the "&&" node
// will appear, as both are translated into pure control flow.
//
// CFG implements graph.Graph[int] and is a compact graph.
type CFG struct {
	Blocks []*Block

	Fset *token.FileSet

	subexprs struct {
		once sync.Once
		m    [][]*Block // Indexed by ExBlock.Index
	}
}

// NumNodes returns the number of blocks in c.
func (c *CFG) NumNodes() int {
	return len(c.Blocks)
}

// Nodes yields the indexes of all blocks in c. This is simply the sequence [0, NumNodes()).
func (c *CFG) Nodes() iter.Seq[int] {
	return func(yield func(int) bool) {
		for i := range c.Blocks {
			if !yield(i) {
				break
			}
		}
	}
}

// Out yields the indexes of the successors of block.
func (c *CFG) Out(block int) iter.Seq[int] {
	return func(yield func(int) bool) {
		for _, succ := range c.Blocks[block].Succs {
			if !yield(int(succ.Index)) {
				break
			}
		}
	}
}

func (c *CFG) IsCompact() bool {
	return true
}

var _ graph.Graph[int] = (*CFG)(nil)

type Block struct {
	Kind ExKind

	// Node is a statement, expression, or ast.ValueSpec.
	//
	// An expression may contain sub-expressions computed in preceding blocks of
	// type ExKindBool and ExKindCall. Each sub-expression block is used by
	// exactly one other block, though that block may not be an immediate
	// successor. All sub-expressions must be used at the end of an ExKindStmt
	// or ExKindIf block. A sub-expression block will have a lower Index than
	// the block that consumes the sub-expression.
	Node ast.Node

	// Succs is a list of successor nodes. The length and interpretation of this
	// depends on Kind.
	Succs []*Block

	// Use is set to the block that consumes the value of this sub-expression
	// block. Only set for ExKindSubExpr and ExKindBool.
	Use *Block

	// Index is the index of this block in ExCFG.Blocks.
	Index int32

	// CFGBlock is the index of the cfg.Block this expression block was derived
	// from.
	CFGBlock int32

	succs [2]*Block // Storage for Succs
}

type ExKind int8

//go:generate stringer -type ExKind .

const (
	ExKindInvalid ExKind = iota

	// An ExKindStmt ExBlock is a statement, expression, or ValueSpec with no
	// value. It has 0 or 1 successors. It consumes any preceding sub-expression
	// values.
	ExKindStmt

	// An ExKindIf ExBlock is a boolean-typed expression that branches to one of
	// two successors. Like ExKindStmt, it consumes any preceding
	// sub-expressions. Successor 0 is the true branch and successor 1 is the
	// false branch.
	ExKindIf

	// An ExKindSubExpr ExBlock is a sub-expression whose value is used as part
	// of another ExBlock. It has exactly one successor.
	ExKindSubExpr

	// An ExKindBool ExBlock is a sub-expression whose value is used as part of
	// another ExBlock, like ExKindSubExpr, but it branches to two successors
	// based on the value of the sub-expression. Successor 0 is the true branch
	// and successor 1 is the false branch.
	ExKindBool
)

func New(cfg *cfg.CFG, fset *token.FileSet) *CFG {
	eb := builder{
		entry: make([]*Block, len(cfg.Blocks)),
	}

	// Allocate "placeholder" ExBlocks for each cfg.Block. We often need to
	// record a successor that hasn't yet been converted to an ExBlock, so we
	// use these placeholders for this and resolve them at the end.
	placeholders := make([]Block, len(cfg.Blocks))
	for i := range placeholders {
		placeholders[i].CFGBlock = int32(i)
		placeholders[i].Index = -1 // Indicates a placeholder
		eb.entry[i] = &placeholders[i]
	}

	// Convert each cfg.Block into one or more ExBlocks.
	//
	// We do this backwards so that successor blocks are usually already visited
	// and we can link them up eagerly.
	for i := len(cfg.Blocks) - 1; i >= 0; i-- {
		b := cfg.Blocks[i]
		if b.Live {
			eb.visitBlock(b)
		}
	}

	// Put blocks in forward order.
	eb.assertReverse()
	slices.Reverse(eb.blocks)
	for i, b := range eb.blocks {
		b.Index = int32(i)
	}

	// Resolve placeholder blocks.
	for _, b := range eb.blocks {
		for i, succ := range b.Succs {
			if succ.Index == -1 {
				b.Succs[i] = eb.entry[succ.CFGBlock]
			}
		}
	}

	return &CFG{Blocks: eb.blocks, Fset: fset}
}

// Subexprs returns the preceding sub-expression blocks consumed by b. This is
// the inverse of [Block.Use]. The slice is in ast.Inspect order for b.Node.
func (g *CFG) Subexprs(b *Block) []*Block {
	g.subexprs.once.Do(func() {
		// Count how many back-references we need.
		n := make([]int, 1+len(g.Blocks))
		for _, b := range g.Blocks {
			if b.Use != nil {
				n[1+b.Use.Index]++
			}
		}
		// Convert n from a count to an index.
		for i := 1; i < len(n); i++ {
			n[i] += n[i-1]
		}
		// Allocate a single block and slice it up.
		allSubexprs := make([]*Block, n[len(n)-1])
		g.subexprs.m = make([][]*Block, len(g.Blocks))
		for i := range g.subexprs.m {
			g.subexprs.m[i] = allSubexprs[n[i]:n[i+1]]
		}
		// Collect back-references.
		for _, b := range g.Blocks {
			if b.Use != nil {
				allSubexprs[n[b.Use.Index]] = b
				n[b.Use.Index]++
			}
		}
	})
	return g.subexprs.m[b.Index]
}

type builder struct {
	blocks []*Block

	// Map from cfg.Block.Index to the entry ExBlock. Initially, this is
	// populated with placeholder blocks that can be used in ExBlock.Succs. Once
	// all blocks have been visited, they should all be replaced with actual
	// ExBlocks, and NewExCFG will replace references to the placeholders.
	entry []*Block

	// cfgBlock is the cfg.Block we're currently translating.
	cfgBlock *cfg.Block

	// For the most part, we build the block list in reverse so that successors
	// are almost always already available. However, at the bottom of the
	// recursive process we use ast.Inspect, we forces us to build it in forward
	// order. Hence, we track whether we're in "reverse" or "forward" mode and
	// when we exit forward mode, we reverse the forward part of the block list.
	// Finally, at the very end of traversal, we reverse the entire block list
	// into forward order. We never enter reverse mode from forward mode, so all
	// remains linear time with simple tracking.

	// forwardStart is in the index into blocks of the start of a forward region.
	forwardStart int
	// forwardDepth is the recursive depth of forward regions.
	forwardDepth int
}

func (eb *builder) assertReverse() {
	if eb.forwardDepth != 0 {
		panic("unexpected reverse operation in forward region")
	}
}

func (eb *builder) enterForward() {
	if eb.forwardDepth == 0 {
		eb.forwardStart = len(eb.blocks)
	}
	eb.forwardDepth++
}

func (eb *builder) exitForward() {
	eb.forwardDepth--
	if eb.forwardDepth == 0 {
		// Put forward region into reverse order.
		slices.Reverse(eb.blocks[eb.forwardStart:])
	}
}

func (eb *builder) visitBlock(cb *cfg.Block) {
	eb.cfgBlock = cb

	var next *Block
	if len(cb.Succs) > 0 {
		next = eb.entry[cb.Succs[0].Index]
	}
	isIf := len(cb.Succs) == 2
	for ni := len(cb.Nodes) - 1; ni >= 0; ni-- {
		eb.assertReverse()
		node := cb.Nodes[ni]

		if isIf {
			next = eb.visitCond(node.(ast.Expr), next, eb.entry[cb.Succs[1].Index])
			isIf = false
		} else {
			nodeEntry, nodeExit := eb.visitNode(ExKindStmt, node)
			if next != nil {
				nodeExit.Succs = nodeExit.succs[:1]
				nodeExit.Succs[0] = next
			}
			next = nodeEntry
		}
	}

	eb.entry[cb.Index] = next
}

// visitNode traverses a statement or expression node. It creates a sub-graph of
// one or more blocks to compute root and returns the entry and exit blocks of
// that subgraph. The type of the exit block is set to kind. The caller must set
// the successors of the exit block.
func (eb *builder) visitNode(kind ExKind, root ast.Node) (entry, exit *Block) {
	eb.enterForward()
	defer eb.exitForward()

	entryI := len(eb.blocks)
	block := &Block{Kind: kind, Node: root, CFGBlock: eb.cfgBlock.Index}

	preds := make([]**Block, 0, 3)
	// link connects a subgraph of blocks by connecting the previous exit block
	// to entry and recording the new exit block's successors that should be set
	// to the next entry block. This is necessary because we're processing
	// nodes in forward order here, so successors must be back-patched.
	link := func(entry *Block, exitSuccs ...**Block) {
		for _, pred := range preds {
			*pred = entry
		}
		preds = append(preds[:0], exitSuccs...)
	}

	// Traverse root in pre-order, stopping at any sub-expressions that affect
	// control flow to create blocks for those sub-expressions.
	ast.Inspect(root, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			// The body of the function literal is not part of the control flow.
			return false

		case *ast.BinaryExpr:
			switch n.Op {
			case token.LAND:
				xin, xout := eb.visitNode(ExKindBool, n.X)
				yin, yout := eb.visitNode(ExKindSubExpr, n.Y)

				xout.Use, yout.Use = block, block

				xout.Succs = xout.succs[:2]
				xout.Succs[0] = yin // If true, evaluate Y.

				yout.Succs = yout.succs[:1]

				// x && y will produce:
				//
				//      x
				//     / \
				//    y   \
				//     \  /
				//   (x && y)
				link(xin, &xout.succs[1], &yout.succs[0])

				return false

			case token.LOR:
				xin, xout := eb.visitNode(ExKindBool, n.X)
				yin, yout := eb.visitNode(ExKindSubExpr, n.Y)

				xout.Use, yout.Use = block, block

				xout.Succs = xout.succs[:2]
				xout.Succs[1] = yin // If false, evaluate Y.

				yout.Succs = yout.succs[:1]

				link(xin, &xout.succs[0], &yout.succs[0])

				return false
			}

		case *ast.CallExpr:
			// f() + g() + h() has an AST like
			//
			//              +
			//             / \
			//           f()  \
			//                 +
			//                / \
			//              g() h()
			//
			// Because of the traversal order, we'll produce a linkage like
			//
			//   f() => g() => h() => root +
			if n != root {
				in, out := eb.visitNode(ExKindSubExpr, n)
				out.Use = block
				out.Succs = out.succs[:1]
				link(in, &out.succs[0])
				return false
			}
		}
		return true
	})

	link(block)
	eb.blocks = append(eb.blocks, block)
	return eb.blocks[entryI], block
}

func (eb *builder) visitCond(n ast.Expr, tSucc, fSucc *Block) (entry *Block) {
	eb.assertReverse()

	switch n := n.(type) {
	case *ast.ParenExpr:
		return eb.visitCond(n.X, tSucc, fSucc)

	case *ast.UnaryExpr:
		if n.Op == token.NOT {
			return eb.visitCond(n.X, fSucc, tSucc)
		}

	case *ast.BinaryExpr:
		switch n.Op {
		case token.LAND:
			y := eb.visitCond(n.Y, tSucc, fSucc)
			x := eb.visitCond(n.X, y, fSucc)
			return x
		case token.LOR:
			y := eb.visitCond(n.Y, tSucc, fSucc)
			x := eb.visitCond(n.X, tSucc, y)
			return x
		}
	}

	entry, exit := eb.visitNode(ExKindIf, n)
	exit.Succs = exit.succs[:2]
	exit.Succs[0] = tSucc
	exit.Succs[1] = fSucc
	return entry
}
