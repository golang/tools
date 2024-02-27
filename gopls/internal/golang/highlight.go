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

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/util/typesutil"
	"golang.org/x/tools/internal/event"
)

func Highlight(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, position protocol.Position) ([]protocol.Range, error) {
	ctx, done := event.Start(ctx, "golang.Highlight")
	defer done()

	// We always want fully parsed files for highlight, regardless
	// of whether the file belongs to a workspace package.
	pkg, pgf, err := NarrowestPackageForFile(ctx, snapshot, fh.URI())
	if err != nil {
		return nil, fmt.Errorf("getting package for Highlight: %w", err)
	}

	pos, err := pgf.PositionPos(position)
	if err != nil {
		return nil, err
	}
	path, _ := astutil.PathEnclosingInterval(pgf.File, pos, pos)
	if len(path) == 0 {
		return nil, fmt.Errorf("no enclosing position found for %v:%v", position.Line, position.Character)
	}
	// If start == end for astutil.PathEnclosingInterval, the 1-char interval
	// following start is used instead. As a result, we might not get an exact
	// match so we should check the 1-char interval to the left of the passed
	// in position to see if that is an exact match.
	if _, ok := path[0].(*ast.Ident); !ok {
		if p, _ := astutil.PathEnclosingInterval(pgf.File, pos-1, pos-1); p != nil {
			switch p[0].(type) {
			case *ast.Ident, *ast.SelectorExpr:
				path = p // use preceding ident/selector
			}
		}
	}
	result, err := highlightPath(path, pgf.File, pkg.GetTypesInfo())
	if err != nil {
		return nil, err
	}
	var ranges []protocol.Range
	for rng := range result {
		rng, err := pgf.PosRange(rng.start, rng.end)
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, rng)
	}
	return ranges, nil
}

// highlightPath returns ranges to highlight for the given enclosing path,
// which should be the result of astutil.PathEnclosingInterval.
func highlightPath(path []ast.Node, file *ast.File, info *types.Info) (map[posRange]struct{}, error) {
	result := make(map[posRange]struct{})
	switch node := path[0].(type) {
	case *ast.BasicLit:
		// Import path string literal?
		if len(path) > 1 {
			if imp, ok := path[1].(*ast.ImportSpec); ok {
				highlight := func(n ast.Node) {
					result[posRange{start: n.Pos(), end: n.End()}] = struct{}{}
				}

				// Highlight the import itself...
				highlight(imp)

				// ...and all references to it in the file.
				if pkgname, ok := typesutil.ImportedPkgName(info, imp); ok {
					ast.Inspect(file, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok &&
							info.Uses[id] == pkgname {
							highlight(id)
						}
						return true
					})
				}
				return result, nil
			}
		}
		highlightFuncControlFlow(path, result)
	case *ast.ReturnStmt, *ast.FuncDecl, *ast.FuncType:
		highlightFuncControlFlow(path, result)
	case *ast.Ident:
		// Check if ident is inside return or func decl.
		highlightFuncControlFlow(path, result)
		highlightIdentifier(node, file, info, result)
	case *ast.ForStmt, *ast.RangeStmt:
		highlightLoopControlFlow(path, info, result)
	case *ast.SwitchStmt:
		highlightSwitchFlow(path, info, result)
	case *ast.BranchStmt:
		// BREAK can exit a loop, switch or select, while CONTINUE exit a loop so
		// these need to be handled separately. They can also be embedded in any
		// other loop/switch/select if they have a label. TODO: add support for
		// GOTO and FALLTHROUGH as well.
		switch node.Tok {
		case token.BREAK:
			if node.Label != nil {
				highlightLabeledFlow(path, info, node, result)
			} else {
				highlightUnlabeledBreakFlow(path, info, result)
			}
		case token.CONTINUE:
			if node.Label != nil {
				highlightLabeledFlow(path, info, node, result)
			} else {
				highlightLoopControlFlow(path, info, result)
			}
		}
	default:
		// If the cursor is in an unidentified area, return empty results.
		return nil, nil
	}
	return result, nil
}

type posRange struct {
	start, end token.Pos
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
func highlightFuncControlFlow(path []ast.Node, result map[posRange]unit) {

	var (
		funcType   *ast.FuncType   // type of enclosing func, or nil
		funcBody   *ast.BlockStmt  // body of enclosing func, or nil
		returnStmt *ast.ReturnStmt // enclosing ReturnStmt within the func, or nil
	)

findEnclosingFunc:
	for i, n := range path {
		switch n := n.(type) {
		// TODO(rfindley, low priority): these pre-existing cases for KeyValueExpr
		// and CallExpr appear to avoid highlighting when the cursor is in a
		// complicated expression. However, the basis for this heuristic is
		// unclear. Can we formalize a rationale?
		case *ast.KeyValueExpr:
			// If cursor is in a key: value expr, we don't want control flow highlighting.
			return

		case *ast.CallExpr:
			// If cursor is an arg in a callExpr, we don't want control flow highlighting.
			if i > 0 {
				for _, arg := range n.Args {
					if arg == path[i-1] {
						return
					}
				}
			}

		case *ast.FuncLit:
			funcType = n.Type
			funcBody = n.Body
			break findEnclosingFunc

		case *ast.FuncDecl:
			funcType = n.Type
			funcBody = n.Body
			break findEnclosingFunc

		case *ast.ReturnStmt:
			returnStmt = n
		}
	}

	if funcType == nil {
		return // cursor is not in a function
	}

	// Helper functions for inspecting the current location.
	var (
		pos    = path[0].Pos()
		inSpan = func(start, end token.Pos) bool { return start <= pos && pos < end }
		inNode = func(n ast.Node) bool { return inSpan(n.Pos(), n.End()) }
	)

	inResults := funcType.Results != nil && inNode(funcType.Results)

	// If the cursor is on a "return" or "func" keyword, but not highlighting any
	// specific field or expression, we should highlight all of the exit points
	// of the function, including the "return" and "func" keywords.
	funcEnd := funcType.Func + token.Pos(len("func"))
	highlightAll := path[0] == returnStmt || inSpan(funcType.Func, funcEnd)
	var highlightIndexes map[int]bool

	if highlightAll {
		// Add the "func" part of the func declaration.
		result[posRange{
			start: funcType.Func,
			end:   funcEnd,
		}] = unit{}
	} else if returnStmt == nil && !inResults {
		return // nothing to highlight
	} else {
		// If we're not highighting the entire return statement, we need to collect
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
							result[posRange{name.Pos(), name.End()}] = unit{}
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
						result[posRange{field.Pos(), field.End()}] = unit{}
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
					result[posRange{n.Pos(), n.End()}] = unit{}
				} else {
					// Add the highlighted indexes.
					for i, expr := range n.Results {
						if highlightIndexes[i] {
							result[posRange{expr.Pos(), expr.End()}] = unit{}
						}
					}
				}
				return false

			}
			return true
		})
	}
}

// highlightUnlabeledBreakFlow highlights the innermost enclosing for/range/switch or swlect
func highlightUnlabeledBreakFlow(path []ast.Node, info *types.Info, result map[posRange]struct{}) {
	// Reverse walk the path until we find closest loop, select, or switch.
	for _, n := range path {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			highlightLoopControlFlow(path, info, result)
			return // only highlight the innermost statement
		case *ast.SwitchStmt:
			highlightSwitchFlow(path, info, result)
			return
		case *ast.SelectStmt:
			// TODO: add highlight when breaking a select.
			return
		}
	}
}

// highlightLabeledFlow highlights the enclosing labeled for, range,
// or switch statement denoted by a labeled break or continue stmt.
func highlightLabeledFlow(path []ast.Node, info *types.Info, stmt *ast.BranchStmt, result map[posRange]struct{}) {
	use := info.Uses[stmt.Label]
	if use == nil {
		return
	}
	for _, n := range path {
		if label, ok := n.(*ast.LabeledStmt); ok && info.Defs[label.Label] == use {
			switch label.Stmt.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				highlightLoopControlFlow([]ast.Node{label.Stmt, label}, info, result)
			case *ast.SwitchStmt:
				highlightSwitchFlow([]ast.Node{label.Stmt, label}, info, result)
			}
			return
		}
	}
}

func labelFor(path []ast.Node) *ast.Ident {
	if len(path) > 1 {
		if n, ok := path[1].(*ast.LabeledStmt); ok {
			return n.Label
		}
	}
	return nil
}

func highlightLoopControlFlow(path []ast.Node, info *types.Info, result map[posRange]struct{}) {
	var loop ast.Node
	var loopLabel *ast.Ident
	stmtLabel := labelFor(path)
Outer:
	// Reverse walk the path till we get to the for loop.
	for i := range path {
		switch n := path[i].(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			loopLabel = labelFor(path[i:])

			if stmtLabel == nil || loopLabel == stmtLabel {
				loop = n
				break Outer
			}
		}
	}
	if loop == nil {
		return
	}

	// Add the for statement.
	rng := posRange{
		start: loop.Pos(),
		end:   loop.Pos() + token.Pos(len("for")),
	}
	result[rng] = struct{}{}

	// Traverse AST to find branch statements within the same for-loop.
	ast.Inspect(loop, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			return loop == n
		case *ast.SwitchStmt, *ast.SelectStmt:
			return false
		}
		b, ok := n.(*ast.BranchStmt)
		if !ok {
			return true
		}
		if b.Label == nil || info.Uses[b.Label] == info.Defs[loopLabel] {
			result[posRange{start: b.Pos(), end: b.End()}] = struct{}{}
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
			result[posRange{start: n.Pos(), end: n.End()}] = struct{}{}
		}
		return true
	})

	// We don't need to check other for loops if we aren't looking for labeled statements.
	if loopLabel == nil {
		return
	}

	// Find labeled branch statements in any loop.
	ast.Inspect(loop, func(n ast.Node) bool {
		b, ok := n.(*ast.BranchStmt)
		if !ok {
			return true
		}
		// statement with labels that matches the loop
		if b.Label != nil && info.Uses[b.Label] == info.Defs[loopLabel] {
			result[posRange{start: b.Pos(), end: b.End()}] = struct{}{}
		}
		return true
	})
}

func highlightSwitchFlow(path []ast.Node, info *types.Info, result map[posRange]struct{}) {
	var switchNode ast.Node
	var switchNodeLabel *ast.Ident
	stmtLabel := labelFor(path)
Outer:
	// Reverse walk the path till we get to the switch statement.
	for i := range path {
		switch n := path[i].(type) {
		case *ast.SwitchStmt:
			switchNodeLabel = labelFor(path[i:])
			if stmtLabel == nil || switchNodeLabel == stmtLabel {
				switchNode = n
				break Outer
			}
		}
	}
	// Cursor is not in a switch statement
	if switchNode == nil {
		return
	}

	// Add the switch statement.
	rng := posRange{
		start: switchNode.Pos(),
		end:   switchNode.Pos() + token.Pos(len("switch")),
	}
	result[rng] = struct{}{}

	// Traverse AST to find break statements within the same switch.
	ast.Inspect(switchNode, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.SwitchStmt:
			return switchNode == n
		case *ast.ForStmt, *ast.RangeStmt, *ast.SelectStmt:
			return false
		}

		b, ok := n.(*ast.BranchStmt)
		if !ok || b.Tok != token.BREAK {
			return true
		}

		if b.Label == nil || info.Uses[b.Label] == info.Defs[switchNodeLabel] {
			result[posRange{start: b.Pos(), end: b.End()}] = struct{}{}
		}
		return true
	})

	// We don't need to check other switches if we aren't looking for labeled statements.
	if switchNodeLabel == nil {
		return
	}

	// Find labeled break statements in any switch
	ast.Inspect(switchNode, func(n ast.Node) bool {
		b, ok := n.(*ast.BranchStmt)
		if !ok || b.Tok != token.BREAK {
			return true
		}

		if b.Label != nil && info.Uses[b.Label] == info.Defs[switchNodeLabel] {
			result[posRange{start: b.Pos(), end: b.End()}] = struct{}{}
		}

		return true
	})
}

func highlightIdentifier(id *ast.Ident, file *ast.File, info *types.Info, result map[posRange]struct{}) {
	highlight := func(n ast.Node) {
		result[posRange{start: n.Pos(), end: n.End()}] = struct{}{}
	}

	// obj may be nil if the Ident is undefined.
	// In this case, the behavior expected by tests is
	// to match other undefined Idents of the same name.
	obj := info.ObjectOf(id)

	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if n.Name == id.Name && info.ObjectOf(n) == obj {
				highlight(n)
			}

		case *ast.ImportSpec:
			pkgname, ok := typesutil.ImportedPkgName(info, n)
			if ok && pkgname == obj {
				if n.Name != nil {
					highlight(n.Name)
				} else {
					highlight(n)
				}
			}
		}
		return true
	})
}
