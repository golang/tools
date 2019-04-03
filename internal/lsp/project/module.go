package project

import (
	"golang.org/x/tools/go/packages"
	"sync"
)

type module struct {
	mu          sync.RWMutex
	w           *Workspace
	rootPath    string
}

func newModule(w *Workspace, rootPath string) *module {
	return &module{w: w, rootPath: rootPath}
}

func (m *module) buildCache() error {
	cfg := m.w.view.Config
	cfg.Dir = m.rootPath
	cfg.Mode = packages.LoadAllSyntax
	pattern := cfg.Dir + "/..."

	pkgList, err := packages.Load(&cfg, pattern)
	if err != nil {
		return err
	}

	m.w.setCache(pkgList)
	return nil
}
