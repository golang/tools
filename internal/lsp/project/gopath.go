package project

import (
	"sync"

	"golang.org/x/tools/go/packages"
)

type gopath struct {
	mu          sync.RWMutex
	workspace   *Workspace
	rootPath    string
	importPath  string
	underGoroot bool
}

func newGopath(workspace *Workspace, rootPath string, importPath string, underGoroot bool) *gopath {
	return &gopath{
		workspace:   workspace,
		rootPath:    rootPath,
		importPath:  importPath,
		underGoroot: underGoroot,
	}
}

func (p *gopath) init() (err error) {
	err = p.doInit()
	if err != nil {
		return err
	}

	return p.buildCache()
}

func (p *gopath) doInit() error {
	return nil
}

func (p *gopath) rebuildCache() (bool, error) {
	err := p.buildCache()
	return err == nil, err
}

func (p *gopath) buildCache() error {
	cfg := p.workspace.view.Config
	cfg.Dir = p.rootPath
	cfg.Mode = packages.LoadAllSyntax

	var pattern string
	if p.underGoroot {
		pattern = cfg.Dir
	} else {
		pattern = p.importPath + "/..."
	}

	pkgs, err := packages.Load(&cfg, pattern)
	if err != nil {
		return err
	}

	p.workspace.setCache(pkgs)
	return nil
}
