package cache

import (
	"sync"

	"golang.org/x/tools/go/packages"
)

type module struct {
	mu       sync.RWMutex
	w        *Workspace
	rootPath string
}

func newModule(w *Workspace, rootPath string) *module {
	return &module{w: w, rootPath: rootPath}
}

func (m *module) buildCache() error {
	cfg := packages.Config{
		Dir:  m.rootPath,
		Fset: m.w.session.cache.FileSet(),
		Mode: packages.LoadAllSyntax,
	}

	pkgList, err := packages.Load(&cfg, cfg.Dir+"/...")
	if err != nil {
		return err
	}

	m.w.setCache(pkgList)
	return nil
}
