package project

import (
	"golang.org/x/tools/go/packages"
	"sync"
)

type module struct {
	mu          sync.RWMutex
	workspace   *Workspace
	rootPath    string
	modulePath  string
	underGoRoot bool
}

func newModule(workspace *Workspace, rootPath string) *module {
	return &module{workspace: workspace, rootPath: rootPath}
}

func (m *module) buildCache() error {
	cfg := m.workspace.view.Config
	cfg.Dir = m.rootPath
	cfg.Mode = packages.LoadAllSyntax
	pattern := cfg.Dir + "/..."

	pkgs, err := packages.Load(&cfg, pattern)
	if err != nil {
		return err
	}

	m.workspace.setCache(pkgs)
	return nil
}
