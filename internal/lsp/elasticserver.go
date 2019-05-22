package lsp

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/vcs"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/lsp/xlog"
	"golang.org/x/tools/internal/semver"
	"golang.org/x/tools/internal/span"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// NewClientElasticServer
func NewClientElasticServer(cache source.Cache, client protocol.Client) *ElasticServer {
	return &ElasticServer{
		Server: Server{
			client:  client,
			session: cache.NewSession(xlog.New(protocol.NewLogger(client))),
		},
	}
}

// NewElasticServer starts an LSP server on the supplied stream, and waits until the
// stream is closed.
func NewElasticServer(cache source.Cache, stream jsonrpc2.Stream) *ElasticServer {
	goPath := ""
	goRoot := ""
	for _, v := range os.Environ() {
		if strings.HasPrefix(v, "GOPATH=") {
			goPath = strings.TrimPrefix(v, "GOPATH=")
		}
		if strings.HasPrefix(v, "GOROOT=") {
			goRoot = strings.TrimPrefix(v, "GOROOT=")
		}
	}
	depsPath := filepath.Join(filepath.Join(goPath, "pkg"), "mod")

	s := &ElasticServer{
		DepsPath: depsPath,
		GoRoot:   goRoot,
	}

	var log xlog.Logger
	s.Conn, s.client, log = protocol.NewElasticServer(stream, s)
	s.session = cache.NewSession(log)
	return s
}

// RunElasticServerOnPort starts an LSP server on the given port and does not exit.
// This function exists for debugging purposes.
func RunElasticServerOnPort(ctx context.Context, cache source.Cache, port int, h func(s *ElasticServer)) error {
	return RunElasticServerOnAddress(ctx, cache, fmt.Sprintf(":%v", port), h)
}

// RunElasticServerOnAddress starts an LSP server on the given port and does not exit.
// This function exists for debugging purposes.
func RunElasticServerOnAddress(ctx context.Context, cache source.Cache, addr string, h func(s *ElasticServer)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		stream := jsonrpc2.NewHeaderStream(conn, conn)
		s := NewElasticServer(cache, stream)
		h(s)

		go s.Run(ctx)
	}
}

// ElasticServer "inherits" from lsp.server and is used to implement the elastic extension for the official go lsp.
type ElasticServer struct {
	Server
	DepsPath string
	GoRoot   string
}

func (s *ElasticServer) RunElasticServer(ctx context.Context) error {
	return s.Conn.Run(ctx)
}

// EDefinition has almost the same functionality with Definition except for the qualified name and symbol kind.
func (s *ElasticServer) EDefinition(ctx context.Context, params *protocol.TextDocumentPositionParams) ([]protocol.SymbolLocator, error) {
	uri := span.NewURI(params.TextDocument.URI)
	view := s.session.ViewOf(uri)
	f, m, err := getGoFile(ctx, view, uri)
	if err != nil {
		return nil, err
	}
	spn, err := m.PointSpan(params.Position)
	if err != nil {
		return nil, err
	}
	rng, err := spn.Range(m.Converter)
	if err != nil {
		return nil, err
	}
	ident, err := source.Identifier(ctx, view, f, rng.Start)
	if err != nil {
		return nil, err
	}

	kind := getSymbolKind(ident)
	if kind == 0 {
		return nil, fmt.Errorf("no corresponding symbol kind for '" + ident.Name + "'")
	}
	qname := getQName(ctx, f, ident, kind)

	declSpan, err := ident.Declaration.Range.Span()
	if err != nil {
		return nil, err
	}
	_, decM, err := getSourceFile(ctx, view, declSpan.URI())
	if err != nil {
		return nil, err
	}
	loc, err := decM.Location(declSpan)
	if err != nil {
		return nil, err
	}

	path := strings.TrimPrefix(loc.URI, "file://")
	pkgLoc := collectPkgMetadata(ident, view.Config(), s, path)
	path = normalizePath(path, view.Config(), &pkgLoc, s.DepsPath)
	loc.URI = normalizeLoc(loc.URI, s.DepsPath, &pkgLoc, path)

	return []protocol.SymbolLocator{{Qname: qname, Kind: kind, Path: path, Loc: loc, Package: pkgLoc}}, nil
}

// getSymbolKind get the symbol kind for a single position.
func getSymbolKind(ident *source.IdentifierInfo) protocol.SymbolKind {
	declObj := ident.Declaration.Object
	switch declObj.(type) {
	case *types.Const:
		return protocol.Constant
	case *types.Var:
		v, _ := declObj.(*types.Var)
		if v.IsField() {
			return protocol.Field
		}
		return protocol.Variable
	case *types.Nil:
		return protocol.Null
	case *types.PkgName:
		return protocol.Package
	case *types.Func:
		s, _ := declObj.Type().(*types.Signature)
		if s.Recv() == nil {
			return protocol.Function
		}
		return protocol.Method
	case *types.TypeName:
		switch declObj.Type().Underlying().(type) {
		case *types.Struct:
			return protocol.Struct
		case *types.Interface:
			return protocol.Interface
		case *types.Slice:
			return protocol.Array
		case *types.Array:
			return protocol.Array
		case *types.Basic:
			b, _ := declObj.Type().Underlying().(*types.Basic)
			if b.Info()&types.IsNumeric != 0 {
				return protocol.Number
			} else if b.Info()&types.IsBoolean != 0 {
				return protocol.Boolean
			} else if b.Info()&types.IsString != 0 {
				return protocol.String
			}
		}
	}

	// TODO(henrywong) For now, server use 0 represent the unknown symbol kind, however this is not a good practice, see
	//  https://github.com/Microsoft/language-server-protocol/issues/129.
	return protocol.SymbolKind(0)
}

// getQName returns the qualified name for a position in a file. Qualified name mainly served as the cross repo code
// search and code intelligence. The qualified name pattern as bellow:
//  qname = package.name + struct.name* + function.name* | (struct.name + method.name)* + struct.name* + symbol.name
//
// TODO(henrywong) It's better to use the scope chain to give a qualified name for the symbols, however there is no
// APIs can achieve this goals, just traverse the ast node path for now.
func getQName(ctx context.Context, f source.GoFile, ident *source.IdentifierInfo, kind protocol.SymbolKind) string {
	declObj := ident.Declaration.Object
	qname := declObj.Name()

	if kind == protocol.Package {
		return qname
	}

	// Get the file where the symbol definition located.
	fAST := f.GetAST(ctx)
	pos := declObj.Pos()
	path, _ := astutil.PathEnclosingInterval(fAST, pos, pos)

	// TODO(henrywong) Should we put a check here for the case of only one node?
	for id, n := range path[1:] {
		switch n.(type) {
		case *ast.StructType:
			// Check its father to decide whether the ast.StructType is a named type or an anonymous type.
			switch path[id+2].(type) {
			case *ast.TypeSpec:
				// ident is located in a named struct declaration, add the type name into the qualified name.
				ts, _ := path[id+2].(*ast.TypeSpec)
				qname = ts.Name.Name + "." + qname
			case *ast.Field:
				// ident is located in a anonymous struct declaration which used to define a field, like struct fields,
				// function parameters, function named return parameters, add the field name into the qualified name.
				field, _ := path[id+2].(*ast.Field)
				if len(field.Names) != 0 {
					// If there is a bunch of fields declared with same anonymous struct type, just consider the first field's
					// name.
					qname = field.Names[0].Name + "." + qname
				}

			case *ast.ValueSpec:
				// ident is located in a anonymous struct declaration which used define a variable, add the variable name into
				// the qualified name.
				vs, _ := path[id+2].(*ast.ValueSpec)
				if len(vs.Names) != 0 {
					// If there is a bunch of variables declared with same anonymous struct type, just consider the first
					// variable's name.
					qname = vs.Names[0].Name + "." + qname
				}
			}
		case *ast.InterfaceType:
			// Check its father to get the interface name.
			switch path[id+2].(type) {
			case *ast.TypeSpec:
				ts, _ := path[id+2].(*ast.TypeSpec)
				qname = ts.Name.Name + "." + qname
			}

		case *ast.FuncDecl:
			f, _ := n.(*ast.FuncDecl)
			if f.Name != nil && f.Name.Name != qname && (kind == protocol.Method || kind == protocol.Function) {
				qname = f.Name.Name + "." + qname
			}

			if f.Name != nil {
				if kind == protocol.Method || kind == protocol.Function {
					if f.Name.Name != qname {
						qname = f.Name.Name + "." + qname
					}
				} else {
					qname = f.Name.Name + "." + qname
				}
			}

			// If n is method, add the struct name as a prefix.
			if f.Recv != nil {
				var typeName string
				switch r := f.Recv.List[0].Type.(type) {
				case *ast.StarExpr:
					typeName = r.X.(*ast.Ident).Name
				case *ast.Ident:
					typeName = r.Name
				}
				qname = typeName + "." + qname
			}
		case *ast.FuncLit:
			// Considering the function literal is for making the local variable declared in it more unique, the
			// handling is a little tricky. If the function literal is assigned to a named entity, like variable, it is
			// better consider the variable name into the qualified name.

			// Check its ancestors to decide where it is located in, like a assignment, variable declaration, or a
			// return statement.
			switch path[id+2].(type) {
			case *ast.AssignStmt:
				as, _ := path[id+2].(*ast.AssignStmt)
				if i, ok := as.Lhs[0].(*ast.Ident); ok {
					qname = i.Name + "." + qname
				}
			}
		}
	}
	return declObj.Pkg().Name() + "." + qname
}

// collectPackageMetadata collects metadata for the packages where the specified symbols located.
func collectPkgMetadata(ident *source.IdentifierInfo, cfg packages.Config, s *ElasticServer, loc string) protocol.PackageLocator {
	pkgLoc := protocol.PackageLocator{
		Version: "",
		Name:    "",
		RepoURI: "",
	}
	// Get the package where the symbol belongs to.
	pkg := ident.Declaration.Object.Pkg()
	if pkg == nil {
		return pkgLoc
	}
	pkgLoc.Name = pkg.Name()
	pkgLoc.RepoURI = pkg.Path()

	// If the location is inside the current project or the location from standard library, there is no need to resolve
	// the revision.
	if strings.HasPrefix(loc, cfg.Dir) || strings.HasPrefix(loc, s.GoRoot) {
		return pkgLoc
	}

	if _, err := ident.File.URI().Filename(); err == nil {
		getPkgVersion(s, cfg, &pkgLoc, loc)
	}
	repoRoot, err := vcs.RepoRootForImportPath(pkg.Path(), false)
	if err == nil {
		pkgLoc.RepoURI = repoRoot.Root
	}
	return pkgLoc
}

// getPkgVersion collects the version information for a specified package, the version information will be one of the
// two forms semver format and prefix of a commit hash.
func getPkgVersion(s *ElasticServer, cfg packages.Config, pkgLoc *protocol.PackageLocator, loc string) {
	rev := getPkgVersionFast(strings.TrimPrefix(loc, filepath.Join(s.DepsPath, cfg.Dir)))
	if rev == "" {
		if err := getPkgVersionSlow(); err != nil {
			return
		}
	}
	// In general, the module version is in semver format and it's bound to be accompanied by a semver tag. But
	// sometimes, like when there is no tag or try to get the latest commit, the module version is in pseudo-version
	// pseudo-version format. Strip off the prefix to get the commit hash part which is a prefix of the full commit
	// hash.
	if strings.Count(rev, "-") == 2 {
		rev = strings.TrimSuffix(rev, "+incompatible")
		i := strings.LastIndex(rev, "-")
		rev = rev[i+1:]
	}
	pkgLoc.Version = rev
}

// getPkgVersionSlow get the pkg revision with a more accurate approach, call 'go list' again is an option, but it not
// wise to call 'go list' twice.
// TODO(henrywong) Use correct API to get the revision.
func getPkgVersionSlow() error {
	return fmt.Errorf("for now, there is no proper and efficient API to get the revision")
}

// getPkgVersionFast extract the revision in a fast manner. 'go list' will create a folder whose name will contain the
// revision, we can extract it from the path, like '.../modulename@v1.2.3/...', this approach can avoid call 'go list'
// multiple times. If there are multiple valid version substrings, give up.
func getPkgVersionFast(loc string) string {
	strs := strings.SplitAfter(loc, "@")
	var validVersion []string
	for i := 1; i < len(strs); i++ {
		substrs := strings.Split(strs[i], string(filepath.Separator))
		if semver.IsValid(substrs[0]) {
			validVersion = append(validVersion, substrs[0])
		}
	}
	if len(validVersion) != 1 {
		// give up
		return ""
	}
	return validVersion[0]
}

func normalizeLoc(loc string, depsPath string, pkgLoc *protocol.PackageLocator, path string) string {
	loc = strings.TrimPrefix(loc, "file://")
	if strings.HasPrefix(loc, depsPath) {
		strs := []string{pkgLoc.RepoURI, "blob", pkgLoc.Version, path}
		return filepath.Join(strs...)
	}
	return "file://" + loc
}

// normalizePath trims the workspace folder prefix to get the file path in project. Remove the revision embedded in the
// path if it exists.
func normalizePath(path string, cfg packages.Config, pkgLoc *protocol.PackageLocator, depsPath string) string {
	if strings.HasPrefix(path, cfg.Dir) {
		path = strings.TrimPrefix(path, cfg.Dir)
	} else {
		path = strings.TrimPrefix(path, filepath.Join(depsPath, pkgLoc.RepoURI))
		rev := getPkgVersionFast(path)
		if rev != "" {
			strs := strings.Split(path, rev)
			i := strings.LastIndex(strs[0], "@")
			path = filepath.Join(path[:i], strs[1])
		}
	}
	return strings.TrimPrefix(path, string(filepath.Separator))
}
