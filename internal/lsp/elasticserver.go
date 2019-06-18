package lsp

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/vcs"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/lsp/xlog"
	"golang.org/x/tools/internal/semver"
	"golang.org/x/tools/internal/span"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
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

// ElasticDocumentSymbol is the override version of the 'Server.DocumentSymbol', which provides the
// DetailSymbolInformation construction and 'DocumentSymbol' flatten.
func (s *ElasticServer) ElasticDocumentSymbol(ctx context.Context, params *protocol.DocumentSymbolParams, full bool, pkgLocator *protocol.PackageLocator) (symsInfo []protocol.SymbolInformation,
	detailSyms []protocol.DetailSymbolInformation,
	err error) {
	docSyms, err := (*Server).DocumentSymbol(&s.Server, ctx, params)
	var flattenDocumentSymbol func(*[]protocol.DocumentSymbol, string, string)
	// Note: The reason why we construct the qname during the flatten process is that we can't construct the qname
	// through the 'SymbolInformation.ContainerName' because of the possibilities of the 'ContainerName' collision.
	flattenDocumentSymbol = func(symbols *[]protocol.DocumentSymbol, prefix string, container string) {
		for _, symbol := range *symbols {
			sym := protocol.SymbolInformation{
				Name:          symbol.Name,
				Kind:          symbol.Kind,
				Deprecated:    symbol.Deprecated,
				ContainerName: container,
				Location: protocol.Location{
					URI:   params.TextDocument.URI,
					Range: symbol.SelectionRange,
				},
			}
			symsInfo = append(symsInfo, sym)
			var qnamePrefix string
			if full {
				if prefix != "" {
					qnamePrefix = prefix + "." + symbol.Name
				} else {
					qnamePrefix = symbol.Name
				}
				detailSyms = append(detailSyms, protocol.DetailSymbolInformation{
					Symbol:  sym,
					Qname:   pkgLocator.Name + "." + qnamePrefix,
					Package: *pkgLocator,
				})
			}
			if len(symbol.Children) > 0 {
				flattenDocumentSymbol(&symbol.Children, qnamePrefix, symbol.Name)
			}
		}
	}

	flattenDocumentSymbol(&docSyms, "", "")
	return
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

	declObj := getDeclObj(ctx, f, rng.Start)
	kind := getSymbolKind(declObj)
	if kind == 0 {
		return nil, fmt.Errorf("no corresponding symbol kind for '" + ident.Name + "'")
	}
	qname := getQName(ctx, f, declObj, ident, kind)

	declSpan, err := ident.DeclarationRange().Span()
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
	pkgLocator, scheme := collectPkgMetadata(declObj.Pkg(), view.Folder().Filename(), s, path)
	path = normalizePath(path, view.Folder().Filename(), strings.TrimPrefix(pkgLocator.RepoURI, scheme), s.DepsPath)
	loc.URI = normalizeLoc(loc.URI, s.DepsPath, &pkgLocator, path)

	return []protocol.SymbolLocator{{Qname: qname, Kind: kind, Path: path, Loc: loc, Package: pkgLocator}}, nil
}

// Full collects the symbols defined in the current file and the references.
func (s *ElasticServer) Full(ctx context.Context, fullParams *protocol.FullParams) (protocol.FullResponse, error) {
	params := protocol.DocumentSymbolParams{TextDocument: fullParams.TextDocument}
	fullResponse := protocol.FullResponse{
		Symbols:    []protocol.DetailSymbolInformation{},
		References: []protocol.Reference{},
	}
	uri := span.NewURI(fullParams.TextDocument.URI)
	view := s.session.ViewOf(uri)
	f, _, err := getGoFile(ctx, view, uri)
	if err != nil {
		return fullResponse, err
	}
	path := f.URI().Filename()
	if f.GetPackage(ctx) == nil {
		return fullResponse, err
	}
	pkgLocator, _ := collectPkgMetadata(f.GetPackage(ctx).GetTypes(), view.Folder().Filename(), s, path)

	_, detailSyms, err := s.ElasticDocumentSymbol(ctx, &params, true, &pkgLocator)
	if err != nil {
		return fullResponse, err
	}
	fullResponse.Symbols = detailSyms

	// TODO(henrywong) We won't collect the references for now because of the performance issue. Once the 'References'
	//  option is true, we will implement the references collecting feature.
	if !fullParams.Reference {
		return fullResponse, nil
	}
	return fullResponse, nil
}

type WorkspaceFolderMeta struct {
	URI           span.URI
	moduleFolders []string
}

// manageDeps will explore the workspace folders sent from the client and give a whole picture of them. Besides that,
// manageDeps will try its best to convert the folders to modules. The core functions, like deps downloading and deps
// management, will be implemented in the package 'cache'.
func (s ElasticServer) ManageDeps(folders *[]protocol.WorkspaceFolder) error {
	// Note: For the upstream go langserver, granularity of the workspace folders is repository. But for the elastic go
	// language server, there are repositories contain multiple modules. In order to handle the modules separately, we
	// consider different modules as different workspace folders, so we can manage the dependency of different modules
	// separately.
	for _, folder := range *folders {
		metadata := &WorkspaceFolderMeta{}
		if folder.URI != "" {
			metadata.URI = span.NewURI(folder.URI)
		}
		if err := collectWorkspaceFolderMetadata(metadata); err != nil {
			return err
		}
		// Convert the module folders to the workspace folders.
		for _, folder := range metadata.moduleFolders {
			uri := span.NewURI(folder)
			notExists := true
			for _, wf := range *folders {
				if filepath.Clean(string(uri)) == filepath.Clean(wf.URI) {
					notExists = false
				}
			}
			if notExists {
				*folders = append(*folders, protocol.WorkspaceFolder{URI: string(uri), Name: filepath.Base(folder)})
			}
		}
	}
	return nil
}

// getSymbolKind get the symbol kind for a single position.
func getSymbolKind(declObj types.Object) protocol.SymbolKind {
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
func getQName(ctx context.Context, f source.GoFile, declObj types.Object, ident *source.IdentifierInfo, kind protocol.SymbolKind) string {
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
	if declObj.Pkg() == nil {
		return qname
	}
	return declObj.Pkg().Name() + "." + qname
}

// collectPackageMetadata collects metadata for the packages where the specified symbols located and the scheme, i.e.
// URL prefix, of the repository which the packages belong to.
func collectPkgMetadata(pkg *types.Package, dir string, s *ElasticServer, loc string) (protocol.PackageLocator, string) {
	pkgLocator := protocol.PackageLocator{
		Version: "",
		Name:    "",
		RepoURI: "",
	}
	// Get the package where the symbol belongs to.
	if pkg == nil {
		return pkgLocator, ""
	}
	pkgLocator.Name = pkg.Name()
	pkgLocator.RepoURI = pkg.Path()

	// If the location is inside the current project or the location from standard library, there is no need to resolve
	// the revision.
	if strings.HasPrefix(loc, dir) || strings.HasPrefix(loc, s.GoRoot) {
		return pkgLocator, ""
	}

	getPkgVersion(s, dir, &pkgLocator, loc)
	repoRoot, err := vcs.RepoRootForImportPath(pkg.Path(), false)
	if err == nil {
		pkgLocator.RepoURI = repoRoot.Repo
		return pkgLocator, strings.TrimSuffix(repoRoot.Repo, repoRoot.Root)
	}

	return pkgLocator, ""
}

// getPkgVersion collects the version information for a specified package, the version information will be one of the
// two forms semver format and prefix of a commit hash.
func getPkgVersion(s *ElasticServer, dir string, pkgLoc *protocol.PackageLocator, loc string) {
	rev := getPkgVersionFast(strings.TrimPrefix(loc, filepath.Join(s.DepsPath, dir)))
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

// normalizeLoc concatenates repository URL, package version and file path to get a complete location URL for the
// location located in the dependencies.
func normalizeLoc(loc string, depsPath string, pkgLocator *protocol.PackageLocator, path string) string {
	loc = strings.TrimPrefix(loc, "file://")
	if strings.HasPrefix(loc, depsPath) {
		strs := []string{"blob", pkgLocator.Version, path}
		return pkgLocator.RepoURI + string(filepath.Separator) + filepath.Join(strs...)
	}
	return "file://" + loc
}

// normalizePath trims the workspace folder prefix to get the file path in project. Remove the revision embedded in the
// path if it exists.
func normalizePath(path, dir, repoURI, depsPath string) string {
	if strings.HasPrefix(path, dir) {
		path = strings.TrimPrefix(path, dir)
	} else {
		path = strings.TrimPrefix(path, filepath.Join(depsPath, repoURI))
		rev := getPkgVersionFast(path)
		if rev != "" {
			strs := strings.Split(path, rev)
			i := strings.LastIndex(strs[0], "@")
			path = filepath.Join(path[:i], strs[1])
		}
	}
	return strings.TrimPrefix(path, string(filepath.Separator))
}

// collectWorkspaceFolderMetadata explores the workspace folder to collects the meta information of the folder. And
// create a new 'go.mod' if necessary to cover all the source files.
func collectWorkspaceFolderMetadata(metadata *WorkspaceFolderMeta) error {
	rootPath := metadata.URI.Filename()
	// Collect 'go.mod' and record them as workspace folders.
	if err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		dir := filepath.Dir(path)
		if dir[0] == '.' {
			return filepath.SkipDir
		} else if info.Name() == "go.mod" {
			dir := filepath.Dir(path)
			metadata.moduleFolders = append(metadata.moduleFolders, dir)
		}
		return nil
	}); err != nil {
		return err
	}
	folderUncovered, folderNeedMod, err := collectUncoveredSrc(rootPath)
	if err != nil {
		return nil
	}
	// If folders need to be covered exist, a new 'go.mod' will be created manually.
	if len(folderUncovered) > 0 {
		longestPrefix := string(filepath.Separator)
		// Compute the longest common prefix of the folders which need to be covered by 'go.mod'.
	DONE:
		for i, name := range folderUncovered[0] {
			same := true
			for _, folder := range folderUncovered[1:] {
				if len(folder) <= i || folder[i] != name {
					same = false
					break DONE
				}
			}
			if same {
				longestPrefix = filepath.Join(longestPrefix, name)
			}
		}
		folderNeedMod = append(folderNeedMod, filepath.Clean(longestPrefix))
	}

	for _, path := range folderNeedMod {
		cmd := exec.Command("go", "mod", "init", path)
		cmd.Dir = path
		if err := cmd.Run(); err != nil {
			return err
		}
		metadata.moduleFolders = append(metadata.moduleFolders, path)
	}
	return nil
}

var DependencyControlSystem = []string{
	"GLOCKFILE",
	"Godeps/Godeps.json",
	"Gopkg.lock",
	"dependencies.tsv",
	"glide.lock",
	"vendor.conf",
	"vendor.yml",
	"vendor/manifest",
	"vendor/vendor.json",
}

// existDepControlFile determines if dependency control files exist in the specified folder.
func existDepControlFile(dir string) bool {
	for _, name := range DependencyControlSystem {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// collectUncoveredSrc explores the rootPath recursively, collects
//  - folders need to be covered, which we will create a module to cover all these folders.
//  - folders need to create a module.
func collectUncoveredSrc(path string) ([][]string, []string, error) {
	var folderUncovered [][]string
	var folderNeedMod []string
	if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
		return nil, nil, nil
	}
	// Given that we have to respect the original dependency control data, if there is a dependency control file, we
	// we will create a 'go.mod' accordingly.
	if existDepControlFile(path) {
		folderNeedMod = append(folderNeedMod, path)
		return nil, folderNeedMod, nil
	}
	// If there are remaining '.go' source files under the current folder, that means they will not be covered by
	// any 'go.mod'.
	shouldBeCovered := false
	fileInfo, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, nil, err
	}
	for _, info := range fileInfo {
		if !shouldBeCovered && filepath.Ext(info.Name()) == ".go" && !strings.HasSuffix(info.Name(), "_test.go") {
			shouldBeCovered = true
		}
		if info.IsDir() && info.Name()[0] != '.' {
			uncovered, mod, e := collectUncoveredSrc(filepath.Join(path, info.Name()))
			folderNeedMod = append(folderNeedMod, mod...)
			folderUncovered = append(folderUncovered, uncovered...)
			err = e
		}
	}
	if shouldBeCovered {
		folderUncovered = append(folderUncovered, strings.Split(path, string(filepath.Separator)))
	}
	return folderUncovered, folderNeedMod, err
}

// TODO(henrywong) Upstream has made the declaration object of the selected symbol as a private field, so we have to
//  construct the declaration object by ourselves. Given that upstream has trimmed the ast of the dependencies to reduce
//  the usage of the memory, this construction will parse the AST of the dependent source file for every call and bring
//  neglect overhead.
func getDeclObj(ctx context.Context, f source.GoFile, pos token.Pos) types.Object {
	var astIdent *ast.Ident
	astPath, _ := astutil.PathEnclosingInterval(f.GetAST(ctx), pos, pos)
	switch node := astPath[0].(type) {
	case *ast.Ident:
		astIdent = node
	case *ast.SelectorExpr:
		astIdent = node.Sel
	}
	return f.GetPackage(ctx).GetTypesInfo().ObjectOf(astIdent)
}
