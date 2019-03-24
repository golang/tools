package project

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/internal/lsp/protocol"

	"golang.org/x/tools/internal/lsp/cache"

	"golang.org/x/tools/internal/lsp/source"

	"golang.org/x/tools/go/packages"
)

const (
	goext           = ".go"
	gomod           = "go.mod"
	vendor          = "vendor"
	gopathEnv       = "GOPATH"
	go111module     = "GO111MODULE"
	emacsLockPrefix = ".#"
)

var (
	goroot  = getGoRoot()
	gopaths = getGoPaths()
)

func getGoRoot() string {
	root := runtime.GOROOT()
	root = filepath.ToSlash(filepath.Join(root, "src"))
	return lowerDriver(root)
}

func getGoPaths() []string {
	gopath := os.Getenv(gopathEnv)
	if gopath == "" {
		gopath = filepath.Join(os.Getenv("HOME"), "go")
	}

	paths := strings.Split(gopath, string(os.PathListSeparator))
	return paths
}

// FindPackageFunc matches the signature of loader.Config.FindPackage, except
// also takes a context.Context.
type FindPackageFunc func(project *Workspace, importPath string) (source.Package, error)

// Workspace workspace struct
type Workspace struct {
	context       context.Context
	client        protocol.Client
	view          *cache.View
	rootPath      string
	modules       []*module
	gopath        *gopath
	cached        bool
	cache         cache.GlobalCache
	changedCount  int
	lastBuildTime time.Time
}

func New(ctx context.Context, client protocol.Client, rootPath string, view *cache.View) *Workspace {
	p := &Workspace{
		context:  ctx,
		client:   client,
		view:     view,
		rootPath: lowerDriver(rootPath),
	}
	return p
}

func (w *Workspace) notify(err error) {
	if err != nil {
		w.notifyLog(fmt.Sprintf("notify: %s\n", err))
	}
}

// Init init workspace
func (w *Workspace) Init() {
	w.cache = cache.NewCache()
	w.view.SetCache(w.cache)
	go w.buildCache()
}

// Init init workspace
func (w *Workspace) buildCache() {
	start := time.Now()

	defer func() {
		elapsedTime := time.Since(start) / time.Second
		message := fmt.Sprintf("load %s successfully! elapsed time: %d seconds, cache: %t, go module: %t.",
			w.rootPath, elapsedTime, w.cached, len(w.modules) > 0)
		w.notifyInfo(message)
	}()

	err := w.createModuleCache()
	w.notify(err)
	w.lastBuildTime = time.Now()

	w.fsnotify()
}

func (w *Workspace) fsnotify() {
	if !w.cached {
		return
	}

	subject := newSubject(w)
	go subject.notify()
}

func (w *Workspace) getImportPath() string {
	for _, path := range gopaths {
		path = lowerDriver(filepath.ToSlash(path))
		srcDir := filepath.Join(path, "src")
		if strings.HasPrefix(w.rootPath, srcDir) && w.rootPath != srcDir {
			return filepath.ToSlash(w.rootPath[len(srcDir)+1:])
		}
	}

	return ""
}

func (w *Workspace) isUnderGoroot() bool {
	return strings.HasPrefix(w.rootPath, goroot)
}

var siteLenMap = map[string]int{
	"github.com": 3,
	"golang.org": 3,
	"gopkg.in":   2,
}

func (w *Workspace) createModuleCache() error {
	value := os.Getenv(go111module)

	if value == "on" {
		w.notifyLog("GO111MODULE=on, module mode")
		gomodList := w.findGoModFiles()
		return w.createGoModule(gomodList)
	}

	if w.isUnderGoroot() {
		w.notifyLog(fmt.Sprintf("%s under go root dir %s", w.rootPath, goroot))
		return w.createGoPath("", true)
	}

	importPath := w.getImportPath()
	w.notifyLog(fmt.Sprintf("GOPATH: %v, import path: %s", gopaths, importPath))
	if (value == "" || value == "auto") && importPath == "" {
		w.notifyLog("GO111MODULE=auto, module mode")
		gomodList := w.findGoModFiles()
		return w.createGoModule(gomodList)
	}

	if importPath == "" {
		return fmt.Errorf("%s is out of GOPATH Workspace %v", w.rootPath, gopaths)
	}

	dirs := strings.Split(importPath, "/")
	siteLen := siteLenMap[dirs[0]]

	if len(dirs) < siteLen {
		return fmt.Errorf("%s is not correct root dir of workspace.", w.rootPath)
	}

	w.notifyLog("GOPATH mode")
	return w.createGoPath(importPath, false)
}

func (w *Workspace) createGoModule(gomodList []string) error {
	for _, v := range gomodList {
		module := newModule(w, lowerDriver(filepath.Dir(v)))
		err := module.init()
		w.notify(err)
		w.modules = append(w.modules, module)
	}

	if len(w.modules) == 0 {
		return nil
	}

	w.cached = true
	sort.Slice(w.modules, func(i, j int) bool {
		return w.modules[i].rootPath >= w.modules[j].rootPath
	})

	return nil
}

func (w *Workspace) createGoPath(importPath string, underGoroot bool) error {
	gopath := newGopath(w, w.rootPath, importPath, underGoroot)
	err := gopath.init()
	w.cached = err == nil
	return err
}

func (w *Workspace) findGoModFiles() []string {
	var gomodList []string
	walkFunc := func(path string, name string) {
		if name == gomod {
			fullpath := filepath.Join(path, name)
			gomodList = append(gomodList, fullpath)
			w.notifyLog(fullpath)
		}
	}

	err := w.walkDir(w.rootPath, 0, walkFunc)
	w.notify(err)
	return gomodList
}

var defaultExcludeDir = []string{".git", ".svn", ".hg", ".vscode", ".idea", "node_modules", vendor}

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

func (w *Workspace) update(eventName string) {
	if w.needRebuild(eventName) {
		w.notifyLog("fsnotify " + eventName)
		w.cache = cache.NewCache()
		w.rebuildGopapthCache(eventName)
		w.rebuildModuleCache(eventName)
		w.lastBuildTime = time.Now()

		w.view.SetCache(w.cache)
	}
}

func (w *Workspace) needRebuild(eventName string) bool {
	if strings.HasSuffix(eventName, gomod) {
		return true
	}

	if strings.HasPrefix(eventName, emacsLockPrefix) {
		return false
	}

	if !strings.HasSuffix(eventName, goext) {
		return false
	}
	w.changedCount++
	if w.changedCount > 20 {
		w.changedCount = 0
		return true
	}

	return time.Now().Sub(w.lastBuildTime) >= 60*time.Second
}

func (w *Workspace) rebuildGopapthCache(eventName string) {
	if w.gopath == nil {
		return
	}

	if strings.HasSuffix(eventName, w.gopath.rootPath) {
		_, _ = w.gopath.rebuildCache()
	}
}

func (w *Workspace) rebuildModuleCache(eventName string) {
	if len(w.modules) == 0 {
		return
	}

	for _, m := range w.modules {
		if strings.HasPrefix(filepath.Dir(eventName), m.rootPath) {
			rebuild, err := m.rebuildCache()
			if err != nil {
				w.notifyError(err.Error())
				return
			}

			if rebuild {
				w.notifyInfo(fmt.Sprintf("rebuild module cache for %s changed", eventName))
			}

			return
		}
	}
}

// NotifyError notify error to lsp client
func (w *Workspace) notifyError(message string) {
	_ = w.client.ShowMessage(w.context, &protocol.ShowMessageParams{Type: protocol.Error, Message: message})
}

// NotifyInfo notify info to lsp client
func (w *Workspace) notifyInfo(message string) {
	_ = w.client.ShowMessage(w.context, &protocol.ShowMessageParams{Type: protocol.Info, Message: message})
}

// NotifyLog notify log to lsp client
func (w *Workspace) notifyLog(message string) {
	_ = w.client.LogMessage(w.context, &protocol.LogMessageParams{Type: protocol.Info, Message: message})
}

func (w *Workspace) root() string {
	return w.rootPath
}

func (w *Workspace) getContext() context.Context {
	return w.context
}

// Search serach package cache
func (w *Workspace) Search(walkFunc source.WalkFunc) {
	var ranks []string
	for _, module := range w.modules {
		if module.mainModulePath == "." || module.mainModulePath == "" {
			continue
		}
		ranks = append(ranks, module.mainModulePath)
	}

	w.cache.Walk(walkFunc, ranks)
}

func (w *Workspace) setCache(pkgs []*packages.Package) {
	for _, pkg := range pkgs {
		w.cache.Add(pkg)
	}
}

func newSubject(observer Observer) Subject {
	return &fsSubject{observer: observer}
}

const windowsOS = "windows"

func isWindows() bool {
	return runtime.GOOS == windowsOS
}

func lowerDriver(path string) string {
	if isWindows() {
		return path
	}

	return strings.ToLower(path[0:1]) + path[1:]
}
