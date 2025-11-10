// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/edge"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/typesinternal"
)

// extractVariableOne implements the refactor.extract.{variable,constant} CodeAction command.
func extractVariableOne(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	countExtractVariable.Inc()
	return extractVariable(pkg, pgf, start, end, false)
}

// extractVariableAll implements the refactor.extract.{variable,constant}-all CodeAction command.
func extractVariableAll(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	countExtractVariableAll.Inc()
	return extractVariable(pkg, pgf, start, end, true)
}

// extractVariable replaces one or all occurrences of a specified
// expression within the same function with newVar. If 'all' is true,
// it replaces all occurrences of the same expression; otherwise, it
// only replaces the selected expression.
//
// The new variable/constant is declared as close as possible to the first found expression
// within the deepest common scope accessible to all candidate occurrences.
func extractVariable(pkg *cache.Package, pgf *parsego.File, start, end token.Pos, all bool) (*token.FileSet, *analysis.SuggestedFix, error) {
	var (
		fset = pkg.FileSet()
		info = pkg.TypesInfo()
		file = pgf.File
	)
	curExprs, err := canExtractVariable(info, pgf.Cursor, start, end, all)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot extract: %v", err)
	}
	expr0 := curExprs[0].Node().(ast.Expr)

	// innermost scope enclosing ith expression
	exprScopes := make([]*types.Scope, len(curExprs))
	for i, curExpr := range curExprs {
		exprScopes[i] = info.Scopes[file].Innermost(curExpr.Node().Pos())
	}

	hasCollision := func(name string) bool {
		for _, scope := range exprScopes {
			if s, _ := scope.LookupParent(name, token.NoPos); s != nil {
				return true
			}
		}
		return false
	}
	constant := info.Types[expr0].Value != nil

	// Generate name(s) for new declaration.
	baseName := cond(constant, "newConst", "newVar")
	var lhsNames []string
	switch expr := expr0.(type) {
	case *ast.CallExpr:
		tup, ok := info.TypeOf(expr).(*types.Tuple)
		if !ok {
			// conversion or single-valued call:
			// treat it the same as our standard extract variable case.
			name, _ := generateName(0, baseName, hasCollision)
			lhsNames = append(lhsNames, name)

		} else {
			// call with multiple results
			idx := 0
			for range tup.Len() {
				// Generate a unique variable for each result.
				var name string
				name, idx = generateName(idx, baseName, hasCollision)
				lhsNames = append(lhsNames, name)
			}
		}

	default:
		// TODO: stricter rules for selectorExpr.
		name, _ := generateName(0, baseName, hasCollision)
		lhsNames = append(lhsNames, name)
	}

	// Where all the extractable positions can see variable being declared.
	var commonScope *types.Scope
	counter := make(map[*types.Scope]int)
Outer:
	for _, scope := range exprScopes {
		for s := scope; s != nil; s = s.Parent() {
			counter[s]++
			if counter[s] == len(exprScopes) {
				// A scope whose count is len(scopes) is common to all ancestor paths.
				// Stop at the first (innermost) one.
				commonScope = s
				break Outer
			}
		}
	}

	visible := curExprs[0] // Insert newVar inside commonScope before the first occurrence of the expression.
	if commonScope != exprScopes[0] {
		// This means the first expr within function body is not the largest scope,
		// we need to find the scope immediately follow the common
		// scope where we will insert the statement before.
		child := exprScopes[0]
		for p := child; p != nil; p = p.Parent() {
			if p == commonScope {
				break
			}
			child = p
		}
		visible, _ = pgf.Cursor.FindByPos(child.Pos(), child.End())
	}
	variables, err := collectFreeVars(info, file, expr0.Pos(), expr0.End(), expr0)
	if err != nil {
		return nil, nil, err
	}

	// TODO: There is a bug here: for a variable declared in a labeled
	// switch/for statement it returns the for/switch statement itself
	// which produces the below code which is a compiler error. e.g.
	//     label:
	//         switch r1 := r() { ... break label ... }
	// On extracting "r()" to a variable
	//     label:
	//         x := r()
	//         switch r1 := x { ... break label ... } // compiler error
	//
	var (
		insertPos   token.Pos
		indentation string
		stmtOK      bool // ok to use ":=" instead of var/const decl?
	)
	if funcDecl, _ := cursorutil.FirstEnclosing[*ast.FuncDecl](visible); funcDecl != nil &&
		astutil.NodeContainsPos(funcDecl.Body, start) {

		beforePos, err := stmtToInsertVarBefore(visible, variables)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot find location to insert extraction: %v", err)
		}
		// Within function: compute appropriate statement indentation.
		indent, err := pgf.Indentation(beforePos)
		if err != nil {
			return nil, nil, err
		}
		insertPos = beforePos
		indentation = "\n" + indent

		// Currently, we always extract a constant expression
		// to a const declaration (and logic in CodeAction
		// assumes that we do so); this is conservative because
		// it preserves its constant-ness.
		//
		// In future, constant expressions used only in
		// contexts where constant-ness isn't important could
		// be profitably extracted to a var declaration or :=
		// statement, especially if the latter is the Init of
		// an {If,For,Switch}Stmt.
		stmtOK = !constant
	} else {
		// Outside any statement: insert before the current
		// top-level declaration, without indentation.
		for curDecl := range visible.Enclosing((*ast.FuncDecl)(nil), (*ast.GenDecl)(nil)) {
			insertPos = curDecl.Node().Pos()
		}
		indentation = "\n"
	}

	// Create statement to declare extracted var/const.
	//
	// TODO(adonovan): beware the const decls are not valid short
	// statements, so if fixing #70563 causes
	// StmtToInsertVarBefore to evolve to permit declarations in
	// the "pre" part of an IfStmt, like so:
	//   Before:
	//	if cond {
	//      } else if «1 + 2» > 0 {
	//      }
	//   After:
	//	if x := 1 + 2; cond {
	//      } else if x > 0 {
	//      }
	// then it will need to become aware that this is invalid
	// for constants.
	//
	// Conversely, a short var decl stmt is not valid at top level,
	// so when we fix #70665, we'll need to use a var decl.
	var newNode ast.Node
	if !stmtOK {
		// var/const x1, ..., xn = expr
		var names []*ast.Ident
		for _, name := range lhsNames {
			names = append(names, ast.NewIdent(name))
		}
		newNode = &ast.GenDecl{
			Tok: cond(constant, token.CONST, token.VAR),
			Specs: []ast.Spec{
				&ast.ValueSpec{
					Names:  names,
					Values: []ast.Expr{expr0},
				},
			},
		}

	} else {
		// var: x1, ... xn := expr
		var lhs []ast.Expr
		for _, name := range lhsNames {
			lhs = append(lhs, ast.NewIdent(name))
		}
		newNode = &ast.AssignStmt{
			Tok: token.DEFINE,
			Lhs: lhs,
			Rhs: []ast.Expr{expr0},
		}
	}

	// Format and indent the declaration.
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, newNode); err != nil {
		return nil, nil, err
	}
	// TODO(adonovan): not sound for `...` string literals containing newlines.
	assignment := strings.ReplaceAll(buf.String(), "\n", indentation) + indentation
	textEdits := []analysis.TextEdit{{
		Pos:     insertPos,
		End:     insertPos,
		NewText: []byte(assignment),
	}}
	for _, curExpr := range curExprs {
		e := curExpr.Node()
		textEdits = append(textEdits, analysis.TextEdit{
			Pos:     e.Pos(),
			End:     e.End(),
			NewText: []byte(strings.Join(lhsNames, ", ")),
		})
	}
	return fset, &analysis.SuggestedFix{
		TextEdits: textEdits,
	}, nil
}

// stmtToInsertVarBefore returns the ast.Stmt before which we can safely insert a new variable,
// and ensures that the new declaration is inserted at a point where all free variables are declared before.
// Some examples:
//
// Basic Example:
//
//	z := 1
//	y := z + x
//
// If x is undeclared, then this function would return `y := z + x`, so that we
// can insert `x := ` on the line before `y := z + x`.
//
// valid IfStmt example:
//
//	if z == 1 {
//	} else if z == y {}
//
// If y is undeclared, then this function would return `if z == 1 {`, because we cannot
// insert a statement between an if and an else if statement. As a result, we need to find
// the top of the if chain to insert `y := ` before.
//
// invalid IfStmt example:
//
//	if x := 1; true {
//	} else if y := x + 1; true { //apply refactor.extract.variable to x
//	}
//
// `x` is a free variable defined in the IfStmt, we should not insert
// the extracted expression outside the IfStmt scope, instead, return an error.
func stmtToInsertVarBefore(cur inspector.Cursor, variables []*variable) (token.Pos, error) {
	// Walk up to enclosing statement.
	{
		var curStmt inspector.Cursor
		for cur := range cur.Enclosing() {
			if is[ast.Stmt](cur.Node()) {
				curStmt = cur
				break
			}
		}
		if !astutil.CursorValid(curStmt) {
			return 0, fmt.Errorf("no enclosing statement")
		}
		cur = curStmt
	}

	// hasFreeVar reports if any free variables is defined inside stmt (which may be nil).
	// If true, indicates that the insertion point will sit before the variable declaration.
	hasFreeVar := func(stmt ast.Stmt) bool {
		if stmt == nil {
			return false
		}
		for _, v := range variables {
			if astutil.NodeContainsPos(stmt, v.obj.Pos()) {
				return true
			}
		}
		return false
	}

	// baseIfStmt walks up the if/else-if chain until we get to
	// the top of the current if chain.
	baseIfStmt := func(curIf inspector.Cursor) (token.Pos, error) {
		for astutil.IsChildOf(curIf, edge.IfStmt_Else) {
			curIf = curIf.Parent()
			if hasFreeVar(curIf.Node().(*ast.IfStmt).Init) {
				return 0, fmt.Errorf("else-if's init has free variable")
			}
		}
		return curIf.Node().Pos(), nil
	}

	stmt := cur.Node()
	switch stmt := stmt.(type) {
	case *ast.IfStmt:
		if hasFreeVar(stmt.Init) {
			return 0, fmt.Errorf("if statement's init has free variable")
		}
		// stmt is inside of the if declaration.
		// We need to check if we are in an else-if stmt and
		// get the base if statement.
		return baseIfStmt(cur)

	case *ast.CaseClause:
		for curSwitch := range cur.Enclosing((*ast.SwitchStmt)(nil), (*ast.TypeSwitchStmt)(nil)) {
			swtch := curSwitch.Node()
			var init ast.Stmt
			switch swtch := swtch.(type) {
			case *ast.SwitchStmt:
				init = swtch.Init
			case *ast.TypeSwitchStmt:
				init = swtch.Init
			}
			if hasFreeVar(init) {
				return 0, fmt.Errorf("switch's init has free variable")
			}
			return swtch.Pos(), nil
		}
	}

	// Check if the enclosing statement is inside another node.
	switch parent := cur.Parent().Node().(type) {
	case *ast.IfStmt:
		if hasFreeVar(parent.Init) {
			return 0, fmt.Errorf("if-statement's init has free variable")
		}
		return baseIfStmt(cur.Parent())

	case *ast.ForStmt:
		switch cur.Node() {
		case parent.Init, parent.Post:
			return parent.Pos(), nil
		}

	case *ast.SwitchStmt:
		if hasFreeVar(parent.Init) {
			return 0, fmt.Errorf("switch's init has free variable")
		}
		return parent.Pos(), nil

	case *ast.TypeSwitchStmt:
		if hasFreeVar(parent.Init) {
			return 0, fmt.Errorf("switch's init has free variable")
		}
		return parent.Pos(), nil
	}
	return stmt.Pos(), nil
}

// canExtractVariable reports whether the code in the given range can
// be extracted to a variable (or constant). It returns (cursors for)
// the selected expression or, if 'all', all structurally equivalent
// expressions within the same function body, in lexical order.
func canExtractVariable(info *types.Info, curFile inspector.Cursor, start, end token.Pos, all bool) ([]inspector.Cursor, error) {
	if start == end {
		return nil, fmt.Errorf("empty selection")
	}

	_, curStart, curEnd, err := astutil.Select(curFile, start, end)
	if err != nil {
		return nil, err
	}
	expr, ok := curStart.Node().(ast.Expr)
	if !ok || curEnd != curStart {
		return nil, fmt.Errorf("selection is not an expression")
	}
	if imp, _ := cursorutil.FirstEnclosing[*ast.ImportSpec](curStart); imp != nil {
		return nil, fmt.Errorf("cannot extract variable or constant in an import block")
	}
	if tv, ok := info.Types[expr]; !ok || !tv.IsValue() || tv.Type == nil || tv.HasOk() {
		// e.g. type, builtin, x.(type), 2-valued m[k], or ill-typed
		return nil, fmt.Errorf("selection is not a single-valued expression")
	}

	var curExprs []inspector.Cursor
	if !all {
		curExprs = append(curExprs, curStart)
	} else if funcDecl, curFuncDecl := cursorutil.FirstEnclosing[*ast.FuncDecl](curStart); funcDecl != nil && funcDecl.Body != nil {
		// Find all expressions in the same function body that
		// are equal to the selected expression.
		for cur := range curFuncDecl.ChildAt(edge.FuncDecl_Body, -1).Preorder() {
			if e, ok := cur.Node().(ast.Expr); ok {
				if astutil.Equal(e, expr, func(x, y *ast.Ident) bool {
					xobj, yobj := info.ObjectOf(x), info.ObjectOf(y)
					// The two identifiers must resolve to the same object,
					// or to a declaration within the candidate expression.
					// (This allows two copies of "func (x int) { print(x) }"
					// to match.)
					if xobj != nil && astutil.NodeContainsPos(e, xobj.Pos()) &&
						yobj != nil && astutil.NodeContainsPos(expr, yobj.Pos()) {
						return x.Name == y.Name
					}
					// Use info.Uses to avoid including declaration, for example,
					// when extractnig x:
					//
					//   x := 1 // should not include x
					//   y := x // include x
					//   z := x // include x
					xuse := info.Uses[x]
					return xuse != nil && xuse == info.Uses[y]
				}) {
					curExprs = append(curExprs, cur)
				}
			}
		}
	} else {
		return nil, fmt.Errorf("node %T is not inside a function", expr)
	}

	// Disallow any expr that sits in lhs of an AssignStmt or ValueSpec for now.
	//
	// TODO(golang/go#70784): In such cases, exprs are operated in "variable" mode (L-value mode in C).
	// In contrast, exprs in the RHS operate in "value" mode (R-value mode in C).
	// L-value mode refers to exprs that represent storage locations,
	// while R-value mode refers to exprs that represent values.
	// There are a number of expressions that may have L-value mode, given by:
	//
	//   lvalue = ident                -- Ident such that info.Uses[id] is a *Var
	//          | '(' lvalue ') '      -- ParenExpr
	//          | lvalue '[' expr ']'  -- IndexExpr
	//          | lvalue '.' ident     -- SelectorExpr.
	//
	// For example:
	//
	//   type foo struct {
	//       bar int
	//   }
	//   f := foo{bar: 1}
	//   x := f.bar + 1 // f.bar operates in "value" mode.
	//   f.bar = 2      // f.bar operates in "variable" mode.
	//
	// When extracting exprs in variable mode, we must be cautious. Any such extraction
	// may require capturing the address of the expression and replacing its uses with dereferenced access.
	// The type checker records this information in info.Types[id].{IsValue,Addressable}().
	// The correct result should be:
	//
	//   newVar := &f.bar
	//   x := *newVar + 1
	//   *newVar = 2
	for _, curExpr := range curExprs {
		switch ek, _ := curExpr.ParentEdge(); ek {
		case edge.AssignStmt_Lhs:
			return nil, fmt.Errorf("node %T is in LHS of an AssignStmt", expr)
		case edge.ValueSpec_Names:
			return nil, fmt.Errorf("node %T is in LHS of a ValueSpec", expr)
		}
	}

	return curExprs, nil
}

// freshName returns an identifier based on prefix (perhaps with a
// numeric suffix) that is not in scope at the specified position
// within the file. It returns the next numeric suffix to use.
func freshName(info *types.Info, file *ast.File, pos token.Pos, prefix string, idx int) (string, int) {
	scope := info.Scopes[file].Innermost(pos)
	return generateName(idx, prefix, func(name string) bool {
		obj, _ := scope.LookupParent(name, pos)
		return obj != nil
	})
}

// freshNameOutsideRange is like [freshName], but ignores names
// declared between start and end for the purposes of detecting conflicts.
//
// This is used for function extraction, where [start, end) will be extracted
// to a new scope.
func freshNameOutsideRange(info *types.Info, file *ast.File, pos, start, end token.Pos, prefix string, idx int) (string, int) {
	scope := info.Scopes[file].Innermost(pos)
	return generateName(idx, prefix, func(name string) bool {
		// Only report a collision if the object declaration
		// was outside the extracted range.
		for scope != nil {
			obj, declScope := scope.LookupParent(name, pos)
			if obj == nil {
				return false // undeclared
			}
			if !(start <= obj.Pos() && obj.Pos() < end) {
				return true // declared outside ignored range
			}
			scope = declScope.Parent()
		}
		return false
	})
}

func generateName(idx int, prefix string, hasCollision func(string) bool) (string, int) {
	name := prefix
	if idx != 0 {
		name += fmt.Sprintf("%d", idx)
	}
	for hasCollision(name) {
		idx++
		name = fmt.Sprintf("%v%d", prefix, idx)
	}
	return name, idx + 1
}

// returnVariable keeps track of the information we need to properly introduce a new variable
// that we will return in the extracted function.
type returnVariable struct {
	// name is the identifier that is used on the left-hand side of the call to
	// the extracted function.
	name *ast.Ident
	// decl is the declaration of the variable. It is used in the type signature of the
	// extracted function and for variable declarations.
	decl *ast.Field
	// zeroVal is the "zero value" of the type of the variable. It is used in a return
	// statement in the extracted function.
	zeroVal ast.Expr
}

// extractMethod refactors the selected block of code into a new method.
func extractMethod(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	countExtractMethod.Inc()
	return extractFunctionMethod(pkg, pgf, start, end, true)
}

// extractFunction refactors the selected block of code into a new function.
func extractFunction(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	countExtractFunction.Inc()
	return extractFunctionMethod(pkg, pgf, start, end, false)
}

// extractFunctionMethod refactors the selected block of code into a new function/method.
// It also replaces the selected block of code with a call to the extracted
// function. First, we manually adjust the selection range. We remove trailing
// and leading whitespace characters to ensure the range is precisely bounded
// by AST nodes. Next, we determine the variables that will be the parameters
// and return values of the extracted function/method. Lastly, we construct the call
// of the function/method and insert this call as well as the extracted function/method into
// their proper locations.
func extractFunctionMethod(cpkg *cache.Package, pgf *parsego.File, start, end token.Pos, isMethod bool) (*token.FileSet, *analysis.SuggestedFix, error) {
	var (
		fset = cpkg.FileSet()
		pkg  = cpkg.Types()
		info = cpkg.TypesInfo()
		src  = pgf.Src
		file = pgf.File
	)

	errorPrefix := "extractFunction"
	if isMethod {
		errorPrefix = "extractMethod"
	}

	p, ok, methodOk, err := canExtractFunction(pgf.Cursor, start, end)
	if (!ok && !isMethod) || (!methodOk && isMethod) {
		return nil, nil, fmt.Errorf("%s: cannot extract %s: %v", errorPrefix,
			safetoken.StartPosition(fset, start), err)
	}
	curEnclosing, curStart, curEnd, curFuncDecl := p.curEnclosing, p.curStart, p.curEnd, p.curFuncDecl

	// Narrow (start, end) to the located nodes.
	start, end = curStart.Node().Pos(), curEnd.Node().End()

	outer := curFuncDecl.Node().(*ast.FuncDecl)

	// A return statement is non-nested if its parent node is equal to the parent node
	// of the first node in the selection. These cases must be handled separately because
	// non-nested return statements are guaranteed to execute.
	var hasNonNestedReturn bool

	// Determine whether all return statements in the selection are
	// error-handling return statements. They must be of the form:
	// if err != nil {
	// 	return ..., err
	// }
	// If all return statements in the extracted block have a non-nil error, we
	// can replace the "shouldReturn" check with an error check to produce a
	// more concise output.
	var (
		allReturnsFinalErr = true  // all ReturnStmts have final 'err' expression
		hasReturn          = false // selection contains a ReturnStmt
		filter             = []ast.Node{(*ast.ReturnStmt)(nil), (*ast.FuncLit)(nil)}
	)
	curEnclosing.Inspect(filter, func(cur inspector.Cursor) (descend bool) {
		if funcLit, ok := cur.Node().(*ast.FuncLit); ok {
			// Exclude return statements in function literals because they don't affect the refactor.
			// Keep descending into func lits whose declaration is not included in the extracted block.
			return !(start < funcLit.Pos() && funcLit.End() < end)
		}
		ret := cur.Node().(*ast.ReturnStmt)
		if ret.Pos() < start || ret.End() > end {
			return false // not part of the extracted block
		}
		hasReturn = true

		if cur.Parent() == curStart.Parent() {
			hasNonNestedReturn = true
		}

		if !allReturnsFinalErr {
			// Stop the traversal if we have already found a non error-handling return statement.
			return false
		}
		// Check if the return statement returns a non-nil error as the last value.
		if len(ret.Results) > 0 {
			typ := info.TypeOf(ret.Results[len(ret.Results)-1])
			if typ != nil && types.Identical(typ, errorType) {
				// Have: return ..., err
				// Check for enclosing "if err != nil { return ..., err }".
				// In that case, we can lift the error return to the caller.
				if ifstmt, ok := cur.Parent().Parent().Node().(*ast.IfStmt); ok {
					// Only handle the case where the if statement body contains a single statement.
					if body, ok := cur.Parent().Node().(*ast.BlockStmt); ok && len(body.List) <= 1 {
						if cond, ok := ifstmt.Cond.(*ast.BinaryExpr); ok {
							tx := info.TypeOf(cond.X)
							ty := info.TypeOf(cond.Y)
							isErr := tx != nil && types.Identical(tx, errorType)
							isNil := ty != nil && types.Identical(ty, types.Typ[types.UntypedNil])
							if cond.Op == token.NEQ && isErr && isNil {
								// allReturnsErrHandling remains true
								return false
							}
						}
					}
				}
			}
		}
		allReturnsFinalErr = false
		return false
	})

	allReturnsFinalErr = hasReturn && allReturnsFinalErr

	// Now that we have determined the correct range for the selection block,
	// we must determine the signature of the extracted function. We will then replace
	// the block with an assignment statement that calls the extracted function with
	// the appropriate parameters and return values.
	variables, err := collectFreeVars(info, file, start, end, curEnclosing.Node())
	if err != nil {
		return nil, nil, err
	}

	var (
		receiverUsed bool
		receiver     *ast.Field
		receiverName string
		receiverObj  types.Object
	)
	if isMethod {
		if outer.Recv == nil || len(outer.Recv.List) == 0 {
			return nil, nil, fmt.Errorf("%s: cannot extract need method receiver", errorPrefix)
		}
		receiver = outer.Recv.List[0]
		if len(receiver.Names) == 0 || receiver.Names[0] == nil {
			return nil, nil, fmt.Errorf("%s: cannot extract need method receiver name", errorPrefix)
		}
		recvName := receiver.Names[0]
		receiverName = recvName.Name
		receiverObj = info.ObjectOf(recvName)
	}

	var (
		params, returns         []ast.Expr     // used when calling the extracted function
		paramTypes, returnTypes []*ast.Field   // used in the signature of the extracted function
		uninitialized           []types.Object // vars we will need to initialize before the call
	)

	// Avoid duplicates while traversing vars and uninitialized.
	seenVars := make(map[types.Object]ast.Expr)
	seenUninitialized := make(map[types.Object]struct{})

	// Some variables on the left-hand side of our assignment statement may be free. If our
	// selection begins in the same scope in which the free variable is defined, we can
	// redefine it in our assignment statement. See the following example, where 'b' and
	// 'err' (both free variables) can be redefined in the second funcCall() while maintaining
	// correctness.
	//
	//
	// Not Redefined:
	//
	// a, err := funcCall()
	// var b int
	// b, err = funcCall()
	//
	// Redefined:
	//
	// a, err := funcCall()
	// b, err := funcCall()
	//
	// We track the number of free variables that can be redefined to maintain our preference
	// of using "x, y, z := fn()" style assignment statements.
	var canRedefineCount int

	qual := typesinternal.FileQualifier(file, pkg)

	// Each identifier in the selected block must become (1) a parameter to the
	// extracted function, (2) a return value of the extracted function, or (3) a local
	// variable in the extracted function. Determine the outcome(s) for each variable
	// based on whether it is free, altered within the selected block, and used outside
	// of the selected block.
	for _, v := range variables {
		if _, ok := seenVars[v.obj]; ok {
			continue
		}
		if v.obj.Name() == "_" {
			// The blank identifier is always a local variable
			continue
		}
		typ := typesinternal.TypeExpr(v.obj.Type(), qual)
		seenVars[v.obj] = typ
		identifier := ast.NewIdent(v.obj.Name())
		// An identifier must meet three conditions to become a return value of the
		// extracted function. (1) its value must be defined or reassigned within
		// the selection (isAssigned), (2) it must be used at least once after the
		// selection (isUsed), and (3) its first use after the selection
		// cannot be its own reassignment or redefinition (objOverriden).
		vscope := v.obj.Parent()
		if vscope == nil {
			// v.obj could be a field on an anonymous struct. We'll examine the
			// struct in a different iteration so don't return an error here.
			continue
		}
		isUsed, firstUseAfter := objUsed(info, end, vscope.End(), v.obj)
		if v.assigned && isUsed && !varOverridden(info, firstUseAfter, v.obj, v.free, outer) {
			returnTypes = append(returnTypes, &ast.Field{Type: typ})
			returns = append(returns, identifier)
			if !v.free {
				uninitialized = append(uninitialized, v.obj)

			} else {
				// In go1.22, Scope.Pos for function scopes changed (#60752):
				// it used to start at the body ('{'), now it starts at "func".
				//
				// The second condition below handles the case when
				// v's block is the FuncDecl.Body itself.
				startParent := curStart.Parent().Node()
				if vscope.Pos() == startParent.Pos() ||
					startParent == outer.Body && vscope == info.Scopes[outer.Type] {
					canRedefineCount++
				}
			}
		}
		// An identifier must meet two conditions to become a parameter of the
		// extracted function. (1) it must be free (isFree), and (2) its first
		// use within the selection cannot be its own definition (isDefined).
		if v.free && !v.defined {
			// Skip the selector for a method.
			if isMethod && v.obj == receiverObj {
				receiverUsed = true
				continue
			}
			params = append(params, identifier)
			paramTypes = append(paramTypes, &ast.Field{
				Names: []*ast.Ident{identifier},
				Type:  typ,
			})
		}
	}

	reorderParams(params, paramTypes)

	// Find the function literal that encloses the selection. The enclosing function literal
	// may not be the enclosing function declaration (i.e. 'outer'). For example, in the
	// following block:
	//
	// func main() {
	//     ast.Inspect(node, func(n ast.Node) bool {
	//         v := 1 // this line extracted
	//         return true
	//     })
	// }
	//
	// 'outer' is main(). However, the extracted selection most directly belongs to
	// the anonymous function literal, the second argument of ast.Inspect(). We use the
	// enclosing function literal to determine the proper return types for return statements
	// within the selection. We still need the enclosing function declaration because this is
	// the top-level declaration. We inspect the top-level declaration to look for variables
	// as well as for code replacement.
	enclosing := outer.Type
	if funcLit, _ := cursorutil.FirstEnclosing[*ast.FuncLit](curEnclosing); funcLit != nil {
		enclosing = funcLit.Type
	}

	// We put the selection in a constructed file. We can then traverse and edit
	// the extracted selection without modifying the original AST.
	startOffset, endOffset, err := safetoken.Offsets(pgf.Tok, start, end)
	if err != nil {
		return nil, nil, err
	}
	selection := src[startOffset:endOffset]

	extractedBlock, extractedComments, err := parseStmts(fset, selection)
	if err != nil {
		return nil, nil, err
	}

	// We need to account for return statements in the selected block, as they will complicate
	// the logical flow of the extracted function. See the following example, where ** denotes
	// the range to be extracted.
	//
	// Before:
	//
	// func _() int {
	//     a := 1
	//     b := 2
	//     **if a == b {
	//         return a
	//     }**
	//     ...
	// }
	//
	// After:
	//
	// func _() int {
	//     a := 1
	//     b := 2
	//     cond0, ret0 := x0(a, b)
	//     if cond0 {
	//         return ret0
	//     }
	//     ...
	// }
	//
	// func x0(a int, b int) (bool, int) {
	//     if a == b {
	//         return true, a
	//     }
	//     return false, 0
	// }
	//
	// We handle returns by adding an additional boolean return value to the extracted function.
	// This bool reports whether the original function would have returned. Because the
	// extracted selection contains a return statement, we must also add the types in the
	// return signature of the enclosing function to the return signature of the
	// extracted function. We then add an extra if statement checking this boolean value
	// in the original function. If the condition is met, the original function should
	// return a value, mimicking the functionality of the original return statement(s)
	// in the selection.
	//
	// If there is a return that is guaranteed to execute (hasNonNestedReturns=true), then
	// we don't need to include this additional condition check and can simply return.
	//
	// Before:
	//
	// func _() int {
	//     a := 1
	//     b := 2
	//     **if a == b {
	//         return a
	//     }
	//	   return b**
	// }
	//
	// After:
	//
	// func _() int {
	//     a := 1
	//     b := 2
	//     return x0(a, b)
	// }
	//
	// func x0(a int, b int) int {
	//     if a == b {
	//         return a
	//     }
	//     return b
	// }

	var retVars []*returnVariable
	var ifReturn *ast.IfStmt

	// Determine if the extracted block contains any free branch statements, for
	// example: "continue label" where "label" is declared outside of the
	// extracted block, or continue inside a "for" statement where the for
	// statement is declared outside of the extracted block. These will be
	// handled below, after adjusting return statements and generating return
	// info.
	curSel, _ := pgf.Cursor.FindByPos(start, end) // since canExtractFunction succeeded, this will always return a valid cursor
	freeBranches := freeBranches(info, curSel, start, end)

	// All return statements in the extracted block are error handling returns, and there are no free control statements.
	isErrHandlingReturnsCase := allReturnsFinalErr && len(freeBranches) == 0

	if hasReturn {
		if !hasNonNestedReturn {
			// The selected block contained return statements, so we have to modify the
			// signature of the extracted function as described above. Adjust all of
			// the return statements in the extracted function to reflect this change in
			// signature.
			if err := adjustReturnStatements(returnTypes, seenVars, extractedBlock, qual, isErrHandlingReturnsCase); err != nil {
				return nil, nil, err
			}
		}
		// Collect the additional return values and types needed to accommodate return
		// statements in the selection. Update the type signature of the extracted
		// function and construct the if statement that will be inserted in the enclosing
		// function.
		retVars, ifReturn, err = generateReturnInfo(enclosing, pkg, curEnclosing.Node().Pos(), file, info, start, end, hasNonNestedReturn, isErrHandlingReturnsCase)
		if err != nil {
			return nil, nil, err
		}
	}

	// If the extracted block contains free branch statements, we add another
	// return value "ctrl" to the extracted function that will be used to
	// determine the control flow. See the following example, where === denotes
	// the range to be extracted.
	//
	// Before:
	// func f(cond bool) {
	//      for range "abc" {
	//      ==============
	//      if cond {
	//          continue
	//      }
	//      ==============
	//      println(0)
	//      }
	// }

	// After:
	// func f(cond bool) {
	//      for range "abc" {
	//      ctrl := newFunction(cond)
	//      switch ctrl {
	//      case 1:
	//          continue
	//      }
	//      println(0)
	//      }
	// }
	//
	// func newFunction(cond bool) int {
	//      if cond {
	//          return 1
	//      }
	//      return 0
	// }
	//

	// Generate an unused identifier for the control value.
	ctrlVar, _ := freshName(info, file, start, "ctrl", 0)
	if len(freeBranches) > 0 {

		zeroValExpr := &ast.BasicLit{
			Kind:  token.INT,
			Value: "0",
		}
		var branchStmts []*ast.BranchStmt
		var stack []ast.Node
		// Add the zero "ctrl" value to each return statement in the extracted block.
		ast.Inspect(extractedBlock, func(n ast.Node) bool {
			if n != nil {
				stack = append(stack, n)
			} else {
				stack = stack[:len(stack)-1]
			}
			switch n := n.(type) {
			case *ast.ReturnStmt:
				n.Results = append(n.Results, zeroValExpr)
			case *ast.BranchStmt:
				// Collect a list of branch statements in the extracted block to examine later.
				if isFreeBranchStmt(stack) {
					branchStmts = append(branchStmts, n)
				}
			case *ast.FuncLit:
				// Don't descend into nested functions. When we return false
				// here, ast.Inspect does not give us a "pop" event when leaving
				// the subtree, so we need to pop here. (golang/go#73319)
				stack = stack[:len(stack)-1]
				return false
			}
			return true
		})

		// Construct a return statement to replace each free branch statement in the extracted block. It should have
		// zero values for all return parameters except one, "ctrl", which dictates which continuation to follow.
		var freeCtrlStmtReturns []ast.Expr
		// Create "zero values" for each type.
		for _, returnType := range returnTypes {
			var val ast.Expr
			var isValid bool
			for obj, typ := range seenVars {
				if typ == returnType.Type {
					val, isValid = typesinternal.ZeroExpr(obj.Type(), qual)
					break
				}
			}
			if !isValid {
				return nil, nil, fmt.Errorf("could not find matching AST expression for %T", returnType.Type)
			}
			freeCtrlStmtReturns = append(freeCtrlStmtReturns, val)
		}
		freeCtrlStmtReturns = append(freeCtrlStmtReturns, getZeroVals(retVars)...)

		for i, branchStmt := range branchStmts {
			replaceBranchStmtWithReturnStmt(extractedBlock, branchStmt, &ast.ReturnStmt{
				Return: branchStmt.Pos(),
				Results: append(slices.Clip(freeCtrlStmtReturns), &ast.BasicLit{
					Kind:  token.INT,
					Value: strconv.Itoa(i + 1), // start with 1 because 0 is reserved for base case
				}),
			})

		}
		retVars = append(retVars, &returnVariable{
			name:    ast.NewIdent(ctrlVar),
			decl:    &ast.Field{Type: ast.NewIdent("int")},
			zeroVal: zeroValExpr,
		})
	}

	// Add a return statement to the end of the new function. This return statement must include
	// the values for the types of the original extracted function signature and (if a return
	// statement is present in the selection) enclosing function signature.
	// This only needs to be done if the selections does not have a non-nested return, otherwise
	// it already terminates with a return statement.
	hasReturnValues := len(returns)+len(retVars) > 0
	if hasReturnValues && !hasNonNestedReturn {
		extractedBlock.List = append(extractedBlock.List, &ast.ReturnStmt{
			Results: append(returns, getZeroVals(retVars)...),
		})
	}

	// Construct the appropriate call to the extracted function.
	// We must meet two conditions to use ":=" instead of '='. (1) there must be at least
	// one variable on the lhs that is uninitialized (non-free) prior to the assignment.
	// (2) all of the initialized (free) variables on the lhs must be able to be redefined.
	sym := token.ASSIGN
	canDefineCount := len(uninitialized) + canRedefineCount
	canDefine := len(uninitialized)+len(retVars) > 0 && canDefineCount == len(returns)
	if canDefine {
		sym = token.DEFINE
	}
	var funName string
	if isMethod {
		// TODO(suzmue): generate a name that does not conflict for "newMethod".
		funName = "newMethod"
	} else {
		funName, _ = freshName(info, file, start, "newFunction", 0)
	}
	extractedFunCall := generateFuncCall(hasNonNestedReturn, hasReturnValues, params,
		append(returns, getNames(retVars)...), funName, sym, receiverName)

	// Create variable declarations for any identifiers that need to be initialized prior to
	// calling the extracted function. We do not manually initialize variables if every return
	// value is uninitialized. We can use := to initialize the variables in this situation.
	var declarations []ast.Stmt
	if canDefineCount != len(returns) {
		declarations = initializeVars(uninitialized, retVars, seenUninitialized, seenVars)
	}

	var declBuf, replaceBuf, newFuncBuf, ifBuf, commentBuf bytes.Buffer
	if err := format.Node(&declBuf, fset, declarations); err != nil {
		return nil, nil, err
	}
	if err := format.Node(&replaceBuf, fset, extractedFunCall); err != nil {
		return nil, nil, err
	}
	if ifReturn != nil {
		if isErrHandlingReturnsCase {
			errName := retVars[len(retVars)-1]
			fmt.Fprintf(&ifBuf, "if %s != nil ", errName.name.String())
			if err := format.Node(&ifBuf, fset, ifReturn.Body); err != nil {
				return nil, nil, err
			}
		} else {
			if err := format.Node(&ifBuf, fset, ifReturn); err != nil {
				return nil, nil, err
			}
		}
	}

	// Build the extracted function. We format the function declaration and body
	// separately, so that comments are printed relative to the extracted
	// BlockStmt.
	//
	// In other words, extractedBlock and extractedComments were parsed from a
	// synthetic function declaration of the form func _() { ... }. If we now
	// print the real function declaration, the length of the signature will have
	// grown, causing some comment positions to be computed as inside the
	// signature itself.
	newFunc := &ast.FuncDecl{
		Name: ast.NewIdent(funName),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: paramTypes},
			Results: &ast.FieldList{List: append(returnTypes, getDecls(retVars)...)},
		},
		// Body handled separately -- see above.
	}
	if isMethod {
		var names []*ast.Ident
		if receiverUsed {
			names = append(names, ast.NewIdent(receiverName))
		}
		newFunc.Recv = &ast.FieldList{
			List: []*ast.Field{{
				Names: names,
				Type:  receiver.Type,
			}},
		}
	}
	if err := format.Node(&newFuncBuf, fset, newFunc); err != nil {
		return nil, nil, err
	}
	// Write a space between the end of the function signature and opening '{'.
	if err := newFuncBuf.WriteByte(' '); err != nil {
		return nil, nil, err
	}
	commentedNode := &printer.CommentedNode{
		Node:     extractedBlock,
		Comments: extractedComments,
	}
	if err := format.Node(&newFuncBuf, fset, commentedNode); err != nil {
		return nil, nil, err
	}

	// We're going to replace the whole enclosing function,
	// so preserve the text before and after the selected block.
	outerStart, outerEnd, err := pgf.NodeOffsets(outer)
	if err != nil {
		return nil, nil, err
	}
	before := src[outerStart:startOffset]
	after := src[endOffset:outerEnd]
	indent, err := pgf.Indentation(start)
	if err != nil {
		return nil, nil, err
	}
	newLineIndent := "\n" + indent

	var fullReplacement strings.Builder
	fullReplacement.Write(before)
	if commentBuf.Len() > 0 {
		comments := strings.ReplaceAll(commentBuf.String(), "\n", newLineIndent)
		fullReplacement.WriteString(comments)
	}
	if declBuf.Len() > 0 { // add any initializations, if needed
		initializations := strings.ReplaceAll(declBuf.String(), "\n", newLineIndent) +
			newLineIndent
		fullReplacement.WriteString(initializations)
	}
	fullReplacement.Write(replaceBuf.Bytes()) // call the extracted function
	if ifBuf.Len() > 0 {                      // add the if statement below the function call, if needed
		ifstatement := newLineIndent +
			strings.ReplaceAll(ifBuf.String(), "\n", newLineIndent)
		fullReplacement.WriteString(ifstatement)
	}

	// Add the switch statement for free branch statements after the new function call.
	if len(freeBranches) > 0 {
		fmt.Fprintf(&fullReplacement, "%[1]sswitch %[2]s {%[1]s", newLineIndent, ctrlVar)
		for i, br := range freeBranches {
			// Preserve spacing at the beginning of the line containing the branch statement.
			startPos := pgf.Tok.LineStart(safetoken.Line(pgf.Tok, br.Pos()))
			text, err := pgf.PosText(startPos, br.End())
			if err != nil {
				return nil, nil, err
			}
			fmt.Fprintf(&fullReplacement, "case %d:\n%s%s", i+1, text, newLineIndent)
		}
		fullReplacement.WriteString("}")
	}

	fullReplacement.Write(after)
	fullReplacement.WriteString("\n\n")       // add newlines after the enclosing function
	fullReplacement.Write(newFuncBuf.Bytes()) // insert the extracted function

	return fset, &analysis.SuggestedFix{
		TextEdits: []analysis.TextEdit{{
			Pos:     outer.Pos(),
			End:     outer.End(),
			NewText: []byte(fullReplacement.String()),
		}},
	}, nil
}

// isSelector reports if e is the selector expr <x>, <sel>. It works for pointer and non-pointer selector expressions.
func isSelector(e ast.Expr, x, sel string) bool {
	unary, ok := e.(*ast.UnaryExpr)
	if ok && unary.Op == token.MUL {
		e = unary.X
	}
	selectorExpr, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selectorExpr.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == x && selectorExpr.Sel.Name == sel
}

// reorderParams reorders the given parameters in-place to follow common Go conventions.
func reorderParams(params []ast.Expr, paramTypes []*ast.Field) {
	moveParamToFrontIfFound(params, paramTypes, "testing", "T")
	moveParamToFrontIfFound(params, paramTypes, "testing", "B")
	moveParamToFrontIfFound(params, paramTypes, "context", "Context")
}

func moveParamToFrontIfFound(params []ast.Expr, paramTypes []*ast.Field, x, sel string) {
	// Move Context parameter (if any) to front.
	for i, t := range paramTypes {
		if isSelector(t.Type, x, sel) {
			p, t := params[i], paramTypes[i]
			copy(params[1:], params[:i])
			copy(paramTypes[1:], paramTypes[:i])
			params[0], paramTypes[0] = p, t
			break
		}
	}
}

// variable describes the status of a variable within a selection.
type variable struct {
	obj types.Object

	// free reports whether the variable is a free variable, meaning it should
	// be a parameter to the extracted function.
	free bool

	// assigned reports whether the variable is assigned to in the selection.
	assigned bool

	// defined reports whether the variable is defined in the selection.
	defined bool
}

// collectFreeVars maps each identifier in the given range to whether it is "free."
// Given a range, a variable in that range is defined as "free" if it is declared
// outside of the range and neither at the file scope nor package scope. These free
// variables will be used as arguments in the extracted function. It also returns a
// list of identifiers that may need to be returned by the extracted function.
// Some of the code in this function has been adapted from tools/cmd/guru/freevars.go.
func collectFreeVars(info *types.Info, file *ast.File, start, end token.Pos, node ast.Node) ([]*variable, error) {
	fileScope := info.Scopes[file]
	if fileScope == nil {
		return nil, bug.Errorf("file scope is empty")
	}
	pkgScope := fileScope.Parent()
	if pkgScope == nil {
		return nil, bug.Errorf("package scope is empty")
	}
	// id returns non-nil if n denotes an object that is referenced by the span
	// and defined either within the span or in the lexical environment. The bool
	// return value acts as an indicator for where it was defined.
	id := func(n *ast.Ident) (types.Object, bool) {
		obj := info.Uses[n]
		if obj == nil {
			return info.Defs[n], false
		}
		if obj.Name() == "_" {
			return nil, false // exclude objects denoting '_'
		}
		if _, ok := obj.(*types.PkgName); ok {
			return nil, false // imported package
		}
		if !(file.FileStart <= obj.Pos() && obj.Pos() <= file.FileEnd) {
			return nil, false // not defined in this file
		}
		scope := obj.Parent()
		if scope == nil {
			return nil, false // e.g. interface method, struct field
		}
		if scope == fileScope || scope == pkgScope {
			return nil, false // defined at file or package scope
		}
		if start <= obj.Pos() && obj.Pos() <= end {
			return obj, false // defined within selection => not free
		}
		return obj, true
	}
	// sel returns non-nil if n denotes a selection o.x.y that is referenced by the
	// span and defined either within the span or in the lexical environment. The bool
	// return value acts as an indicator for where it was defined.
	var sel func(n *ast.SelectorExpr) (types.Object, bool)
	sel = func(n *ast.SelectorExpr) (types.Object, bool) {
		switch x := ast.Unparen(n.X).(type) {
		case *ast.SelectorExpr:
			return sel(x)
		case *ast.Ident:
			return id(x)
		}
		return nil, false
	}
	seen := make(map[types.Object]*variable)
	firstUseIn := make(map[types.Object]token.Pos)
	var vars []types.Object
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if start <= n.Pos() && n.End() <= end {
			var obj types.Object
			var isFree, prune bool
			switch n := n.(type) {
			case *ast.BranchStmt:
				// Avoid including labels attached to branch statements.
				return false
			case *ast.Ident:
				obj, isFree = id(n)
			case *ast.SelectorExpr:
				obj, isFree = sel(n)
				prune = true
			}
			if obj != nil {
				seen[obj] = &variable{
					obj:  obj,
					free: isFree,
				}
				vars = append(vars, obj)
				// Find the first time that the object is used in the selection.
				first, ok := firstUseIn[obj]
				if !ok || n.Pos() < first {
					firstUseIn[obj] = n.Pos()
				}
				if prune {
					return false
				}
			}
		}
		return n.Pos() <= end
	})

	// Find identifiers that are initialized or whose values are altered at some
	// point in the selected block. For example, in a selected block from lines 2-4,
	// variables x, y, and z are included in assigned. However, in a selected block
	// from lines 3-4, only variables y and z are included in assigned.
	//
	// 1: var a int
	// 2: var x int
	// 3: y := 3
	// 4: z := x + a
	//
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if n.Pos() < start || n.End() > end {
			return n.Pos() <= end
		}
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, assignment := range n.Lhs {
				lhs, ok := assignment.(*ast.Ident)
				if !ok {
					continue
				}
				obj, _ := id(lhs)
				if obj == nil {
					continue
				}
				if _, ok := seen[obj]; !ok {
					continue
				}
				seen[obj].assigned = true
				if n.Tok != token.DEFINE {
					continue
				}
				// Find identifiers that are defined prior to being used
				// elsewhere in the selection.
				// TODO: Include identifiers that are assigned prior to being
				// used elsewhere in the selection. Then, change the assignment
				// to a definition in the extracted function.
				if firstUseIn[obj] != lhs.Pos() {
					continue
				}
				// Ensure that the object is not used in its own re-definition.
				// For example:
				// var f float64
				// f, e := math.Frexp(f)
				for _, expr := range n.Rhs {
					if referencesObj(info, expr, obj) {
						continue
					}
					if _, ok := seen[obj]; !ok {
						continue
					}
					seen[obj].defined = true
					break
				}
			}
			return false
		case *ast.DeclStmt:
			gen, ok := n.Decl.(*ast.GenDecl)
			if !ok {
				return false
			}
			for _, spec := range gen.Specs {
				vSpecs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, vSpec := range vSpecs.Names {
					obj, _ := id(vSpec)
					if obj == nil {
						continue
					}
					if _, ok := seen[obj]; !ok {
						continue
					}
					seen[obj].assigned = true
				}
			}
			return false
		case *ast.IncDecStmt:
			if ident, ok := n.X.(*ast.Ident); !ok {
				return false
			} else if obj, _ := id(ident); obj == nil {
				return false
			} else {
				if _, ok := seen[obj]; !ok {
					return false
				}
				seen[obj].assigned = true
			}
		}
		return true
	})
	var variables []*variable
	for _, obj := range vars {
		v, ok := seen[obj]
		if !ok {
			return nil, fmt.Errorf("no seen types.Object for %v", obj)
		}
		if named, ok := v.obj.Type().(typesinternal.NamedOrAlias); ok {
			namedPos := named.Obj().Pos()
			if isLocal(named.Obj()) && !(start <= namedPos && namedPos <= end) {
				return nil, fmt.Errorf("Cannot extract selection: the code refers to a local type whose definition lies outside the extracted block")
			}
		}
		variables = append(variables, v)
	}
	return variables, nil
}

// referencesObj checks whether the given object appears in the given expression.
func referencesObj(info *types.Info, expr ast.Expr, obj types.Object) bool {
	var hasObj bool
	ast.Inspect(expr, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		objUse := info.Uses[ident]
		if obj == objUse {
			hasObj = true
			return false
		}
		return false
	})
	return hasObj
}

type fnExtractParams struct {
	curStart, curEnd inspector.Cursor // first and last nodes wholly enclosed by selection
	curEnclosing     inspector.Cursor // node that encloses selection (e.g. a BlockStmt)
	curFuncDecl      inspector.Cursor // enclosing *ast.FuncDecl
}

// canExtractFunction reports whether the code in the given range can be
// extracted to a function.
func canExtractFunction(curFile inspector.Cursor, start, end token.Pos) (*fnExtractParams, bool, bool, error) {
	if start == end {
		return nil, false, false, fmt.Errorf("start and end are equal")
	}

	curEnclosing, curStart, curEnd, err := astutil.Select(curFile, start, end)
	if err != nil {
		return nil, false, false, err
	}

	// Node that encloses the selection must be a statement.
	// TODO: Support function extraction for an expression.
	if !is[ast.Stmt](curEnclosing.Node()) {
		return nil, false, false, fmt.Errorf("node is not a statement")
	}

	// Find the function declaration that encloses the selection.
	funcDecl, curFuncDecl := cursorutil.FirstEnclosing[*ast.FuncDecl](curEnclosing)
	if funcDecl == nil {
		return nil, false, false, fmt.Errorf("no enclosing function")
	}

	// If the selection is a block statement, use its first and last statements.
	// «{ ... }»  =>  { «...» }
	if is[*ast.BlockStmt](curStart.Node()) && curStart == curEnd {
		var (
			first, ok1 = curStart.FirstChild()
			last, ok2  = curStart.LastChild()
		)
		if !(ok1 && ok2) {
			return nil, false, false, fmt.Errorf("range maps to empty block statement")
		}
		curStart = first
		curEnd = last
	}

	return &fnExtractParams{
		curStart:     curStart,
		curEnd:       curEnd,
		curEnclosing: curEnclosing,
		curFuncDecl:  curFuncDecl,
	}, true, funcDecl.Recv != nil, nil
}

// objUsed checks if the object is used within the range. It returns the first
// occurrence of the object in the range, if it exists.
func objUsed(info *types.Info, start, end token.Pos, obj types.Object) (bool, *ast.Ident) {
	var firstUse *ast.Ident
	for id, objUse := range info.Uses {
		if obj != objUse {
			continue
		}
		if id.Pos() < start || id.End() > end {
			continue
		}
		if firstUse == nil || id.Pos() < firstUse.Pos() {
			firstUse = id
		}
	}
	return firstUse != nil, firstUse
}

// varOverridden traverses the given AST node until we find the given identifier. Then, we
// examine the occurrence of the given identifier and check for (1) whether the identifier
// is being redefined. If the identifier is free, we also check for (2) whether the identifier
// is being reassigned. We will not include an identifier in the return statement of the
// extracted function if it meets one of the above conditions.
func varOverridden(info *types.Info, firstUse *ast.Ident, obj types.Object, isFree bool, node ast.Node) bool {
	var isOverriden bool
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		assignment, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		// A free variable is initialized prior to the selection. We can always reassign
		// this variable after the selection because it has already been defined.
		// Conversely, a non-free variable is initialized within the selection. Thus, we
		// cannot reassign this variable after the selection unless it is initialized and
		// returned by the extracted function.
		if !isFree && assignment.Tok == token.ASSIGN {
			return false
		}
		for _, assigned := range assignment.Lhs {
			ident, ok := assigned.(*ast.Ident)
			// Check if we found the first use of the identifier.
			if !ok || ident != firstUse {
				continue
			}
			objUse := info.Uses[ident]
			if objUse == nil || objUse != obj {
				continue
			}
			// Ensure that the object is not used in its own definition.
			// For example:
			// var f float64
			// f, e := math.Frexp(f)
			for _, expr := range assignment.Rhs {
				if referencesObj(info, expr, obj) {
					return false
				}
			}
			isOverriden = true
			return false
		}
		return false
	})
	return isOverriden
}

// parseStmts parses the specified source (a list of statements) and
// returns them as a BlockStmt along with any associated comments.
func parseStmts(fset *token.FileSet, src []byte) (*ast.BlockStmt, []*ast.CommentGroup, error) {
	text := "package main\nfunc _() { " + string(src) + " }"
	file, err := parser.ParseFile(fset, "", text, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}
	if len(file.Decls) != 1 {
		return nil, nil, fmt.Errorf("got %d declarations, want 1", len(file.Decls))
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		return nil, nil, bug.Errorf("parsed file does not contain expected function declaration")
	}
	if decl.Body == nil {
		return nil, nil, bug.Errorf("extracted function has no body")
	}
	return decl.Body, file.Comments, nil
}

// generateReturnInfo generates the information we need to adjust the return statements and
// signature of the extracted function. We prepare names, signatures, and "zero values" that
// represent the new variables. We also use this information to construct the if statement that
// is inserted below the call to the extracted function.
func generateReturnInfo(enclosing *ast.FuncType, pkg *types.Package, at token.Pos, file *ast.File, info *types.Info, start, end token.Pos, hasNonNestedReturns bool, isErrHandlingReturnsCase bool) ([]*returnVariable, *ast.IfStmt, error) {
	var retVars []*returnVariable
	var cond *ast.Ident
	// Generate information for the values in the return signature of the enclosing function.
	if enclosing.Results != nil {
		nameIdx := make(map[string]int) // last integral suffixes of generated names
		qual := typesinternal.FileQualifier(file, pkg)
		for _, field := range enclosing.Results.List {
			typ := info.TypeOf(field.Type)
			if typ == nil {
				return nil, nil, fmt.Errorf(
					"failed type conversion, AST expression: %T", field.Type)
			}
			names := []string{""}
			if len(field.Names) > 0 {
				names = nil
				for _, n := range field.Names {
					names = append(names, n.Name)
				}
			}
			for _, name := range names {
				bestName := "result"
				if name != "" && name != "_" {
					bestName = name
				} else if n, ok := varNameForType(typ); ok {
					bestName = n
				}
				retName, idx := freshNameOutsideRange(info, file, at, start, end, bestName, nameIdx[bestName])
				nameIdx[bestName] = idx
				z, isValid := typesinternal.ZeroExpr(typ, qual)
				if !isValid {
					return nil, nil, fmt.Errorf("can't generate zero value for %T", typ)
				}
				retVars = append(retVars, &returnVariable{
					name:    ast.NewIdent(retName),
					decl:    &ast.Field{Type: typesinternal.TypeExpr(typ, qual)},
					zeroVal: z,
				})
			}
		}
	}
	var ifReturn *ast.IfStmt
	if !hasNonNestedReturns {
		results := getNames(retVars)
		if !isErrHandlingReturnsCase {
			// Generate information for the added bool value.
			name, _ := freshNameOutsideRange(info, file, at, start, end, "shouldReturn", 0)
			cond = &ast.Ident{Name: name}
			retVars = append(retVars, &returnVariable{
				name:    cond,
				decl:    &ast.Field{Type: ast.NewIdent("bool")},
				zeroVal: ast.NewIdent("false"),
			})
		}
		ifReturn = &ast.IfStmt{
			Cond: cond,
			Body: &ast.BlockStmt{
				List: []ast.Stmt{&ast.ReturnStmt{Results: results}},
			},
		}
	}
	return retVars, ifReturn, nil
}

type objKey struct{ pkg, name string }

// conventionalVarNames specifies conventional names for variables with various
// standard library types.
//
// Keep this up to date with completion.conventionalAcronyms.
//
// TODO(rfindley): consider factoring out a "conventions" library.
var conventionalVarNames = map[objKey]string{
	{"", "error"}:              "err",
	{"context", "Context"}:     "ctx",
	{"sql", "Tx"}:              "tx",
	{"http", "ResponseWriter"}: "rw", // Note: same as [AbbreviateVarName].
}

// varNameForType chooses a "good" name for a variable with the given type,
// if possible. Otherwise, it returns "", false.
//
// For special types, it uses known conventional names.
func varNameForType(t types.Type) (string, bool) {
	tname := typesinternal.TypeNameFor(t)
	if tname == nil {
		return "", false
	}

	// Have Alias, Basic, Named, or TypeParam.
	k := objKey{name: tname.Name()}
	if tname.Pkg() != nil {
		k.pkg = tname.Pkg().Name()
	}
	if name, ok := conventionalVarNames[k]; ok {
		return name, true
	}

	return AbbreviateVarName(tname.Name()), true
}

// adjustReturnStatements adds "zero values" of the given types to each return
// statement in the given AST node.
func adjustReturnStatements(returnTypes []*ast.Field, seenVars map[types.Object]ast.Expr, extractedBlock *ast.BlockStmt, qual types.Qualifier, isErrHandlingReturnsCase bool) error {
	var zeroVals []ast.Expr
	// Create "zero values" for each type.
	for _, returnType := range returnTypes {
		var val ast.Expr
		var isValid bool
		for obj, typ := range seenVars {
			if typ == returnType.Type {
				val, isValid = typesinternal.ZeroExpr(obj.Type(), qual)
				break
			}
		}
		if !isValid {
			return fmt.Errorf("could not find matching AST expression for %T", returnType.Type)
		}
		zeroVals = append(zeroVals, val)
	}
	// Add "zero values" to each return statement.
	// The bool reports whether the enclosing function should return after calling the
	// extracted function. We set the bool to 'true' because, if these return statements
	// execute, the extracted function terminates early, and the enclosing function must
	// return as well.
	var shouldReturnCond []ast.Expr
	if !isErrHandlingReturnsCase {
		shouldReturnCond = append(shouldReturnCond, ast.NewIdent("true"))
	}

	ast.Inspect(extractedBlock, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		// Don't modify return statements inside anonymous functions.
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if n, ok := n.(*ast.ReturnStmt); ok {
			n.Results = slices.Concat(zeroVals, n.Results, shouldReturnCond)
			return false
		}
		return true
	})
	return nil
}

// generateFuncCall constructs a call expression for the extracted function, described by the
// given parameters and return variables.
func generateFuncCall(hasNonNestedReturn, hasReturnVals bool, params, returns []ast.Expr, name string, token token.Token, selector string) ast.Node {
	var replace ast.Node
	callExpr := &ast.CallExpr{
		Fun:  ast.NewIdent(name),
		Args: params,
	}
	if selector != "" {
		callExpr = &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent(selector),
				Sel: ast.NewIdent(name),
			},
			Args: params,
		}
	}
	if hasReturnVals {
		if hasNonNestedReturn {
			// Create a return statement that returns the result of the function call.
			replace = &ast.ReturnStmt{
				Return:  0,
				Results: []ast.Expr{callExpr},
			}
		} else {
			// Assign the result of the function call.
			replace = &ast.AssignStmt{
				Lhs: returns,
				Tok: token,
				Rhs: []ast.Expr{callExpr},
			}
		}
	} else {
		replace = callExpr
	}
	return replace
}

// initializeVars creates variable declarations, if needed.
// Our preference is to replace the selected block with an "x, y, z := fn()" style
// assignment statement. We can use this style when all of the variables in the
// extracted function's return statement are either not defined prior to the extracted block
// or can be safely redefined. However, for example, if z is already defined
// in a different scope, we replace the selected block with:
//
// var x int
// var y string
// x, y, z = fn()
func initializeVars(uninitialized []types.Object, retVars []*returnVariable, seenUninitialized map[types.Object]struct{}, seenVars map[types.Object]ast.Expr) []ast.Stmt {
	var declarations []ast.Stmt
	for _, obj := range uninitialized {
		if _, ok := seenUninitialized[obj]; ok {
			continue
		}
		seenUninitialized[obj] = struct{}{}
		valSpec := &ast.ValueSpec{
			Names: []*ast.Ident{ast.NewIdent(obj.Name())},
			Type:  seenVars[obj],
		}
		genDecl := &ast.GenDecl{
			Tok:   token.VAR,
			Specs: []ast.Spec{valSpec},
		}
		declarations = append(declarations, &ast.DeclStmt{Decl: genDecl})
	}
	// Each variable added from a return statement in the selection
	// must be initialized.
	for i, retVar := range retVars {
		valSpec := &ast.ValueSpec{
			Names: []*ast.Ident{retVar.name},
			Type:  retVars[i].decl.Type,
		}
		genDecl := &ast.GenDecl{
			Tok:   token.VAR,
			Specs: []ast.Spec{valSpec},
		}
		declarations = append(declarations, &ast.DeclStmt{Decl: genDecl})
	}
	return declarations
}

// getNames returns the names from the given list of returnVariable.
func getNames(retVars []*returnVariable) []ast.Expr {
	var names []ast.Expr
	for _, retVar := range retVars {
		names = append(names, retVar.name)
	}
	return names
}

// getZeroVals returns the "zero values" from the given list of returnVariable.
func getZeroVals(retVars []*returnVariable) []ast.Expr {
	var zvs []ast.Expr
	for _, retVar := range retVars {
		zvs = append(zvs, retVar.zeroVal)
	}
	return zvs
}

// getDecls returns the declarations from the given list of returnVariable.
func getDecls(retVars []*returnVariable) []*ast.Field {
	var decls []*ast.Field
	for _, retVar := range retVars {
		decls = append(decls, retVar.decl)
	}
	return decls
}

func cond[T any](cond bool, t, f T) T {
	if cond {
		return t
	} else {
		return f
	}
}

// replaceBranchStmtWithReturnStmt modifies the ast node to replace the given
// branch statement with the given return statement.
func replaceBranchStmtWithReturnStmt(block ast.Node, br *ast.BranchStmt, ret *ast.ReturnStmt) {
	ast.Inspect(block, func(n ast.Node) bool {
		// Look for the branch statement within a BlockStmt or CaseClause.
		switch n := n.(type) {
		case *ast.BlockStmt:
			for i, stmt := range n.List {
				if stmt == br {
					n.List[i] = ret
					return false
				}
			}
		case *ast.CaseClause:
			for i, stmt := range n.Body {
				if stmt.Pos() == br.Pos() {
					n.Body[i] = ret
					return false
				}
			}
		}
		return true
	})
}

// freeBranches returns all branch statements beneath cur whose continuation
// lies outside the (start, end) range.
func freeBranches(info *types.Info, cur inspector.Cursor, start, end token.Pos) (free []*ast.BranchStmt) {
nextBranch:
	for curBr := range cur.Preorder((*ast.BranchStmt)(nil)) {
		br := curBr.Node().(*ast.BranchStmt)
		if br.End() < start || br.Pos() > end {
			continue
		}
		label, _ := info.Uses[br.Label].(*types.Label)
		if label != nil && !(start <= label.Pos() && label.Pos() <= end) {
			free = append(free, br)
			continue
		}
		if br.Tok == token.BREAK || br.Tok == token.CONTINUE {
			filter := []ast.Node{
				(*ast.ForStmt)(nil),
				(*ast.RangeStmt)(nil),
				(*ast.SwitchStmt)(nil),
				(*ast.TypeSwitchStmt)(nil),
				(*ast.SelectStmt)(nil),
			}
			// Find innermost relevant ancestor for break/continue.
			for curAncestor := range curBr.Parent().Enclosing(filter...) {
				if l, ok := curAncestor.Parent().Node().(*ast.LabeledStmt); ok &&
					label != nil &&
					l.Label.Name == label.Name() {
					continue
				}
				switch n := curAncestor.Node().(type) {
				case *ast.ForStmt, *ast.RangeStmt:
					if n.Pos() < start {
						free = append(free, br)
					}
					continue nextBranch
				case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
					if br.Tok == token.BREAK {
						if n.Pos() < start {
							free = append(free, br)
						}
						continue nextBranch
					}
				}
			}
		}
	}
	return
}

// isFreeBranchStmt returns true if the relevant ancestor for the branch
// statement at stack[len(stack)-1] cannot be found in the stack. This is used
// when we are examining the extracted block, since type information isn't
// available. We need to find the location of the label without using
// types.Info.
func isFreeBranchStmt(stack []ast.Node) bool {
	switch node := stack[len(stack)-1].(type) {
	case *ast.BranchStmt:
		isLabeled := node.Label != nil
		switch node.Tok {
		case token.GOTO:
			if isLabeled {
				return !enclosingLabel(stack, node.Label.Name)
			}
		case token.BREAK, token.CONTINUE:
			// Find innermost relevant ancestor for break/continue.
			for i := len(stack) - 2; i >= 0; i-- {
				n := stack[i]
				if isLabeled {
					l, ok := n.(*ast.LabeledStmt)
					if !(ok && l.Label.Name == node.Label.Name) {
						continue
					}
				}
				switch n.(type) {
				case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
					return false
				}
			}
		}
	}
	// We didn't find the relevant ancestor on the path, so this must be a free branch statement.
	return true
}

// enclosingLabel returns true if the given label is found on the stack.
func enclosingLabel(stack []ast.Node, label string) bool {
	for _, n := range stack {
		if labelStmt, ok := n.(*ast.LabeledStmt); ok && labelStmt.Label.Name == label {
			return true
		}
	}
	return false
}
