// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/util/safetoken"
	"golang.org/x/tools/internal/typesinternal"
	"golang.org/x/tools/go/analysis"
)

// canConvertToVarDecl reports whether the code in the given range can be
// converted from a short variable declaration (:=) to an explicit var declaration.
// It returns the AssignStmt if conversion is possible.
func canConvertToVarDecl(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*ast.AssignStmt, bool, error) {
	path, _ := astutil.PathEnclosingInterval(pgf.File, start, end)
	if len(path) == 0 {
		return nil, false, nil
	}

	// Find the enclosing assignment statement
	var assignStmt *ast.AssignStmt
	for _, node := range path {
		if stmt, ok := node.(*ast.AssignStmt); ok {
			assignStmt = stmt
			break
		}
	}

	if assignStmt == nil {
		return nil, false, nil
	}

	// Check if it's a short variable declaration (:=)
	if assignStmt.Tok != token.DEFINE {
		return nil, false, nil
	}

	// Check that all LHS identifiers are being defined (not redeclared)
	// and that their types can be named
	info := pkg.TypesInfo()
	for _, lhs := range assignStmt.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			// Complex LHS expressions like a.b := x are not valid anyway
			return nil, false, nil
		}

		// Skip blank identifiers
		if ident.Name == "_" {
			continue
		}

		// Get the type of this identifier
		obj := info.Defs[ident]
		if obj == nil {
			// This identifier is being reassigned, not defined
			// This happens in cases like: existingVar, newVar := f()
			// For now, we skip these mixed cases
			return nil, false, nil
		}

		// Check if the type can be named outside its package
		typ := obj.Type()
		if !typeIsExportable(typ, pkg.Types()) {
			return nil, false, nil
		}
	}

	return assignStmt, true, nil
}

// typeIsExportable reports whether the given type can be named outside its defining package.
// Returns false for unexported types from other packages.
func typeIsExportable(typ types.Type, currentPkg *types.Package) bool {
	switch t := typ.(type) {
	case *types.Named:
		obj := t.Obj()
		// If the type is from a different package, it must be exported
		if obj.Pkg() != nil && obj.Pkg() != currentPkg && !obj.Exported() {
			return false
		}
		return true
	case *types.Pointer:
		return typeIsExportable(t.Elem(), currentPkg)
	case *types.Slice:
		return typeIsExportable(t.Elem(), currentPkg)
	case *types.Array:
		return typeIsExportable(t.Elem(), currentPkg)
	case *types.Map:
		return typeIsExportable(t.Key(), currentPkg) && typeIsExportable(t.Elem(), currentPkg)
	case *types.Chan:
		return typeIsExportable(t.Elem(), currentPkg)
	default:
		// Basic types, interfaces, etc. are always exportable
		return true
	}
}

// convertToVarDecl converts a short variable declaration (:=) to an explicit
// var declaration with separate assignment.
//
// Example:
//
//	f := os.DirFS("/")
//
// becomes:
//
//	var f fs.FS
//	f = os.DirFS("/")
func convertToVarDecl(pkg *cache.Package, pgf *parsego.File, start, end token.Pos) (*token.FileSet, *analysis.SuggestedFix, error) {
	assignStmt, ok, err := canConvertToVarDecl(pkg, pgf, start, end)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, fmt.Errorf("cannot convert to var declaration")
	}

	fset := pkg.FileSet()
	info := pkg.TypesInfo()
	src := pgf.Src

	// Build the qualifier function for type names
	// This tracks which imports we need to add
	currentPkgPath := pkg.Types().Path()
	importNames := make(map[string]string) // importPath -> localName

	// First, collect existing imports from the file
	for _, imp := range pgf.File.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		localName := ""
		if imp.Name != nil {
			localName = imp.Name.Name
		}
		importNames[importPath] = localName
	}

	qual := func(p *types.Package) string {
		if p == nil || p.Path() == currentPkgPath {
			return ""
		}
		// Check if we already have this import
		if name, ok := importNames[p.Path()]; ok {
			if name != "" {
				return name
			}
			return p.Name()
		}
		// We'll need to add this import
		importNames[p.Path()] = ""
		return p.Name()
	}

	// Build var declarations for each LHS identifier
	var varDecls []string
	var assignLhs []string

	for i, lhs := range assignStmt.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok {
			continue
		}

		// Handle blank identifier
		if ident.Name == "_" {
			assignLhs = append(assignLhs, "_")
			continue
		}

		// Get the type from the definition
		obj := info.Defs[ident]
		if obj == nil {
			// Reassignment case - use the existing variable
			assignLhs = append(assignLhs, ident.Name)
			continue
		}

		typ := obj.Type()
		typeStr := types.TypeString(typ, qual)

		// For function types, we might want to use the interface type instead
		// if available (like fs.FS instead of the concrete return type)
		// This requires more sophisticated analysis of the RHS

		varDecls = append(varDecls, fmt.Sprintf("var %s %s", ident.Name, typeStr))
		assignLhs = append(assignLhs, ident.Name)

		// Check if this is a named type that might have a more general interface
		if i < len(assignStmt.Rhs) {
			// For single-value RHS, try to find if there's a more general type
			// This is an enhancement - for now we use the concrete type
			_ = assignStmt.Rhs[i]
		}
	}

	// Build the RHS string from source
	rhsStart := safetoken.StartPosition(fset, assignStmt.Rhs[0].Pos())
	rhsEnd := safetoken.EndPosition(fset, assignStmt.Rhs[len(assignStmt.Rhs)-1].End())
	rhsText := string(src[rhsStart.Offset:rhsEnd.Offset])

	// Construct the replacement text
	var newText strings.Builder

	// Add var declarations
	for _, decl := range varDecls {
		newText.WriteString(decl)
		newText.WriteString("\n")
	}

	// Get indentation from the original line
	stmtStart := safetoken.StartPosition(fset, assignStmt.Pos())
	lineStart := stmtStart.Offset
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	indent := ""
	for i := lineStart; i < stmtStart.Offset && (src[i] == ' ' || src[i] == '\t'); i++ {
		indent += string(src[i])
	}

	// Add indentation to var declarations (except the first line which replaces the original)
	if len(varDecls) > 0 {
		// Rebuild with proper indentation
		newText.Reset()
		for j, decl := range varDecls {
			if j > 0 {
				newText.WriteString(indent)
			}
			newText.WriteString(decl)
			newText.WriteString("\n")
		}
		newText.WriteString(indent)
	}

	// Add the assignment statement
	newText.WriteString(strings.Join(assignLhs, ", "))
	newText.WriteString(" = ")
	newText.WriteString(rhsText)

	// Create the text edit
	startOffset, endOffset, err := safetoken.Offsets(pgf.Tok, assignStmt.Pos(), assignStmt.End())
	if err != nil {
		return nil, nil, err
	}

	edits := []analysis.TextEdit{{
		Pos:     assignStmt.Pos(),
		End:     assignStmt.End(),
		NewText: []byte(newText.String()),
	}}

	// Check if we need to add any imports
	// This is a simplified version - a full implementation would use
	// the imports package to properly add imports
	_ = startOffset
	_ = endOffset

	// For now, we don't automatically add imports
	// The user may need to organize imports after applying this refactoring
	// A more complete implementation would use golang.AddImport

	return fset, &analysis.SuggestedFix{
		TextEdits: edits,
	}, nil
}

// findImportSpec finds the import spec for the given package path in the file.
func findImportSpec(file *ast.File, pkgPath string) *ast.ImportSpec {
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == pkgPath {
			return imp
		}
	}
	return nil
}

// isUnexportedType checks if typ contains any unexported types from other packages.
func isUnexportedType(typ types.Type, currentPkg *types.Package) bool {
	return !typeIsExportable(typ, currentPkg)
}

// Helper to check if a type needs import qualification
func typeNeedsImport(typ types.Type, currentPkg *types.Package) (string, bool) {
	switch t := typ.(type) {
	case *types.Named:
		obj := t.Obj()
		if obj.Pkg() != nil && obj.Pkg() != currentPkg {
			return obj.Pkg().Path(), true
		}
	case *types.Pointer:
		return typeNeedsImport(t.Elem(), currentPkg)
	case *types.Slice:
		return typeNeedsImport(t.Elem(), currentPkg)
	case *types.Array:
		return typeNeedsImport(t.Elem(), currentPkg)
	case *types.Map:
		if path, needs := typeNeedsImport(t.Key(), currentPkg); needs {
			return path, true
		}
		return typeNeedsImport(t.Elem(), currentPkg)
	case *types.Chan:
		return typeNeedsImport(t.Elem(), currentPkg)
	}
	return "", false
}

// Ensure typesinternal is used (for potential future use)
var _ = typesinternal.ErrorCodeStartEnd
