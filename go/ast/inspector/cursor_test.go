// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package inspector_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"iter"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
)

func TestCursor_Preorder(t *testing.T) {
	inspect := netInspect

	nodeFilter := []ast.Node{(*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)}

	// reference implementation
	var want []ast.Node
	for cur := range inspect.Root().Preorder(nodeFilter...) {
		want = append(want, cur.Node())
	}

	// Check entire sequence.
	got := slices.Collect(inspect.PreorderSeq(nodeFilter...))
	compare(t, got, want)

	// Check that break works.
	got = got[:0]
	for _, c := range firstN(10, inspect.Root().Preorder(nodeFilter...)) {
		got = append(got, c.Node())
	}
	compare(t, got, want[:10])
}

func TestCursor_nestedTraversal(t *testing.T) {
	const src = `package a
func f() {
	print("hello")
}
func g() {
	print("goodbye")
	panic("oops")
}
`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "a.go", src, 0)
	inspect := inspector.New([]*ast.File{f})

	var (
		funcDecls = []ast.Node{(*ast.FuncDecl)(nil)}
		callExprs = []ast.Node{(*ast.CallExpr)(nil)}
		nfuncs    = 0
		ncalls    = 0
	)

	for curFunc := range inspect.Root().Preorder(funcDecls...) {
		_ = curFunc.Node().(*ast.FuncDecl)

		// Check edge and index.
		if k, idx := curFunc.ParentEdge(); k != edge.File_Decls || idx != nfuncs {
			t.Errorf("%v.ParentEdge() = (%v, %d),  want edge.File_Decls, %d", curFunc, k, idx, nfuncs)
		}

		nfuncs++
		stack := slices.Collect(curFunc.Enclosing())

		// Stacks are convenient to print!
		if got, want := fmt.Sprint(stack), "[*ast.FuncDecl *ast.File]"; got != want {
			t.Errorf("curFunc.Enclosing() = %q, want %q", got, want)
		}

		// Parent, iterated, is Enclosing stack.
		i := 0
		for c := curFunc; c.Node() != nil; c = c.Parent() {
			if got, want := stack[i], c; got != want {
				t.Errorf("Enclosing[%d] = %v; Parent()^%d = %v", i, got, i, want)
			}
			i++
		}

		wantStack := "[*ast.CallExpr *ast.ExprStmt *ast.BlockStmt *ast.FuncDecl *ast.File]"

		// nested Preorder traversal
		preorderCount := 0
		for curCall := range curFunc.Preorder(callExprs...) {
			_ = curCall.Node().(*ast.CallExpr)
			preorderCount++
			stack := slices.Collect(curCall.Enclosing())
			if got := fmt.Sprint(stack); got != wantStack {
				t.Errorf("curCall.Enclosing() = %q, want %q", got, wantStack)
			}
		}

		// nested Inspect traversal
		inspectCount := 0
		curFunc.Inspect(callExprs, func(curCall inspector.Cursor) (proceed bool) {
			_ = curCall.Node().(*ast.CallExpr)
			inspectCount++
			stack := slices.Collect(curCall.Enclosing())
			if got := fmt.Sprint(stack); got != wantStack {
				t.Errorf("curCall.Enclosing() = %q, want %q", got, wantStack)
			}
			return true
		})

		if inspectCount != preorderCount {
			t.Errorf("Inspect (%d) and Preorder (%d) events are not consistent", inspectCount, preorderCount)
		}

		ncalls += preorderCount
	}

	if nfuncs != 2 {
		t.Errorf("Found %d FuncDecls, want 2", nfuncs)
	}
	if ncalls != 3 {
		t.Errorf("Found %d CallExprs, want 3", ncalls)
	}
}

func TestCursor_Children(t *testing.T) {
	inspect := netInspect

	// Assert that Cursor.Children agrees with
	// reference implementation for every node.
	var want, got []ast.Node
	for c := range inspect.Root().Preorder() {

		// reference implementation
		want = want[:0]
		{
			parent := c.Node()
			ast.Inspect(parent, func(n ast.Node) bool {
				if n != nil && n != parent {
					want = append(want, n)
				}
				return n == parent // descend only into parent
			})
		}

		// Check cursor-based implementation
		// (uses FirstChild+NextSibling).
		got = got[:0]
		for child := range c.Children() {
			got = append(got, child.Node())
		}

		if !slices.Equal(got, want) {
			t.Errorf("For %v\n"+
				"Using FirstChild+NextSibling: %v\n"+
				"Using ast.Inspect:            %v",
				c, sliceTypes(got), sliceTypes(want))
		}

		// Second cursor-based implementation
		// using LastChild+PrevSibling+reverse.
		got = got[:0]
		for c, ok := c.LastChild(); ok; c, ok = c.PrevSibling() {
			got = append(got, c.Node())
		}
		slices.Reverse(got)

		if !slices.Equal(got, want) {
			t.Errorf("For %v\n"+
				"Using LastChild+PrevSibling: %v\n"+
				"Using ast.Inspect:           %v",
				c, sliceTypes(got), sliceTypes(want))
		}
	}
}

func TestCursor_Inspect(t *testing.T) {
	inspect := netInspect

	// In all three loops, we'll gather both kinds of type switches,
	// but we'll prune the traversal from descending into (value) switches.
	switches := []ast.Node{(*ast.SwitchStmt)(nil), (*ast.TypeSwitchStmt)(nil)}

	// reference implementation (ast.Inspect)
	var nodesA []ast.Node
	for _, f := range netFiles {
		ast.Inspect(f, func(n ast.Node) (proceed bool) {
			switch n.(type) {
			case *ast.SwitchStmt, *ast.TypeSwitchStmt:
				nodesA = append(nodesA, n)
				return !is[*ast.SwitchStmt](n) // descend only into TypeSwitchStmt
			}
			return true
		})
	}

	// Test Cursor.Inspect implementation.
	var nodesB []ast.Node
	inspect.Root().Inspect(switches, func(c inspector.Cursor) (proceed bool) {
		n := c.Node()
		nodesB = append(nodesB, n)
		return !is[*ast.SwitchStmt](n) // descend only into TypeSwitchStmt
		return false
	})
	compare(t, nodesA, nodesB)

	// Test WithStack implementation.
	var nodesC []ast.Node
	inspect.WithStack(switches, func(n ast.Node, push bool, stack []ast.Node) (proceed bool) {
		if push {
			nodesC = append(nodesC, n)
			return !is[*ast.SwitchStmt](n) // descend only into TypeSwitchStmt
		}
		return false
	})
	compare(t, nodesA, nodesC)
}

func TestCursor_FindNode(t *testing.T) {
	inspect := netInspect

	// Enumerate all nodes of a particular type,
	// then check that FindPos can find them,
	// starting at the root.
	//
	// (We use BasicLit because they are numerous.)
	root := inspect.Root()
	for c := range root.Preorder((*ast.BasicLit)(nil)) {
		node := c.Node()
		got, ok := root.FindNode(node)
		if !ok {
			t.Errorf("root.FindNode failed")
		} else if got != c {
			t.Errorf("root.FindNode returned %v, want %v", got, c)
		}
	}

	// Same thing, but searching only within subtrees (each FuncDecl).
	for funcDecl := range root.Preorder((*ast.FuncDecl)(nil)) {
		for c := range funcDecl.Preorder((*ast.BasicLit)(nil)) {
			node := c.Node()
			got, ok := funcDecl.FindNode(node)
			if !ok {
				t.Errorf("funcDecl.FindNode failed")
			} else if got != c {
				t.Errorf("funcDecl.FindNode returned %v, want %v", got, c)
			}

			// Also, check that we cannot find the BasicLit
			// beneath a different FuncDecl.
			if prevFunc, ok := funcDecl.PrevSibling(); ok {
				got, ok := prevFunc.FindNode(node)
				if ok {
					t.Errorf("prevFunc.FindNode succeeded unexpectedly: %v", got)
				}
			}
		}
	}
}

// TestCursor_FindPos_order ensures that FindPos does not assume files are in Pos order.
func TestCursor_FindPos_order(t *testing.T) {
	// Pick an arbitrary decl.
	target := netFiles[7].Decls[0]

	// Find the target decl by its position.
	cur, ok := netInspect.Root().FindByPos(target.Pos(), target.End())
	if !ok || cur.Node() != target {
		t.Fatalf("unshuffled: FindPos(%T) = (%v, %t)", target, cur, ok)
	}

	// Shuffle the files out of Pos order.
	files := slices.Clone(netFiles)
	rand.Shuffle(len(files), func(i, j int) {
		files[i], files[j] = files[j], files[i]
	})

	// Find it again.
	inspect := inspector.New(files)
	cur, ok = inspect.Root().FindByPos(target.Pos(), target.End())
	if !ok || cur.Node() != target {
		t.Fatalf("shuffled: FindPos(%T) = (%v, %t)", target, cur, ok)
	}
}

func TestCursor_Edge(t *testing.T) {
	root := netInspect.Root()
	for cur := range root.Preorder() {
		if cur == root {
			continue // root node
		}

		var (
			parent = cur.Parent()
			e, idx = cur.ParentEdge()
		)

		// ast.File, child of root?
		if parent.Node() == nil {
			if e != edge.Invalid || idx != -1 {
				t.Errorf("%v.Edge = (%v, %d), want (Invalid, -1)", cur, e, idx)
			}
			continue
		}

		// Check Edge.NodeType matches type of Parent.Node.
		if e.NodeType() != reflect.TypeOf(parent.Node()) {
			t.Errorf("Edge.NodeType = %v, Parent.Node has type %T",
				e.NodeType(), parent.Node())
		}

		// Check c.Edge.Get(c.Parent.Node) == c.Node.
		if got := e.Get(parent.Node(), idx); got != cur.Node() {
			t.Errorf("cur=%v@%s: %s.Get(cur.Parent().Node(), %d) = %T@%s, want cur.Node()",
				cur, netFset.Position(cur.Node().Pos()), e, idx, got, netFset.Position(got.Pos()))
		}

		// Check c.Parent.ChildAt(c.ParentEdge()) == c.
		if got := parent.ChildAt(e, idx); got != cur {
			t.Errorf("cur=%v@%s: cur.Parent().ChildAt(%v, %d) = %T@%s, want cur",
				cur, netFset.Position(cur.Node().Pos()), e, idx, got.Node(), netFset.Position(got.Node().Pos()))
		}

		// Check that reflection on the parent finds the current node.
		fv := reflect.ValueOf(parent.Node()).Elem().FieldByName(e.FieldName())
		if idx >= 0 {
			fv = fv.Index(idx) // element of []ast.Node
		}
		if fv.Kind() == reflect.Interface {
			fv = fv.Elem() // e.g. ast.Expr -> *ast.Ident
		}
		got := fv.Interface().(ast.Node)
		if got != cur.Node() {
			t.Errorf("%v.Edge = (%v, %d); FieldName/Index reflection gave %T@%s, not original node",
				cur, e, idx, got, netFset.Position(got.Pos()))
		}

		// Check that Cursor.Child is the reverse of Parent.
		if cur.Parent().Child(cur.Node()) != cur {
			t.Errorf("Cursor.Parent.Child = %v, want %v", cur.Parent().Child(cur.Node()), cur)
		}

		// Check invariants of Contains:

		// A cursor contains itself.
		if !cur.Contains(cur) {
			t.Errorf("!cur.Contains(cur): %v", cur)
		}
		// A parent contains its child, but not the inverse.
		if !parent.Contains(cur) {
			t.Errorf("!cur.Parent().Contains(cur): %v", cur)
		}
		if cur.Contains(parent) {
			t.Errorf("cur.Contains(cur.Parent()): %v", cur)
		}
		// A grandparent contains its grandchild, but not the inverse.
		if grandparent := cur.Parent(); grandparent.Node() != nil {
			if !grandparent.Contains(cur) {
				t.Errorf("!cur.Parent().Parent().Contains(cur): %v", cur)
			}
			if cur.Contains(grandparent) {
				t.Errorf("cur.Contains(cur.Parent().Parent()): %v", cur)
			}
		}
		// A cursor and its uncle/aunt do not contain each other.
		if uncle, ok := parent.NextSibling(); ok {
			if uncle.Contains(cur) {
				t.Errorf("cur.Parent().NextSibling().Contains(cur): %v", cur)
			}
			if cur.Contains(uncle) {
				t.Errorf("cur.Contains(cur.Parent().NextSibling()): %v", cur)
			}
		}
	}
}

func is[T any](x any) bool {
	_, ok := x.(T)
	return ok
}

// sliceTypes is a debugging helper that formats each slice element with %T.
func sliceTypes[T any](slice []T) string {
	var buf strings.Builder
	buf.WriteByte('[')
	for i, elem := range slice {
		if i > 0 {
			buf.WriteByte(' ')
		}
		fmt.Fprintf(&buf, "%T", elem)
	}
	buf.WriteByte(']')
	return buf.String()
}

func BenchmarkInspectCalls(b *testing.B) {
	inspect := netInspect

	// Measure marginal cost of traversal.

	callExprs := []ast.Node{(*ast.CallExpr)(nil)}

	b.Run("Preorder", func(b *testing.B) {
		var ncalls int
		for range b.N {
			inspect.Preorder(callExprs, func(n ast.Node) {
				_ = n.(*ast.CallExpr)
				ncalls++
			})
		}
	})

	b.Run("WithStack", func(b *testing.B) {
		var ncalls int
		for range b.N {
			inspect.WithStack(callExprs, func(n ast.Node, push bool, stack []ast.Node) (proceed bool) {
				_ = n.(*ast.CallExpr)
				if push {
					ncalls++
				}
				return true
			})
		}
	})

	b.Run("Cursor", func(b *testing.B) {
		var ncalls int
		for range b.N {
			for cur := range inspect.Root().Preorder(callExprs...) {
				_ = cur.Node().(*ast.CallExpr)
				ncalls++
			}
		}
	})

	b.Run("CursorEnclosing", func(b *testing.B) {
		var ncalls int
		for range b.N {
			for cur := range inspect.Root().Preorder(callExprs...) {
				_ = cur.Node().(*ast.CallExpr)
				for range cur.Enclosing() {
				}
				ncalls++
			}
		}
	})
}

// This benchmark compares methods for finding a known node in a tree.
func BenchmarkCursor_FindNode(b *testing.B) {
	root := netInspect.Root()

	callExprs := []ast.Node{(*ast.CallExpr)(nil)}

	// Choose a needle in the haystack to use as the search target:
	// a CallExpr not too near the start nor at too shallow a depth.
	var needle inspector.Cursor
	{
		count := 0
		found := false
		for c := range root.Preorder(callExprs...) {
			count++
			if count >= 1000 && iterlen(c.Enclosing()) >= 6 {
				needle = c
				found = true
				break
			}
		}
		if !found {
			b.Fatal("can't choose needle")
		}
	}

	b.ResetTimer()

	b.Run("Cursor.Preorder", func(b *testing.B) {
		needleNode := needle.Node()
		for range b.N {
			var found inspector.Cursor
			for c := range root.Preorder(callExprs...) {
				if c.Node() == needleNode {
					found = c
					break
				}
			}
			if found != needle {
				b.Errorf("Preorder search failed: got %v, want %v", found, needle)
			}
		}
	})

	// This method is about 10-15% faster than Cursor.Preorder.
	b.Run("Cursor.FindNode", func(b *testing.B) {
		for range b.N {
			found, ok := root.FindNode(needle.Node())
			if !ok || found != needle {
				b.Errorf("FindNode search failed: got %v, want %v", found, needle)
			}
		}
	})

	// This method is about 100x (!) faster than Cursor.Preorder.
	b.Run("Cursor.FindPos", func(b *testing.B) {
		needleNode := needle.Node()
		for range b.N {
			found, ok := root.FindByPos(needleNode.Pos(), needleNode.End())
			if !ok || found != needle {
				b.Errorf("FindPos search failed: got %v, want %v", found, needle)
			}
		}
	})
}

func iterlen[T any](seq iter.Seq[T]) (len int) {
	for range seq {
		len++
	}
	return
}
