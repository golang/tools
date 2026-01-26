// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/fmtstr"
	"golang.org/x/tools/internal/moreiters"
)

func Highlight(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, rng protocol.Range) ([]protocol.DocumentHighlight, error) {
	ctx, done := event.Start(ctx, "golang.Highlight")
	defer done()

	// We always want fully parsed files for highlight, regardless
	// of whether the file belongs to a workspace package.
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, fmt.Errorf("getting package for Highlight: %w", err)
	}

	start, end, err := pgf.RangePos(rng)
	if err != nil {
		return nil, err
	}

	cur, _, _, _ := astutil.Select(pgf.Cursor(), start, end) // can't fail: pgf contains pos

	result, err := highlightPath(pkg.TypesInfo(), cur, start, end)
	if err != nil {
		return nil, err
	}
	var ranges []protocol.DocumentHighlight
	for rng, kind := range result {
		rng, err := pgf.NodeRange(rng)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, protocol.DocumentHighlight{
			Range: rng,
			Kind:  kind,
		})
	}
	return ranges, nil
}

// highlightPath returns ranges to highlight for the given the cursor.
func highlightPath(info *types.Info, cur inspector.Cursor, start, end token.Pos) (map[astutil.Range]protocol.DocumentHighlightKind, error) {
	result := make(map[astutil.Range]protocol.DocumentHighlightKind)

	// Inside a call to a printf-like function (as identified
	// by a simple heuristic).
	// Treat each corresponding ("%v", arg) pair as a highlight class.
	for node := range cur.Enclosing((*ast.CallExpr)(nil)) {
		call := node.Node().(*ast.CallExpr)
		lit, idx := formatStringAndIndex(info, call)
		if idx != -1 {
			highlightPrintf(call, idx, start, end, lit, result)
		}
	}

	switch node := cur.Node().(type) {
	case *ast.BasicLit:
		// Import path string literal?
		if imp, ok := cur.Parent().Node().(*ast.ImportSpec); ok {
			highlight := func(n ast.Node) {
				highlightNode(result, n, protocol.Text)
			}

			// Highlight the import itself...
			highlight(imp)

			// ...and all references to it in the file.
			if pkgname := info.PkgNameOf(imp); pkgname != nil {
				_, curFile := cursorutil.FirstEnclosing[*ast.File](cur)
				for c := range curFile.Preorder((*ast.Ident)(nil)) {
					if id := c.Node().(*ast.Ident); info.Uses[id] == pkgname {
						highlight(id)
					}
				}
			}
			return result, nil
		}
		highlightFuncControlFlow(cur, result)
	case *ast.ReturnStmt, *ast.FuncDecl, *ast.FuncType:
		highlightFuncControlFlow(cur, result)
	case *ast.Ident:
		// Check if ident is inside return or func decl.
		highlightFuncControlFlow(cur, result)
		highlightIdentifier(cur, info, result)
	case *ast.ForStmt, *ast.RangeStmt:
		var label *ast.Ident
		if l, ok := cur.Parent().Node().(*ast.LabeledStmt); ok {
			label = l.Label
		}
		highlightLoopControlFlow(node, label, info, result)
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
		var label *ast.Ident
		if l, ok := cur.Parent().Node().(*ast.LabeledStmt); ok {
			label = l.Label
		}
		highlightSwitchFlow(node, label, info, result)
	case *ast.BranchStmt:
		// BREAK can exit a loop, switch or select, while CONTINUE exit a loop so
		// these need to be handled separately. They can also be embedded in any
		// other loop/switch/select if they have a label. TODO: add support for
		// GOTO and FALLTHROUGH as well.
		switch node.Tok {
		case token.BREAK:
			if node.Label != nil {
				highlightLabeledFlow(cur, info, result)
			} else {
				highlightUnlabeledBreakFlow(cur, info, result)
			}
		case token.CONTINUE:
			if node.Label != nil {
				highlightLabeledFlow(cur, info, result)
			} else {
				var (
					stmt  ast.Node
					label *ast.Ident
				)
				if curLoop, ok := moreiters.First(cur.Enclosing((*ast.ForStmt)(nil), (*ast.RangeStmt)(nil))); ok {
					stmt = curLoop.Node()
					if l, ok := curLoop.Parent().Node().(*ast.LabeledStmt); ok {
						label = l.Label
					}
				}
				highlightLoopControlFlow(stmt, label, info, result)
			}
		}
	}

	return result, nil
}

// formatStringAndIndex returns the BasicLit and index of the BasicLit (the last
// non-variadic parameter) within the given printf-like call
// expression, returns -1 as index if unknown.
func formatStringAndIndex(info *types.Info, call *ast.CallExpr) (*ast.BasicLit, int) {
	typ := info.Types[call.Fun].Type
	if typ == nil {
		return nil, -1 // missing type
	}
	sig, ok := typ.(*types.Signature)
	if !ok {
		return nil, -1 // ill-typed
	}
	if !sig.Variadic() {
		// Skip checking non-variadic functions.
		return nil, -1
	}
	idx := sig.Params().Len() - 2
	if !(0 <= idx && idx < len(call.Args)) {
		// Skip checking functions without a format string parameter, or
		// missing the corresponding format argument.
		return nil, -1
	}
	// We only care about literal format strings, so fmt.Sprint("a"+"b%s", "bar") won't be highlighted.
	if lit, ok := call.Args[idx].(*ast.BasicLit); ok && lit.Kind == token.STRING {
		return lit, idx
	}
	return nil, -1
}

// highlightPrintf highlights operations in a format string and their
// corresponding variadic arguments in a (possible) printf-style function call.
// For example:
//
// fmt.Printf("Hello %s, you scored %d", name, score)
//
// If the cursor is on %s or name, it will highlight %s as a write operation,
// and name as a read operation.
func highlightPrintf(call *ast.CallExpr, idx int, start, end token.Pos, lit *ast.BasicLit, result map[astutil.Range]protocol.DocumentHighlightKind) {
	format, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}
	if !strings.Contains(format, "%") {
		return
	}
	operations, err := fmtstr.Parse(format, idx)
	if err != nil {
		return
	}

	// fmt.Printf("%[1]d %[1].2d", 3)
	//
	// When cursor is in `%[1]d`, we record `3` being successfully highlighted.
	// And because we will also record `%[1].2d`'s corresponding arguments index is `3`
	// in `visited`, even though it will not highlight any item in the first pass,
	// in the second pass we can correctly highlight it. So the three are the same class.
	succeededArg := 0
	visited := make(map[astutil.Range]int, 0)

	// highlightPair highlights the operation and its potential argument pair if the cursor is within either range.
	highlightPair := func(rang fmtstr.Range, argIndex int) {
		var (
			rng       astutil.Range
			withinRng bool
		)
		rng, err = astutil.RangeInStringLiteral(lit, rang.Start, rang.End)
		if err != nil {
			return
		}
		visited[rng] = argIndex

		if start == end {
			// End pos can't equal to range's end, otherwise the two neighborhood
			// such as (%[2]*d) are both highlighted if cursor in "d" (ending of [2]*).
			withinRng = rng.Start <= start && end < rng.End()
		} else {
			// With a non-empty selection, there is no ambiguity.
			withinRng = rng.Contains(astutil.RangeOf(start, end))
		}

		var (
			arg       ast.Expr
			withinArg bool
		)
		if argIndex < len(call.Args) {
			arg = call.Args[argIndex]
			withinArg = astutil.NodeContains(arg, astutil.RangeOf(start, end))
		}

		if withinRng || withinArg {
			highlightRange(result, rng, protocol.Write)
			if arg != nil {
				succeededArg = argIndex
				highlightNode(result, arg, protocol.Read)
			}
		}
	}

	for _, op := range operations {
		// If width or prec has any *, we can not highlight the full range from % to verb,
		// because it will overlap with the sub-range of *, for example:
		//
		// fmt.Printf("%*[3]d", 4, 5, 6)
		//               ^  ^ we can only highlight this range when cursor in 6. '*' as a one-rune range will
		//               highlight for 4.
		hasAsterisk := false

		// Try highlight Width if there is a *.
		if op.Width.Dynamic != -1 {
			hasAsterisk = true
			highlightPair(op.Width.Range, op.Width.Dynamic)
		}

		// Try highlight Precision if there is a *.
		if op.Prec.Dynamic != -1 {
			hasAsterisk = true
			highlightPair(op.Prec.Range, op.Prec.Dynamic)
		}

		// Try highlight Verb.
		if op.Verb.Verb != '%' {
			// If any * is found inside operation, narrow the highlight range.
			if hasAsterisk {
				highlightPair(op.Verb.Range, op.Verb.ArgIndex)
			} else {
				highlightPair(op.Range, op.Verb.ArgIndex)
			}
		}
	}

	// Second pass, try to highlight those missed operations.
	for rng, argIndex := range visited {
		if succeededArg == argIndex {
			highlightRange(result, rng, protocol.Write)
		}
	}
}

// highlightFuncControlFlow adds highlight ranges to the result map to
// associate results and result parameters.
//
// Specifically, if the cursor is in a result or result parameter, all
// results and result parameters with the same index are highlighted. If the
// cursor is in a 'func' or 'return' keyword, the func keyword as well as all
// returns from that func are highlighted.
//
// As a special case, if the cursor is within a complicated expression, control
// flow highlighting is disabled, as it would highlight too much.
func highlightFuncControlFlow(cur inspector.Cursor, result map[astutil.Range]protocol.DocumentHighlightKind) {

	var (
		funcType   *ast.FuncType   // type of enclosing func, or nil
		funcBody   *ast.BlockStmt  // body of enclosing func, or nil
		returnStmt *ast.ReturnStmt // enclosing ReturnStmt within the func, or nil
	)

loop:
	for cur := range cur.Enclosing() {
		switch n := cur.Node().(type) {
		// TODO(rfindley, low priority): these pre-existing cases for KeyValueExpr
		// and CallExpr appear to avoid highlighting when the cursor is in a
		// complicated expression. However, the basis for this heuristic is
		// unclear. Can we formalize a rationale?
		case *ast.KeyValueExpr:
			// If cursor is in a key: value expr, we don't want control flow highlighting.
			return

		case *ast.FuncLit:
			funcType = n.Type
			funcBody = n.Body
			break loop

		case *ast.FuncDecl:
			funcType = n.Type
			funcBody = n.Body
			break loop

		case *ast.ReturnStmt:
			returnStmt = n
		}

		// If cursor is an arg in a callExpr, we don't want control flow highlighting.
		if astutil.IsChildOf(cur, edge.CallExpr_Args) {
			return
		}
	}

	if funcType == nil {
		return // cursor is not in a function
	}

	// Helper functions for inspecting the current location.
	var (
		pos    = cur.Node().Pos()
		inSpan = func(start, end token.Pos) bool { return start <= pos && pos < end }
		inNode = func(n ast.Node) bool { return inSpan(n.Pos(), n.End()) }
	)

	inResults := funcType.Results != nil && inNode(funcType.Results)

	// If the cursor is on a "return" or "func" keyword, but not highlighting any
	// specific field or expression, we should highlight all of the exit points
	// of the function, including the "return" and "func" keywords.
	funcEnd := funcType.Func + token.Pos(len("func"))
	highlightAll := cur.Node() == returnStmt || inSpan(funcType.Func, funcEnd)
	var highlightIndexes map[int]bool

	if highlightAll {
		// Add the "func" part of the func declaration.
		highlightRange(result, astutil.RangeOf(funcType.Func, funcEnd), protocol.Text)
	} else if returnStmt == nil && !inResults {
		return // nothing to highlight
	} else {
		// If we're not highlighting the entire return statement, we need to collect
		// specific result indexes to highlight. This may be more than one index if
		// the cursor is on a multi-name result field, but not in any specific name.
		if !highlightAll {
			highlightIndexes = make(map[int]bool)
			if returnStmt != nil {
				for i, n := range returnStmt.Results {
					if inNode(n) {
						highlightIndexes[i] = true
						break
					}
				}
			}

			if funcType.Results != nil {
				// Scan fields, either adding highlights according to the highlightIndexes
				// computed above, or accounting for the cursor position within the result
				// list.
				// (We do both at once to avoid repeating the cumbersome field traversal.)
				i := 0
			findField:
				for _, field := range funcType.Results.List {
					for j, name := range field.Names {
						if inNode(name) || highlightIndexes[i+j] {
							highlightNode(result, name, protocol.Text)
							highlightIndexes[i+j] = true
							break findField // found/highlighted the specific name
						}
					}
					// If the cursor is in a field but not in a name (e.g. in the space, or
					// the type), highlight the whole field.
					//
					// Note that this may not be ideal if we're at e.g.
					//
					//  (x,‸y int, z int8)
					//
					// ...where it would make more sense to highlight only y. But we don't
					// reach this function if not in a func, return, ident, or basiclit.
					if inNode(field) || highlightIndexes[i] {
						highlightNode(result, field, protocol.Text)
						highlightIndexes[i] = true
						if inNode(field) {
							for j := range field.Names {
								highlightIndexes[i+j] = true
							}
						}
						break findField // found/highlighted the field
					}

					n := len(field.Names)
					if n == 0 {
						n = 1
					}
					i += n
				}
			}
		}
	}

	if funcBody != nil {
		ast.Inspect(funcBody, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl, *ast.FuncLit:
				// Don't traverse into any functions other than enclosingFunc.
				return false
			case *ast.ReturnStmt:
				if highlightAll {
					// Add the entire return statement.
					highlightNode(result, n, protocol.Text)
				} else {
					// Add the highlighted indexes.
					for i, expr := range n.Results {
						if highlightIndexes[i] {
							highlightNode(result, expr, protocol.Text)
						}
					}
				}
				return false

			}
			return true
		})
	}
}

// highlightUnlabeledBreakFlow highlights the innermost enclosing
// for/range/switch or select
func highlightUnlabeledBreakFlow(cur inspector.Cursor, info *types.Info, result map[astutil.Range]protocol.DocumentHighlightKind) {
	// Reverse walk the path until we find closest loop, select, or switch.
	for cur := range cur.Enclosing() {
		switch n := cur.Node().(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			var label *ast.Ident
			if l, ok := cur.Parent().Node().(*ast.LabeledStmt); ok {
				label = l.Label
			}
			highlightLoopControlFlow(n, label, info, result)
			return // only highlight the innermost statement
		case *ast.SwitchStmt, *ast.TypeSwitchStmt:
			var label *ast.Ident
			if l, ok := cur.Parent().Node().(*ast.LabeledStmt); ok {
				label = l.Label
			}
			highlightSwitchFlow(n, label, info, result)
			return
		case *ast.SelectStmt:
			// TODO: add highlight when breaking a select.
			return
		}
	}
}

// highlightLabeledFlow highlights the enclosing labeled for, range,
// or switch statement denoted by a labeled break or continue stmt.
//
// The input cursor must point to a [ast.BranchStmt]
func highlightLabeledFlow(curBranch inspector.Cursor, info *types.Info, result map[astutil.Range]protocol.DocumentHighlightKind) {
	use := info.Uses[curBranch.Node().(*ast.BranchStmt).Label]
	if use == nil {
		return
	}
	for cur := range curBranch.Enclosing((*ast.LabeledStmt)(nil)) {
		if label := cur.Node().(*ast.LabeledStmt); info.Defs[label.Label] == use {
			switch label.Stmt.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				highlightLoopControlFlow(label.Stmt, label.Label, info, result)
			case *ast.SwitchStmt, *ast.TypeSwitchStmt:
				highlightSwitchFlow(label.Stmt, label.Label, info, result)
			}
			return
		}
	}
}

func highlightLoopControlFlow(loop ast.Node, label *ast.Ident, info *types.Info, result map[astutil.Range]protocol.DocumentHighlightKind) {
	// Add the for statement.
	rngStart := loop.Pos()
	rngEnd := loop.Pos() + token.Pos(len("for"))
	highlightRange(result, astutil.RangeOf(rngStart, rngEnd), protocol.Text)

	// Traverse AST to find branch statements within the same for-loop.
	ast.Inspect(loop, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return loop == n
		case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			return false
		}
		b, ok := n.(*ast.BranchStmt)
		if !ok {
			return true
		}
		if b.Label == nil || info.Uses[b.Label] == info.Defs[label] {
			highlightNode(result, b, protocol.Text)
		}
		return true
	})

	// Find continue statements in the same loop or switches/selects.
	ast.Inspect(loop, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return loop == n
		}

		if n, ok := n.(*ast.BranchStmt); ok && n.Tok == token.CONTINUE {
			highlightNode(result, n, protocol.Text)
		}
		return true
	})

	// We don't need to check other for loops if we aren't looking for labeled statements.
	if label == nil {
		return
	}

	// Find labeled branch statements in any loop.
	ast.Inspect(loop, func(n ast.Node) bool {
		b, ok := n.(*ast.BranchStmt)
		if !ok {
			return true
		}
		// statement with labels that matches the loop
		if b.Label != nil && info.Uses[b.Label] == info.Defs[label] {
			highlightNode(result, b, protocol.Text)
		}
		return true
	})
}

func highlightSwitchFlow(node ast.Node, label *ast.Ident, info *types.Info, result map[astutil.Range]protocol.DocumentHighlightKind) {
	// Add the switch statement.
	rngStart := node.Pos()
	rngEnd := node.Pos() + token.Pos(len("switch"))
	highlightRange(result, astutil.RangeOf(rngStart, rngEnd), protocol.Text)

	// Traverse AST to find break statements within the same switch.
	ast.Inspect(node, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.SwitchStmt, *ast.TypeSwitchStmt:
			return node == n
		case *ast.ForStmt, *ast.RangeStmt, *ast.SelectStmt:
			return false
		}

		b, ok := n.(*ast.BranchStmt)
		if !ok || b.Tok != token.BREAK {
			return true
		}

		if b.Label == nil || info.Uses[b.Label] == info.Defs[label] {
			highlightNode(result, b, protocol.Text)
		}
		return true
	})

	// We don't need to check other switches if we aren't looking for labeled statements.
	if label == nil {
		return
	}

	// Find labeled break statements in any switch
	ast.Inspect(node, func(n ast.Node) bool {
		b, ok := n.(*ast.BranchStmt)
		if !ok || b.Tok != token.BREAK {
			return true
		}

		if b.Label != nil && info.Uses[b.Label] == info.Defs[label] {
			highlightNode(result, b, protocol.Text)
		}

		return true
	})
}

func highlightNode(result map[astutil.Range]protocol.DocumentHighlightKind, n ast.Node, kind protocol.DocumentHighlightKind) {
	highlightRange(result, astutil.NodeRange(n), kind)
}

func highlightRange(result map[astutil.Range]protocol.DocumentHighlightKind, rng astutil.Range, kind protocol.DocumentHighlightKind) {
	// Order of traversal is important: some nodes (e.g. identifiers) are
	// visited more than once, but the kind set during the first visitation "wins".
	if _, exists := result[rng]; !exists {
		result[rng] = kind
	}
}

func highlightIdentifier(cur inspector.Cursor, info *types.Info, result map[astutil.Range]protocol.DocumentHighlightKind) {
	ident := cur.Node().(*ast.Ident)

	// obj may be nil if the Ident is undefined.
	// In this case, the behavior expected by tests is
	// to match other undefined Idents of the same name.
	obj := info.ObjectOf(ident)

	highlightIdent := func(n *ast.Ident, kind protocol.DocumentHighlightKind) {
		if n.Name == ident.Name && info.ObjectOf(n) == obj {
			highlightNode(result, n, kind)
		}
	}
	// highlightWriteInExpr is called for expressions that are
	// logically on the left side of an assignment.
	// We follow the behavior of VSCode+Rust and GoLand, which differs
	// slightly from types.TypeAndValue.Assignable:
	//     *ptr = 1       // ptr write
	//     *ptr.field = 1 // ptr read, field write
	//     s.field = 1    // s read, field write
	//     array[i] = 1   // array read
	var highlightWriteInExpr func(expr ast.Expr)
	highlightWriteInExpr = func(expr ast.Expr) {
		switch expr := expr.(type) {
		case *ast.Ident:
			highlightIdent(expr, protocol.Write)
		case *ast.SelectorExpr:
			highlightIdent(expr.Sel, protocol.Write)
		case *ast.StarExpr:
			highlightWriteInExpr(expr.X)
		case *ast.ParenExpr:
			highlightWriteInExpr(expr.X)
		}
	}

	_, curFile := cursorutil.FirstEnclosing[*ast.File](cur)
	curFile.Inspect(nil, func(c inspector.Cursor) bool {
		switch n := c.Node().(type) {
		case *ast.AssignStmt:
			for _, s := range n.Lhs {
				highlightWriteInExpr(s)
			}
		case *ast.GenDecl:
			if n.Tok == token.CONST || n.Tok == token.VAR {
				for _, spec := range n.Specs {
					if spec, ok := spec.(*ast.ValueSpec); ok {
						for _, ele := range spec.Names {
							highlightWriteInExpr(ele)
						}
					}
				}
			}
		case *ast.IncDecStmt:
			highlightWriteInExpr(n.X)
		case *ast.SendStmt:
			highlightWriteInExpr(n.Chan)
		case *ast.CompositeLit:
			t := info.TypeOf(n)
			if t == nil {
				t = types.Typ[types.Invalid]
			}
			if ptr, ok := t.Underlying().(*types.Pointer); ok {
				t = ptr.Elem()
			}
			if _, ok := t.Underlying().(*types.Struct); ok {
				for _, expr := range n.Elts {
					if expr, ok := (expr).(*ast.KeyValueExpr); ok {
						highlightWriteInExpr(expr.Key)
					}
				}
			}
		case *ast.RangeStmt:
			highlightWriteInExpr(n.Key)
			highlightWriteInExpr(n.Value)
		case *ast.Field:
			for _, name := range n.Names {
				highlightIdent(name, protocol.Text)
			}
		case *ast.Ident:
			// This case is reached for all Idents,
			// including those also visited by highlightWriteInExpr.
			if is[*types.Var](info.ObjectOf(n)) {
				highlightIdent(n, protocol.Read)
			} else {
				// kind of idents in PkgName, etc. is Text
				highlightIdent(n, protocol.Text)
			}
		case *ast.ImportSpec:
			pkgname := info.PkgNameOf(n)
			if pkgname == obj {
				if n.Name != nil {
					highlightNode(result, n.Name, protocol.Text)
				} else {
					highlightNode(result, n, protocol.Text)
				}
			}
		}
		return true
	})
}
