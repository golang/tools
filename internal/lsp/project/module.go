package project

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"golang.org/x/tools/go/packages"
)

type moduleInfo struct {
	Path     string    `json:"Path"`
	Main     bool      `json:"Main"`
	Dir      string    `json:"Dir"`
	GoMod    string    `json:"GoMod"`
	Version  string    `json:"Version"`
	Time     time.Time `json:"Time"`
	Indirect bool      `json:"Indirect"`
}

type module struct {
	mu             sync.RWMutex
	workspace      *Workspace
	rootPath       string
	mainModulePath string
	moduleMap      map[string]moduleInfo
}

func newModule(workspace *Workspace, rootPath string) *module {
	return &module{workspace: workspace, rootPath: rootPath}
}

func (m *module) init() (err error) {
	err = m.doInit()
	if err != nil {
		return err
	}

	return m.buildCache()
}

func (m *module) doInit() error {
	moduleMap, err := m.readGoModule()
	if err != nil {
		return err
	}

	m.initModule(moduleMap)
	return nil
}

func (m *module) readGoModule() (map[string]moduleInfo, error) {
	buf, err := invokeGo(context.Background(), m.rootPath, "list", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}

	var modules []moduleInfo

	decoder := json.NewDecoder(buf)
	for {
		module := moduleInfo{}
		err = decoder.Decode(&module)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		modules = append(modules, module)
	}

	moduleMap := map[string]moduleInfo{}
	for _, module := range modules {
		if module.Dir == "" {
			// module define in go.mod but not in ${GOMOD}
			continue
		}
		moduleMap[lowerDriver(module.Dir)] = module
	}

	return moduleMap, nil
}

func (m *module) initModule(moduleMap map[string]moduleInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, module := range moduleMap {
		if module.Main {
			m.mainModulePath = module.Path
		}
	}

	m.moduleMap = moduleMap
}

func (m *module) checkModuleCache() (bool, error) {
	moduleMap, err := m.readGoModule()
	if err != nil {
		return false, err
	}

	if !m.hasChanged(moduleMap) {
		return false, nil
	}

	m.initModule(moduleMap)
	return true, nil
}

func (m *module) rebuildCache() (bool, error) {
	rebuild, err := m.checkModuleCache()
	if err != nil {
		return false, err
	}

	if !rebuild {
		return false, nil
	}

	err = m.buildCache()
	return err == nil, err
}

func (m *module) hasChanged(moduleMap map[string]moduleInfo) bool {
	for dir := range moduleMap {
		// there are some new module add into go.mod
		if _, ok := m.moduleMap[dir]; !ok {
			return true
		}
	}

	return false
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
