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
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	gastutil "golang.org/x/tools/gopls/internal/util/astutil"
	"golang.org/x/tools/internal/event"
)

func Highlight(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, position protocol.Position) ([]protocol.DocumentHighlight, error) {
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
	result, err := highlightPath(path, pgf.File, pkg.TypesInfo(), pos)
	if err != nil {
		return nil, err
	}
	var ranges []protocol.DocumentHighlight
	for rng, kind := range result {
		rng, err := pgf.PosRange(rng.start, rng.end)
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

// highlightPath returns ranges to highlight for the given enclosing path,
// which should be the result of astutil.PathEnclosingInterval.
func highlightPath(path []ast.Node, file *ast.File, info *types.Info, pos token.Pos) (map[posRange]protocol.DocumentHighlightKind, error) {
	result := make(map[posRange]protocol.DocumentHighlightKind)
	// Inside a printf-style call?
	for _, node := range path {
		if call, ok := node.(*ast.CallExpr); ok {
			for _, args := range call.Args {
				// Only try when pos is in right side of the format String.
				if basicList, ok := args.(*ast.BasicLit); ok && basicList.Pos() < pos &&
					basicList.Kind == token.STRING && strings.Contains(basicList.Value, "%") {
					highlightPrintf(basicList, call, pos, result)
				}
			}
		}
	}
	switch node := path[0].(type) {
	case *ast.BasicLit:
		// Import path string literal?
		if len(path) > 1 {
			if imp, ok := path[1].(*ast.ImportSpec); ok {
				highlight := func(n ast.Node) {
					highlightNode(result, n, protocol.Text)
				}

				// Highlight the import itself...
				highlight(imp)

				// ...and all references to it in the file.
				if pkgname := info.PkgNameOf(imp); pkgname != nil {
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
	case *ast.SwitchStmt, *ast.TypeSwitchStmt:
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
	}

	return result, nil
}

// highlightPrintf identifies and highlights the relationships between placeholders
// in a format string and their corresponding variadic arguments in a printf-style
// function call.
//
// For example:
//
// fmt.Printf("Hello %s, you scored %d", name, score)
//
// If the cursor is on %s or name, highlightPrintf will highlight %s as a write operation,
// and name as a read operation.
func highlightPrintf(directive *ast.BasicLit, call *ast.CallExpr, pos token.Pos, result map[posRange]protocol.DocumentHighlightKind) {
	// Two '%'s are interpreted as one '%'(escaped), let's replace them with spaces.
	format := strings.Replace(directive.Value, "%%", "  ", -1)
	if strings.Contains(directive.Value, "%[") ||
		strings.Contains(directive.Value, "%p") ||
		strings.Contains(directive.Value, "%T") {
		// parsef can not handle these cases.
		return
	}
	expectedVariadicArgs := make([]ast.Expr, strings.Count(format, "%"))
	firstVariadic := -1
	for i, arg := range call.Args {
		if directive == arg {
			firstVariadic = i + 1
			argsLen := len(call.Args) - i - 1
			if argsLen > len(expectedVariadicArgs) {
				// Translate from Printf(a0,"%d %d",5, 6, 7) to [5, 6]
				copy(expectedVariadicArgs, call.Args[firstVariadic:firstVariadic+len(expectedVariadicArgs)])
			} else {
				// Translate from Printf(a0,"%d %d %s",5, 6) to [5, 6, nil]
				copy(expectedVariadicArgs[:argsLen], call.Args[firstVariadic:])
			}
			break
		}
	}
	formatItems, err := parsef(format, directive.Pos(), expectedVariadicArgs...)
	if err != nil {
		return
	}
	var percent formatPercent
	// Cursor in argument.
	if pos > directive.End() {
		var curVariadic int
		// Which variadic argument cursor sits inside.
		for i := firstVariadic; i < len(call.Args); i++ {
			if gastutil.NodeContains(call.Args[i], pos) {
				// Offset relative to formatItems.
				curVariadic = i - firstVariadic
				break
			}
		}
		index := -1
		for _, item := range formatItems {
			switch item := item.(type) {
			case formatPercent:
				percent = item
				index++
			case formatVerb:
				if token.Pos(percent).IsValid() {
					if index == curVariadic {
						// Placeholders behave like writting values from arguments to themselves,
						// so highlight them with Write semantic.
						highlightRange(result, token.Pos(percent), item.rang.end, protocol.Write)
						highlightRange(result, item.operand.Pos(), item.operand.End(), protocol.Read)
						return
					}
					percent = formatPercent(token.NoPos)
				}
			}
		}
	} else {
		// Cursor in format string.
		for _, item := range formatItems {
			switch item := item.(type) {
			case formatPercent:
				percent = item
			case formatVerb:
				if token.Pos(percent).IsValid() {
					if token.Pos(percent) <= pos && pos <= item.rang.end {
						highlightRange(result, token.Pos(percent), item.rang.end, protocol.Write)
						if item.operand != nil {
							highlightRange(result, item.operand.Pos(), item.operand.End(), protocol.Read)
						}
						return
					}
					percent = formatPercent(token.NoPos)
				}
			}
		}
	}
}

// Below are formatting directives definitions.
type formatPercent token.Pos
type formatLiteral struct {
	literal string
	rang    posRange
}
type formatFlags struct {
	flag string
	rang posRange
}
type formatWidth struct {
	width int
	rang  posRange
}
type formatPrec struct {
	prec int
	rang posRange
}
type formatVerb struct {
	verb    rune
	rang    posRange
	operand ast.Expr // verb's corresponding operand, may be nil
}

type formatItem interface {
	formatItem()
}

func (formatPercent) formatItem() {}
func (formatLiteral) formatItem() {}
func (formatVerb) formatItem()    {}
func (formatWidth) formatItem()   {}
func (formatFlags) formatItem()   {}
func (formatPrec) formatItem()    {}

type formatFunc func(fmt.State, rune)

var _ fmt.Formatter = formatFunc(nil)

func (f formatFunc) Format(st fmt.State, verb rune) { f(st, verb) }

// parsef parses a printf-style format string into its constituent components together with
// their position in the source code, including [formatLiteral], formatting directives
// [formatFlags], [formatPrecision], [formatWidth], [formatPrecision], [formatVerb], and its operand.
//
// If format contains explicit argument indexes, eg. fmt.Sprintf("%[2]d %[1]d\n", 11, 22),
// the returned range will not be correct.
// If an invalid argument is given for a verb, such as providing a string to %d, the returned error will
// contain a description of the problem.
func parsef(format string, pos token.Pos, args ...ast.Expr) ([]formatItem, error) {
	const sep = "__GOPLS_SEP__"
	// A conversion represents a single % operation and its operand.
	type conversion struct {
		verb    rune
		width   int    // or -1
		prec    int    // or -1
		flag    string // some of "-+# 0"
		operand ast.Expr
	}
	var convs []conversion
	wrappers := make([]any, len(args))
	for i, operand := range args {
		wrappers[i] = formatFunc(func(st fmt.State, verb rune) {
			st.Write([]byte(sep))
			width, ok := st.Width()
			if !ok {
				width = -1
			}
			prec, ok := st.Precision()
			if !ok {
				prec = -1
			}
			flag := ""
			for _, b := range "-+# 0" {
				if st.Flag(int(b)) {
					flag += string(b)
				}
			}
			convs = append(convs, conversion{
				verb:    verb,
				width:   width,
				prec:    prec,
				flag:    flag,
				operand: operand,
			})
		})
	}

	// Interleave the literals and the conversions.
	var formatItems []formatItem
	s := fmt.Sprintf(format, wrappers...)
	// All errors begin with the string "%!".
	if strings.Contains(s, "%!") {
		return nil, fmt.Errorf("%s", strings.Replace(s, sep, "", -1))
	}
	for i, word := range strings.Split(s, sep) {
		if word != "" {
			formatItems = append(formatItems, formatLiteral{
				literal: word,
				rang: posRange{
					start: pos,
					end:   pos + token.Pos(len(word)),
				},
			})
			pos = pos + token.Pos(len(word))
		}
		if i < len(convs) {
			conv := convs[i]
			// Collect %.
			formatItems = append(formatItems, formatPercent(pos))
			pos += 1
			// Collect flags.
			if flag := conv.flag; flag != "" {
				length := token.Pos(len(conv.flag))
				formatItems = append(formatItems, formatFlags{
					flag: flag,
					rang: posRange{
						start: pos,
						end:   pos + length,
					},
				})
				pos += length
			}
			// Collect width.
			if width := conv.width; conv.width != -1 {
				length := token.Pos(len(fmt.Sprintf("%d", conv.width)))
				formatItems = append(formatItems, formatWidth{
					width: width,
					rang: posRange{
						start: pos,
						end:   pos + length,
					},
				})
				pos += length
			}
			// Collect precision, which starts with a dot.
			if prec := conv.prec; conv.prec != -1 {
				length := token.Pos(len(fmt.Sprintf("%d", conv.prec))) + 1
				formatItems = append(formatItems, formatPrec{
					prec: prec,
					rang: posRange{
						start: pos,
						end:   pos + length,
					},
				})
				pos += length
			}
			// Collect verb, which must be present.
			length := token.Pos(len(string(conv.verb)))
			formatItems = append(formatItems, formatVerb{
				verb: conv.verb,
				rang: posRange{
					start: pos,
					end:   pos + length,
				},
				operand: conv.operand,
			})
			pos += length
		}
	}
	return formatItems, nil
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
func highlightFuncControlFlow(path []ast.Node, result map[posRange]protocol.DocumentHighlightKind) {

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
		highlightRange(result, funcType.Func, funcEnd, protocol.Text)
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

// highlightUnlabeledBreakFlow highlights the innermost enclosing for/range/switch or swlect
func highlightUnlabeledBreakFlow(path []ast.Node, info *types.Info, result map[posRange]protocol.DocumentHighlightKind) {
	// Reverse walk the path until we find closest loop, select, or switch.
	for _, n := range path {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			highlightLoopControlFlow(path, info, result)
			return // only highlight the innermost statement
		case *ast.SwitchStmt, *ast.TypeSwitchStmt:
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
func highlightLabeledFlow(path []ast.Node, info *types.Info, stmt *ast.BranchStmt, result map[posRange]protocol.DocumentHighlightKind) {
	use := info.Uses[stmt.Label]
	if use == nil {
		return
	}
	for _, n := range path {
		if label, ok := n.(*ast.LabeledStmt); ok && info.Defs[label.Label] == use {
			switch label.Stmt.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				highlightLoopControlFlow([]ast.Node{label.Stmt, label}, info, result)
			case *ast.SwitchStmt, *ast.TypeSwitchStmt:
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

func highlightLoopControlFlow(path []ast.Node, info *types.Info, result map[posRange]protocol.DocumentHighlightKind) {
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
	rngStart := loop.Pos()
	rngEnd := loop.Pos() + token.Pos(len("for"))
	highlightRange(result, rngStart, rngEnd, protocol.Text)

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
		if b.Label == nil || info.Uses[b.Label] == info.Defs[loopLabel] {
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
			highlightNode(result, b, protocol.Text)
		}
		return true
	})
}

func highlightSwitchFlow(path []ast.Node, info *types.Info, result map[posRange]protocol.DocumentHighlightKind) {
	var switchNode ast.Node
	var switchNodeLabel *ast.Ident
	stmtLabel := labelFor(path)
Outer:
	// Reverse walk the path till we get to the switch statement.
	for i := range path {
		switch n := path[i].(type) {
		case *ast.SwitchStmt, *ast.TypeSwitchStmt:
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
	rngStart := switchNode.Pos()
	rngEnd := switchNode.Pos() + token.Pos(len("switch"))
	highlightRange(result, rngStart, rngEnd, protocol.Text)

	// Traverse AST to find break statements within the same switch.
	ast.Inspect(switchNode, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.SwitchStmt, *ast.TypeSwitchStmt:
			return switchNode == n
		case *ast.ForStmt, *ast.RangeStmt, *ast.SelectStmt:
			return false
		}

		b, ok := n.(*ast.BranchStmt)
		if !ok || b.Tok != token.BREAK {
			return true
		}

		if b.Label == nil || info.Uses[b.Label] == info.Defs[switchNodeLabel] {
			highlightNode(result, b, protocol.Text)
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
			highlightNode(result, b, protocol.Text)
		}

		return true
	})
}

func highlightNode(result map[posRange]protocol.DocumentHighlightKind, n ast.Node, kind protocol.DocumentHighlightKind) {
	highlightRange(result, n.Pos(), n.End(), kind)
}

func highlightRange(result map[posRange]protocol.DocumentHighlightKind, pos, end token.Pos, kind protocol.DocumentHighlightKind) {
	rng := posRange{pos, end}
	// Order of traversal is important: some nodes (e.g. identifiers) are
	// visited more than once, but the kind set during the first visitation "wins".
	if _, exists := result[rng]; !exists {
		result[rng] = kind
	}
}

func highlightIdentifier(id *ast.Ident, file *ast.File, info *types.Info, result map[posRange]protocol.DocumentHighlightKind) {

	// obj may be nil if the Ident is undefined.
	// In this case, the behavior expected by tests is
	// to match other undefined Idents of the same name.
	obj := info.ObjectOf(id)

	highlightIdent := func(n *ast.Ident, kind protocol.DocumentHighlightKind) {
		if n.Name == id.Name && info.ObjectOf(n) == obj {
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

	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
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
