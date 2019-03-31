package source

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

func FindComments(pkg Package, fset *token.FileSet, o types.Object, name string) (string, error) {
	if o == nil {
		return "", nil
	}

	// Package names must be resolved specially, so do this now to avoid
	// additional overhead.
	if v, ok := o.(*types.PkgName); ok {
		imp := pkg.GetImport(v.Imported().Path())
		if imp == nil {
			return "", fmt.Errorf("failed to import package %q", v.Imported().Path())
		}

		return PackageDoc(imp.GetSyntax(), name), nil
	}

	// Resolve the object o into its respective ast.Node
	path, _, _ := getObjectPathNode(pkg, fset, o)
	if len(path) == 0 {
		return "", nil
	}

	return PullComments(path), nil
}

func PullComments(pathNodes []ast.Node) string {
	// Pull the comment out of the comment map for the file. Do
	// not search too far away from the current path.
	var comments string
	for i := 0; i < 3 && i < len(pathNodes) && comments == ""; i++ {
		switch v := pathNodes[i].(type) {
		case *ast.Field:
			// Concat associated documentation with any inline comments
			comments = JoinCommentGroups(v.Doc, v.Comment)
		case *ast.ValueSpec:
			comments = v.Doc.Text()
		case *ast.TypeSpec:
			comments = v.Doc.Text()
		case *ast.GenDecl:
			comments = v.Doc.Text()
		case *ast.FuncDecl:
			comments = v.Doc.Text()
		}
	}
	return comments
}

// PackageDoc finds the documentation for the named package from its files or
// additional files.
func PackageDoc(files []*ast.File, pkgName string) string {
	for _, f := range files {
		if f.Name.Name == pkgName {
			txt := f.Doc.Text()
			if strings.TrimSpace(txt) != "" {
				return txt
			}
		}
	}
	return ""
}

// JoinCommentGroups joins the resultant non-empty comment text from two
// CommentGroups with a newline.
func JoinCommentGroups(a, b *ast.CommentGroup) string {
	aText := a.Text()
	bText := b.Text()
	if aText == "" {
		return bText
	} else if bText == "" {
		return aText
	} else {
		return aText + "\n" + bText
	}
}
