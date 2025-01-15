// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build go1.23

package cursor_test

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"iter"
	"log"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/internal/astutil/cursor"
	"golang.org/x/tools/internal/astutil/edge"
)

// net/http package
var (
	netFset    = token.NewFileSet()
	netFiles   []*ast.File
	netInspect *inspector.Inspector
)

func init() {
	files, err := parseNetFiles()
	if err != nil {
		log.Fatal(err)
	}
	netFiles = files
	netInspect = inspector.New(netFiles)
}

func parseNetFiles() ([]*ast.File, error) {
	pkg, err := build.Default.Import("net", "", 0)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for _, filename := range pkg.GoFiles {
		filename = filepath.Join(pkg.Dir, filename)
		f, err := parser.ParseFile(netFset, filename, nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// compare calls t.Error if !slices.Equal(nodesA, nodesB).
func compare[N comparable](t *testing.T, nodesA, nodesB []N) {
	if len(nodesA) != len(nodesB) {
		t.Errorf("inconsistent node lists: %d vs %d", len(nodesA), len(nodesB))
	} else {
		for i := range nodesA {
			if a, b := nodesA[i], nodesB[i]; a != b {
				t.Errorf("node %d is inconsistent: %T, %T", i, a, b)
			}
		}
	}
}

// firstN(n, seq), returns a slice of up to n elements of seq.
func firstN[T any](n int, seq iter.Seq[T]) (res []T) {
	for x := range seq {
		res = append(res, x)
		if len(res) == n {
			break
		}
	}
	return res
}

func TestCursor_Preorder(t *testing.T) {
	inspect := netInspect

	nodeFilter := []ast.Node{(*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)}

	// reference implementation
	var want []ast.Node
	for cur := range cursor.Root(inspect).Preorder(nodeFilter...) {
		want = append(want, cur.Node())
	}

	// Check entire sequence.
	got := slices.Collect(inspect.PreorderSeq(nodeFilter...))
	compare(t, got, want)

	// Check that break works.
	got = got[:0]
	for _, c := range firstN(10, cursor.Root(inspect).Preorder(nodeFilter...)) {
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

	for curFunc := range cursor.Root(inspect).Preorder(funcDecls...) {
		_ = curFunc.Node().(*ast.FuncDecl)

		// Check edge and index.
		if e, idx := curFunc.Edge(); e != edge.File_Decls || idx != nfuncs {
			t.Errorf("%v.Edge() = (%v, %v),  want edge.File_Decls, %d",
				curFunc, e, idx, nfuncs)
		}

		nfuncs++
		stack := curFunc.Stack(nil)

		// Stacks are convenient to print!
		if got, want := fmt.Sprint(stack), "[*ast.File *ast.FuncDecl]"; got != want {
			t.Errorf("curFunc.Stack() = %q, want %q", got, want)
		}

		// Parent, iterated, is Stack.
		i := 0
		for c := curFunc; c.Node() != nil; c = c.Parent() {
			if got, want := stack[len(stack)-1-i], c; got != want {
				t.Errorf("Stack[%d] = %v; Parent()^%d = %v", i, got, i, want)
			}
			i++
		}

		// nested Preorder traversal
		preorderCount := 0
		for curCall := range curFunc.Preorder(callExprs...) {
			_ = curCall.Node().(*ast.CallExpr)
			preorderCount++
			stack := curCall.Stack(nil)
			if got, want := fmt.Sprint(stack), "[*ast.File *ast.FuncDecl *ast.BlockStmt *ast.ExprStmt *ast.CallExpr]"; got != want {
				t.Errorf("curCall.Stack() = %q, want %q", got, want)
			}

			// Ancestors = Reverse(Stack[:last]).
			stack = stack[:len(stack)-1]
			slices.Reverse(stack)
			if got, want := slices.Collect(curCall.Ancestors()), stack; !reflect.DeepEqual(got, want) {
				t.Errorf("Ancestors = %v, Reverse(Stack - last element) = %v", got, want)
			}
		}

		// nested Inspect traversal
		inspectCount := 0 // pushes and pops
		curFunc.Inspect(callExprs, func(curCall cursor.Cursor, push bool) (proceed bool) {
			_ = curCall.Node().(*ast.CallExpr)
			inspectCount++
			stack := curCall.Stack(nil)
			if got, want := fmt.Sprint(stack), "[*ast.File *ast.FuncDecl *ast.BlockStmt *ast.ExprStmt *ast.CallExpr]"; got != want {
				t.Errorf("curCall.Stack() = %q, want %q", got, want)
			}
			return true
		})

		if inspectCount != preorderCount*2 {
			t.Errorf("Inspect (%d push/pop events) and Preorder (%d push events) are not consistent", inspectCount, preorderCount)
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
	for c := range cursor.Root(inspect).Preorder() {

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
	cursor.Root(inspect).Inspect(switches, func(c cursor.Cursor, push bool) (proceed bool) {
		if push {
			n := c.Node()
			nodesB = append(nodesB, n)
			return !is[*ast.SwitchStmt](n) // descend only into TypeSwitchStmt
		}
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
	root := cursor.Root(inspect)
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

	// TODO(adonovan): FindPos needs a test (not just a benchmark).
}

func TestCursor_Edge(t *testing.T) {
	root := cursor.Root(netInspect)
	for cur := range root.Preorder() {
		if cur == root {
			continue // root node
		}

		e, idx := cur.Edge()
		parent := cur.Parent()

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

		// Check consistency of c.Edge.Get(c.Parent().Node()) == c.Node().
		if got := e.Get(parent.Node(), idx); got != cur.Node() {
			t.Errorf("cur=%v@%s: %s.Get(cur.Parent().Node(), %d) = %T@%s, want cur.Node()",
				cur, netFset.Position(cur.Node().Pos()), e, idx, got, netFset.Position(got.Pos()))
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

// (partially duplicates benchmark in go/ast/inspector)
func BenchmarkInspectCalls(b *testing.B) {
	inspect := netInspect
	b.ResetTimer()

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

	// Cursor.Stack(nil) is ~6x slower than WithStack.
	// Even using Cursor.Stack(stack[:0]) to amortize the
	// allocation, it's ~4x slower.
	//
	// But it depends on the selectivity of the nodeTypes
	// filter: searching for *ast.InterfaceType, results in
	// fewer calls to Stack, making it only 2x slower.
	// And if the calls to Stack are very selective,
	// or are replaced by 2 calls to Parent, it runs
	// 27% faster than WithStack.
	//
	// But the purpose of inspect.WithStack is not to obtain the
	// stack on every node, but to perform a traversal in which it
	// one as the _option_ to access the stack if it should be
	// needed, but the need is rare and usually only for a small
	// portion. Arguably, because Cursor traversals always
	// provide, at no extra cost, the option to access the
	// complete stack, the right comparison is the plain Cursor
	// benchmark below.
	b.Run("CursorStack", func(b *testing.B) {
		var ncalls int
		for range b.N {
			var stack []cursor.Cursor // recycle across calls
			for cur := range cursor.Root(inspect).Preorder(callExprs...) {
				_ = cur.Node().(*ast.CallExpr)
				stack = cur.Stack(stack[:0])
				ncalls++
			}
		}
	})

	b.Run("Cursor", func(b *testing.B) {
		var ncalls int
		for range b.N {
			for cur := range cursor.Root(inspect).Preorder(callExprs...) {
				_ = cur.Node().(*ast.CallExpr)
				ncalls++
			}
		}
	})

	b.Run("CursorAncestors", func(b *testing.B) {
		var ncalls int
		for range b.N {
			for cur := range cursor.Root(inspect).Preorder(callExprs...) {
				_ = cur.Node().(*ast.CallExpr)
				for range cur.Ancestors() {
				}
				ncalls++
			}
		}
	})
}

// This benchmark compares methods for finding a known node in a tree.
func BenchmarkCursor_FindNode(b *testing.B) {
	root := cursor.Root(netInspect)

	callExprs := []ast.Node{(*ast.CallExpr)(nil)}

	// Choose a needle in the haystack to use as the search target:
	// a CallExpr not too near the start nor at too shallow a depth.
	var needle cursor.Cursor
	{
		count := 0
		found := false
		for c := range root.Preorder(callExprs...) {
			count++
			if count >= 1000 && len(c.Stack(nil)) >= 6 {
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
			var found cursor.Cursor
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
			found, ok := root.FindPos(needleNode.Pos(), needleNode.End())
			if !ok || found != needle {
				b.Errorf("FindPos search failed: got %v, want %v", found, needle)
			}
		}
	})
}
