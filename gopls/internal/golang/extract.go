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
	"sort"
	"strings"
	"text/scanner"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/analysisinternal"
	"golang.org/x/tools/internal/typesinternal"
)

// extractVariable implements the refactor.extract.{variable,constant} CodeAction command.
func extractVariable(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, pkg *types.Package, info *types.Info) (*token.FileSet, *analysis.SuggestedFix, error) {
	tokFile := fset.File(file.FileStart)
	expr, path, err := canExtractVariable(info, file, start, end)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot extract %s: %v", safetoken.StartPosition(fset, start), err)
	}
	constant := info.Types[expr].Value != nil

	// Generate name(s) for new declaration.
	baseName := cond(constant, "k", "x")
	var lhsNames []string
	switch expr := expr.(type) {
	case *ast.CallExpr:
		tup, ok := info.TypeOf(expr).(*types.Tuple)
		if !ok {
			// conversion or single-valued call:
			// treat it the same as our standard extract variable case.
			name, _ := freshName(info, file, expr.Pos(), baseName, 0)
			lhsNames = append(lhsNames, name)

		} else {
			// call with multiple results
			idx := 0
			for range tup.Len() {
				// Generate a unique variable for each result.
				var name string
				name, idx = freshName(info, file, expr.Pos(), baseName, idx)
				lhsNames = append(lhsNames, name)
			}
		}

	default:
		// TODO: stricter rules for selectorExpr.
		name, _ := freshName(info, file, expr.Pos(), baseName, 0)
		lhsNames = append(lhsNames, name)
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
	// TODO(golang/go#70563): Another bug: extracting the
	// expression to the recommended place may cause it to migrate
	// across one or more declarations that it references.
	//
	// Before:
	//   if x := 1; cond {
	//   } else if y := «x + 2»; cond {
	//   }
	//
	// After:
	//   x1 := x + 2 // error: undefined x
	//   if x := 1; cond {
	//   } else if y := x1; cond {
	//   }
	var (
		insertPos   token.Pos
		indentation string
		stmtOK      bool // ok to use ":=" instead of var/const decl?
	)
	if before := analysisinternal.StmtToInsertVarBefore(path); before != nil {
		// Within function: compute appropriate statement indentation.
		indent, err := calculateIndentation(src, tokFile, before)
		if err != nil {
			return nil, nil, err
		}
		insertPos = before.Pos()
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
		// declaration, without indentation.
		currentDecl := path[len(path)-2]
		insertPos = currentDecl.Pos()
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
					Values: []ast.Expr{expr},
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
			Rhs: []ast.Expr{expr},
		}
	}

	// Format and indent the declaration.
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, newNode); err != nil {
		return nil, nil, err
	}
	// TODO(adonovan): not sound for `...` string literals containing newlines.
	assignment := strings.ReplaceAll(buf.String(), "\n", indentation) + indentation

	return fset, &analysis.SuggestedFix{
		TextEdits: []analysis.TextEdit{
			{
				Pos:     insertPos,
				End:     insertPos,
				NewText: []byte(assignment),
			},
			{
				Pos:     start,
				End:     end,
				NewText: []byte(strings.Join(lhsNames, ", ")),
			},
		},
	}, nil
}

// canExtractVariable reports whether the code in the given range can be
// extracted to a variable (or constant).
func canExtractVariable(info *types.Info, file *ast.File, start, end token.Pos) (ast.Expr, []ast.Node, error) {
	if start == end {
		return nil, nil, fmt.Errorf("empty selection")
	}
	path, exact := astutil.PathEnclosingInterval(file, start, end)
	if !exact {
		return nil, nil, fmt.Errorf("selection is not an expression")
	}
	if len(path) == 0 {
		return nil, nil, bug.Errorf("no path enclosing interval")
	}
	for _, n := range path {
		if _, ok := n.(*ast.ImportSpec); ok {
			return nil, nil, fmt.Errorf("cannot extract variable or constant in an import block")
		}
	}
	expr, ok := path[0].(ast.Expr)
	if !ok {
		return nil, nil, fmt.Errorf("selection is not an expression") // e.g. statement
	}
	if tv, ok := info.Types[expr]; !ok || !tv.IsValue() || tv.Type == nil || tv.HasOk() {
		// e.g. type, builtin, x.(type), 2-valued m[k], or ill-typed
		return nil, nil, fmt.Errorf("selection is not a single-valued expression")
	}
	return expr, path, nil
}

// Calculate indentation for insertion.
// When inserting lines of code, we must ensure that the lines have consistent
// formatting (i.e. the proper indentation). To do so, we observe the indentation on the
// line of code on which the insertion occurs.
func calculateIndentation(content []byte, tok *token.File, insertBeforeStmt ast.Node) (string, error) {
	line := safetoken.Line(tok, insertBeforeStmt.Pos())
	lineOffset, stmtOffset, err := safetoken.Offsets(tok, tok.LineStart(line), insertBeforeStmt.Pos())
	if err != nil {
		return "", err
	}
	return string(content[lineOffset:stmtOffset]), nil
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
func extractMethod(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, pkg *types.Package, info *types.Info) (*token.FileSet, *analysis.SuggestedFix, error) {
	return extractFunctionMethod(fset, start, end, src, file, pkg, info, true)
}

// extractFunction refactors the selected block of code into a new function.
func extractFunction(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, pkg *types.Package, info *types.Info) (*token.FileSet, *analysis.SuggestedFix, error) {
	return extractFunctionMethod(fset, start, end, src, file, pkg, info, false)
}

// extractFunctionMethod refactors the selected block of code into a new function/method.
// It also replaces the selected block of code with a call to the extracted
// function. First, we manually adjust the selection range. We remove trailing
// and leading whitespace characters to ensure the range is precisely bounded
// by AST nodes. Next, we determine the variables that will be the parameters
// and return values of the extracted function/method. Lastly, we construct the call
// of the function/method and insert this call as well as the extracted function/method into
// their proper locations.
func extractFunctionMethod(fset *token.FileSet, start, end token.Pos, src []byte, file *ast.File, pkg *types.Package, info *types.Info, isMethod bool) (*token.FileSet, *analysis.SuggestedFix, error) {
	errorPrefix := "extractFunction"
	if isMethod {
		errorPrefix = "extractMethod"
	}

	tok := fset.File(file.FileStart)
	if tok == nil {
		return nil, nil, bug.Errorf("no file for position")
	}
	p, ok, methodOk, err := canExtractFunction(tok, start, end, src, file)
	if (!ok && !isMethod) || (!methodOk && isMethod) {
		return nil, nil, fmt.Errorf("%s: cannot extract %s: %v", errorPrefix,
			safetoken.StartPosition(fset, start), err)
	}
	tok, path, start, end, outer, node := p.tok, p.path, p.start, p.end, p.outer, p.node
	fileScope := info.Scopes[file]
	if fileScope == nil {
		return nil, nil, fmt.Errorf("%s: file scope is empty", errorPrefix)
	}
	pkgScope := fileScope.Parent()
	if pkgScope == nil {
		return nil, nil, fmt.Errorf("%s: package scope is empty", errorPrefix)
	}

	// A return statement is non-nested if its parent node is equal to the parent node
	// of the first node in the selection. These cases must be handled separately because
	// non-nested return statements are guaranteed to execute.
	var retStmts []*ast.ReturnStmt
	var hasNonNestedReturn bool
	startParent := findParent(outer, node)
	ast.Inspect(outer, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if n.Pos() < start || n.End() > end {
			return n.Pos() <= end
		}
		// exclude return statements in function literals because they don't affect the refactor.
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		if findParent(outer, n) == startParent {
			hasNonNestedReturn = true
		}
		retStmts = append(retStmts, ret)
		return false
	})
	containsReturnStatement := len(retStmts) > 0

	// Now that we have determined the correct range for the selection block,
	// we must determine the signature of the extracted function. We will then replace
	// the block with an assignment statement that calls the extracted function with
	// the appropriate parameters and return values.
	variables, err := collectFreeVars(info, file, fileScope, pkgScope, start, end, path[0])
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
		if outer == nil || outer.Recv == nil || len(outer.Recv.List) == 0 {
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
		typ := typesinternal.TypeExpr(file, pkg, v.obj.Type())
		if typ == nil {
			return nil, nil, fmt.Errorf("nil AST expression for type: %v", v.obj.Name())
		}
		seenVars[v.obj] = typ
		identifier := ast.NewIdent(v.obj.Name())
		// An identifier must meet three conditions to become a return value of the
		// extracted function. (1) its value must be defined or reassigned within
		// the selection (isAssigned), (2) it must be used at least once after the
		// selection (isUsed), and (3) its first use after the selection
		// cannot be its own reassignment or redefinition (objOverriden).
		vscope := v.obj.Parent()
		if vscope == nil {
			return nil, nil, fmt.Errorf("parent nil")
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
	for _, p := range path {
		if p == enclosing {
			break
		}
		if fl, ok := p.(*ast.FuncLit); ok {
			enclosing = fl.Type
			break
		}
	}

	// We put the selection in a constructed file. We can then traverse and edit
	// the extracted selection without modifying the original AST.
	startOffset, endOffset, err := safetoken.Offsets(tok, start, end)
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
	if containsReturnStatement {
		if !hasNonNestedReturn {
			// The selected block contained return statements, so we have to modify the
			// signature of the extracted function as described above. Adjust all of
			// the return statements in the extracted function to reflect this change in
			// signature.
			if err := adjustReturnStatements(returnTypes, seenVars, file, pkg, extractedBlock); err != nil {
				return nil, nil, err
			}
		}
		// Collect the additional return values and types needed to accommodate return
		// statements in the selection. Update the type signature of the extracted
		// function and construct the if statement that will be inserted in the enclosing
		// function.
		retVars, ifReturn, err = generateReturnInfo(enclosing, pkg, path, file, info, start, end, hasNonNestedReturn)
		if err != nil {
			return nil, nil, err
		}
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
		if err := format.Node(&ifBuf, fset, ifReturn); err != nil {
			return nil, nil, err
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
	outerStart, outerEnd, err := safetoken.Offsets(tok, outer.Pos(), outer.End())
	if err != nil {
		return nil, nil, err
	}
	before := src[outerStart:startOffset]
	after := src[endOffset:outerEnd]
	indent, err := calculateIndentation(src, tok, node)
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

// adjustRangeForCommentsAndWhiteSpace adjusts the given range to exclude unnecessary leading or
// trailing whitespace characters from selection as well as leading or trailing comments.
// In the following example, each line of the if statement is indented once. There are also two
// extra spaces after the sclosing bracket before the line break and a comment.
//
// \tif (true) {
// \t    _ = 1
// \t} // hello \n
//
// By default, a valid range begins at 'if' and ends at the first whitespace character
// after the '}'. But, users are likely to highlight full lines rather than adjusting
// their cursors for whitespace. To support this use case, we must manually adjust the
// ranges to match the correct AST node. In this particular example, we would adjust
// rng.Start forward to the start of 'if' and rng.End backward to after '}'.
func adjustRangeForCommentsAndWhiteSpace(tok *token.File, start, end token.Pos, content []byte, file *ast.File) (token.Pos, token.Pos, error) {
	// Adjust the end of the range to after leading whitespace and comments.
	prevStart := token.NoPos
	startComment := sort.Search(len(file.Comments), func(i int) bool {
		// Find the index for the first comment that ends after range start.
		return file.Comments[i].End() > start
	})
	for prevStart != start {
		prevStart = start
		// If start is within a comment, move start to the end
		// of the comment group.
		if startComment < len(file.Comments) && file.Comments[startComment].Pos() <= start && start < file.Comments[startComment].End() {
			start = file.Comments[startComment].End()
			startComment++
		}
		// Move forwards to find a non-whitespace character.
		offset, err := safetoken.Offset(tok, start)
		if err != nil {
			return 0, 0, err
		}
		for offset < len(content) && isGoWhiteSpace(content[offset]) {
			offset++
		}
		start = tok.Pos(offset)
	}

	// Adjust the end of the range to before trailing whitespace and comments.
	prevEnd := token.NoPos
	endComment := sort.Search(len(file.Comments), func(i int) bool {
		// Find the index for the first comment that ends after the range end.
		return file.Comments[i].End() >= end
	})
	// Search will return n if not found, so we need to adjust if there are no
	// comments that would match.
	if endComment == len(file.Comments) {
		endComment = -1
	}
	for prevEnd != end {
		prevEnd = end
		// If end is within a comment, move end to the start
		// of the comment group.
		if endComment >= 0 && file.Comments[endComment].Pos() < end && end <= file.Comments[endComment].End() {
			end = file.Comments[endComment].Pos()
			endComment--
		}
		// Move backwards to find a non-whitespace character.
		offset, err := safetoken.Offset(tok, end)
		if err != nil {
			return 0, 0, err
		}
		for offset > 0 && isGoWhiteSpace(content[offset-1]) {
			offset--
		}
		end = tok.Pos(offset)
	}

	return start, end, nil
}

// isGoWhiteSpace returns true if b is a considered white space in
// Go as defined by scanner.GoWhitespace.
func isGoWhiteSpace(b byte) bool {
	return uint64(scanner.GoWhitespace)&(1<<uint(b)) != 0
}

// findParent finds the parent AST node of the given target node, if the target is a
// descendant of the starting node.
func findParent(start ast.Node, target ast.Node) ast.Node {
	var parent ast.Node
	analysisinternal.WalkASTWithParent(start, func(n, p ast.Node) bool {
		if n == target {
			parent = p
			return false
		}
		return true
	})
	return parent
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
func collectFreeVars(info *types.Info, file *ast.File, fileScope, pkgScope *types.Scope, start, end token.Pos, node ast.Node) ([]*variable, error) {
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
		switch x := astutil.Unparen(n.X).(type) {
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
	tok        *token.File
	start, end token.Pos
	path       []ast.Node
	outer      *ast.FuncDecl
	node       ast.Node
}

// canExtractFunction reports whether the code in the given range can be
// extracted to a function.
func canExtractFunction(tok *token.File, start, end token.Pos, src []byte, file *ast.File) (*fnExtractParams, bool, bool, error) {
	if start == end {
		return nil, false, false, fmt.Errorf("start and end are equal")
	}
	var err error
	start, end, err = adjustRangeForCommentsAndWhiteSpace(tok, start, end, src, file)
	if err != nil {
		return nil, false, false, err
	}
	path, _ := astutil.PathEnclosingInterval(file, start, end)
	if len(path) == 0 {
		return nil, false, false, fmt.Errorf("no path enclosing interval")
	}
	// Node that encloses the selection must be a statement.
	// TODO: Support function extraction for an expression.
	_, ok := path[0].(ast.Stmt)
	if !ok {
		return nil, false, false, fmt.Errorf("node is not a statement")
	}

	// Find the function declaration that encloses the selection.
	var outer *ast.FuncDecl
	for _, p := range path {
		if p, ok := p.(*ast.FuncDecl); ok {
			outer = p
			break
		}
	}
	if outer == nil {
		return nil, false, false, fmt.Errorf("no enclosing function")
	}

	// Find the nodes at the start and end of the selection.
	var startNode, endNode ast.Node
	ast.Inspect(outer, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		// Do not override 'start' with a node that begins at the same location
		// but is nested further from 'outer'.
		if startNode == nil && n.Pos() == start && n.End() <= end {
			startNode = n
		}
		if endNode == nil && n.End() == end && n.Pos() >= start {
			endNode = n
		}
		return n.Pos() <= end
	})
	if startNode == nil || endNode == nil {
		return nil, false, false, fmt.Errorf("range does not map to AST nodes")
	}
	// If the region is a blockStmt, use the first and last nodes in the block
	// statement.
	// <rng.start>{ ... }<rng.end> => { <rng.start>...<rng.end> }
	if blockStmt, ok := startNode.(*ast.BlockStmt); ok {
		if len(blockStmt.List) == 0 {
			return nil, false, false, fmt.Errorf("range maps to empty block statement")
		}
		startNode, endNode = blockStmt.List[0], blockStmt.List[len(blockStmt.List)-1]
		start, end = startNode.Pos(), endNode.End()
	}
	return &fnExtractParams{
		tok:   tok,
		start: start,
		end:   end,
		path:  path,
		outer: outer,
		node:  startNode,
	}, true, outer.Recv != nil, nil
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
func generateReturnInfo(enclosing *ast.FuncType, pkg *types.Package, path []ast.Node, file *ast.File, info *types.Info, start, end token.Pos, hasNonNestedReturns bool) ([]*returnVariable, *ast.IfStmt, error) {
	var retVars []*returnVariable
	var cond *ast.Ident
	if !hasNonNestedReturns {
		// Generate information for the added bool value.
		name, _ := freshNameOutsideRange(info, file, path[0].Pos(), start, end, "shouldReturn", 0)
		cond = &ast.Ident{Name: name}
		retVars = append(retVars, &returnVariable{
			name:    cond,
			decl:    &ast.Field{Type: ast.NewIdent("bool")},
			zeroVal: ast.NewIdent("false"),
		})
	}
	// Generate information for the values in the return signature of the enclosing function.
	if enclosing.Results != nil {
		nameIdx := make(map[string]int) // last integral suffixes of generated names
		for _, field := range enclosing.Results.List {
			typ := info.TypeOf(field.Type)
			if typ == nil {
				return nil, nil, fmt.Errorf(
					"failed type conversion, AST expression: %T", field.Type)
			}
			expr := typesinternal.TypeExpr(file, pkg, typ)
			if expr == nil {
				return nil, nil, fmt.Errorf("nil AST expression")
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
				retName, idx := freshNameOutsideRange(info, file, path[0].Pos(), start, end, bestName, nameIdx[bestName])
				nameIdx[bestName] = idx
				z := typesinternal.ZeroExpr(file, pkg, typ)
				if z == nil {
					return nil, nil, fmt.Errorf("can't generate zero value for %T", typ)
				}
				retVars = append(retVars, &returnVariable{
					name:    ast.NewIdent(retName),
					decl:    &ast.Field{Type: expr},
					zeroVal: z,
				})
			}
		}
	}
	var ifReturn *ast.IfStmt
	if !hasNonNestedReturns {
		// Create the return statement for the enclosing function. We must exclude the variable
		// for the condition of the if statement (cond) from the return statement.
		ifReturn = &ast.IfStmt{
			Cond: cond,
			Body: &ast.BlockStmt{
				List: []ast.Stmt{&ast.ReturnStmt{Results: getNames(retVars)[1:]}},
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

// varNameForTypeName chooses a "good" name for a variable with the given type,
// if possible. Otherwise, it returns "", false.
//
// For special types, it uses known conventional names.
func varNameForType(t types.Type) (string, bool) {
	var typeName string
	if tn, ok := t.(interface{ Obj() *types.TypeName }); ok {
		obj := tn.Obj()
		k := objKey{name: obj.Name()}
		if obj.Pkg() != nil {
			k.pkg = obj.Pkg().Name()
		}
		if name, ok := conventionalVarNames[k]; ok {
			return name, true
		}
		typeName = obj.Name()
	} else if b, ok := t.(*types.Basic); ok {
		typeName = b.Name()
	}

	if typeName == "" {
		return "", false
	}

	return AbbreviateVarName(typeName), true
}

// adjustReturnStatements adds "zero values" of the given types to each return statement
// in the given AST node.
func adjustReturnStatements(returnTypes []*ast.Field, seenVars map[types.Object]ast.Expr, file *ast.File, pkg *types.Package, extractedBlock *ast.BlockStmt) error {
	var zeroVals []ast.Expr
	// Create "zero values" for each type.
	for _, returnType := range returnTypes {
		var val ast.Expr
		for obj, typ := range seenVars {
			if typ != returnType.Type {
				continue
			}
			val = typesinternal.ZeroExpr(file, pkg, obj.Type())
			break
		}
		if val == nil {
			return fmt.Errorf("could not find matching AST expression for %T", returnType.Type)
		}
		zeroVals = append(zeroVals, val)
	}
	// Add "zero values" to each return statement.
	// The bool reports whether the enclosing function should return after calling the
	// extracted function. We set the bool to 'true' because, if these return statements
	// execute, the extracted function terminates early, and the enclosing function must
	// return as well.
	zeroVals = append(zeroVals, ast.NewIdent("true"))
	ast.Inspect(extractedBlock, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if n, ok := n.(*ast.ReturnStmt); ok {
			n.Results = slices.Concat(zeroVals, n.Results)
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
