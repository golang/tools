package source

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/ast/astutil"
	"reflect"
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

type InvalidNodeError struct {
	Node ast.Node
	msg  string
}

func (e *InvalidNodeError) Error() string {
	return e.msg
}

func newInvalidNodeError(fset *token.FileSet, node ast.Node) *InvalidNodeError {
	lineCol := func(p token.Pos) string {
		pp := fset.Position(p)
		return fmt.Sprintf("%d:%d", pp.Line, pp.Column)
	}
	return &InvalidNodeError{
		Node: node,
		msg: fmt.Sprintf("invalid node: %s (%s-%s)",
			reflect.TypeOf(node).Elem(), lineCol(node.Pos()), lineCol(node.End())),
	}
}

func getObjectPathNode(pkg Package, fset *token.FileSet, o types.Object) (nodes []ast.Node, ident *ast.Ident, err error) {
	nodes, _ = getPathNodes(pkg, fset, o.Pos(), o.Pos())
	if len(nodes) == 0 {
		ip := pkg.GetImport(o.Pkg().Path())
		if ip == nil {
			return nil, nil,
				fmt.Errorf("import package %s of package %s does not exist", o.Pkg().Path(), pkg.GetTypes().Path())
		}

		nodes, err = getPathNodes(ip, fset, o.Pos(), o.Pos())
		if err != nil {
			return nil, nil, err
		}
	}

	ident, err = fetchIdentFromPathNodes(fset, nodes)
	return
}

func getPathNodes(pkg Package, fset *token.FileSet, start, end token.Pos) ([]ast.Node, error) {
	nodes, _ := pathEnclosingInterval(pkg, fset, start, end)
	if len(nodes) == 0 {
		s := fset.Position(start)
		return nodes, fmt.Errorf("no node found at %s offset %d", s, s.Offset)
	}

	return nodes, nil
}

func fetchIdentFromPathNodes(fset *token.FileSet, nodes []ast.Node) (*ast.Ident, error) {
	firstNode := nodes[0]
	switch node := firstNode.(type) {
	case *ast.Ident:
		return node, nil
	default:
		return nil, newInvalidNodeError(fset, firstNode)
	}
}

// pathEnclosingInterval returns the PackageInfo and ast.Node that
// contain source interval [start, end), and all the node's ancestors
// up to the AST root.  It searches all ast.Files of all packages in prog.
// exact is defined as for astutil.pathEnclosingInterval.
//
// The zero value is returned if not found.
//
func pathEnclosingInterval(pkg Package, fset *token.FileSet, start, end token.Pos) (path []ast.Node, exact bool) {
	path, exact = doEnclosingInterval(pkg, fset, start, end)
	return
}

func doEnclosingInterval(pkg Package, fset *token.FileSet, start, end token.Pos) ([]ast.Node, bool) {
	if pkg == nil || pkg.GetSyntax() == nil {
		return nil, false
	}

	for _, f := range pkg.GetSyntax() {
		if f.Pos() == token.NoPos {
			// This can happen if the parser saw
			// too many errors and bailed out.
			// (Use parser.AllErrors to prevent that.)
			continue
		}
		if !tokenFileContainsPos(fset.File(f.Pos()), start) {
			continue
		}
		if path, exact := astutil.PathEnclosingInterval(f, start, end); path != nil {
			return path, exact
		}
	}

	return nil, false
}

// TODO(adonovan): make this a method: func (*token.File) Contains(token.Pos)
func tokenFileContainsPos(f *token.File, pos token.Pos) bool {
	p := int(pos)
	base := f.Base()
	return base <= p && p < base+f.Size()
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
