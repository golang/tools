package cache

import (
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
)

const (
	modExt    = "go.mod"
	vendorDir = "vendor"
)

// Workspace holds the go project workspace information
type Workspace struct {
	context  context.Context
	config   *packages.Config
	client   protocol.Client
	rootPath string
	modules  []*module
	cache    GlobalCache
}

// NewWorkspace creates a workspace for a workspace folder
func NewWorkspace(client protocol.Client, config *packages.Config) *Workspace {
	return &Workspace{
		context:  config.Context,
		client:   client,
		config:   config,
		rootPath: config.Dir,
	}
}

// Init inits workspace
func (w *Workspace) Init() {
	w.cache = NewCache()
	go w.buildCache()
}

func (w *Workspace) buildCache() {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start) / time.Second
		msg := fmt.Sprintf("load %s successfully! elapsed time: %d seconds.", w.rootPath, elapsed)
		w.notifyInfo(msg)
	}()

	err := w.createModuleCache()
	w.notify(err)
}

func (w *Workspace) createModuleCache() error {
	modFiles := w.findGoModFiles()
	if len(modFiles) > 0 {
		return w.createGoModule(modFiles)
	}

	return w.createGoPath()
}

func (w *Workspace) createGoModule(modFiles []string) error {
	for _, v := range modFiles {
		module := newModule(w, filepath.Dir(v))
		err := module.buildCache()
		w.notify(err)
		w.modules = append(w.modules, module)
	}

	if len(w.modules) == 0 {
		return nil
	}

	sort.Slice(w.modules, func(i, j int) bool {
		return w.modules[i].rootPath >= w.modules[j].rootPath
	})

	return nil
}

func (w *Workspace) createGoPath() error {
	m := newModule(w, w.rootPath)
	w.modules = append(w.modules, m)
	return m.buildCache()
}

func (w *Workspace) findGoModFiles() []string {
	var modFiles []string
	walkFunc := func(path string, name string) {
		if name == modExt {
			fullPath := filepath.Join(path, name)
			modFiles = append(modFiles, fullPath)
			w.notifyLog(fullPath)
		}
	}

	err := w.walkDir(w.rootPath, 0, walkFunc)
	w.notify(err)
	return modFiles
}

var defaultExcludeDir = []string{".git", ".svn", ".hg", ".vscode", ".idea", "node_modules", vendorDir}

func isExclude(dir string) bool {
	for _, d := range defaultExcludeDir {
		if d == dir {
			return true
		}
	}

	return false
}

func (w *Workspace) walkDir(rootDir string, level int, walkFunc func(string, string)) error {
	if level > 8 {
		return nil
	}

	files, err := ioutil.ReadDir(rootDir)
	if err != nil {
		w.notify(err)
		return nil
	}

	for _, fi := range files {
		if isExclude(fi.Name()) {
			continue
		}

		if fi.IsDir() {
			level++
			err = w.walkDir(filepath.Join(rootDir, fi.Name()), level, walkFunc)
			if err != nil {
				return err
			}
			level--
		} else {
			walkFunc(rootDir, fi.Name())
		}
	}

	return nil
}

func (w *Workspace) rebuildCache(eventName string) {
	if len(w.modules) == 0 {
		return
	}

	for _, m := range w.modules {
		if strings.HasPrefix(filepath.Dir(eventName), m.rootPath) {
			err := m.buildCache()
			if err != nil {
				w.notifyError(err.Error())
			}
		}
	}
}

func (w *Workspace) notify(err error) {
	if err != nil {
		w.notifyLog(fmt.Sprintf("notify: %s\n", err))
	}
}

// NotifyError notify error to lsp client
func (w *Workspace) notifyError(message string) {
	if w.client == nil {
		return
	}
	_ = w.client.ShowMessage(w.context, &protocol.ShowMessageParams{Type: protocol.Error, Message: message})
}

// NotifyInfo notify info to lsp client
func (w *Workspace) notifyInfo(message string) {
	if w.client == nil {
		return
	}
	_ = w.client.ShowMessage(w.context, &protocol.ShowMessageParams{Type: protocol.Info, Message: message})
}

// NotifyLog notify log to lsp client
func (w *Workspace) notifyLog(message string) {
	if w.client == nil {
		return
	}
	_ = w.client.LogMessage(w.context, &protocol.LogMessageParams{Type: protocol.Info, Message: message})
}

func (w *Workspace) root() string {
	return w.rootPath
}

func (w *Workspace) getContext() context.Context {
	return w.context
}

// Search search package cache
func (w *Workspace) Search(walkFunc source.WalkFunc) {
	if w == nil {
		return
	}
	w.cache.Walk(walkFunc)
}

func (w *Workspace) setCache(pkgs []*packages.Package) {
	for _, pkg := range pkgs {
		w.cache.Add(pkg)
	}
}

func (w *Workspace) Put(pkg *Package) {
	if w == nil {
		return
	}
	w.cache.Put(pkg)
}
