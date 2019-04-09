package cache

import (
	"os"
	"sync"
	"time"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/internal/lsp/source"
)

// GlobalCache global package cache for project
type GlobalCache interface {
	source.Cache
	Add(pkg *packages.Package)
	Put(pkg *Package)
}

type globalPackage struct {
	pkg     *Package
	modTime time.Time
}

type path2Package map[string]*globalPackage

func getPackageModTime(pkg *Package) time.Time {
	if pkg == nil || len(pkg.GetFilenames()) == 0 {
		return time.Time{}
	}

	dir := pkg.GetFilenames()[0]
	fi, err := os.Stat(dir)
	if err != nil {
		return time.Time{}
	}

	return fi.ModTime()
}

type globalCache struct {
	mu      sync.RWMutex
	pathMap path2Package
}

// NewCache new a package cache
func NewCache() *globalCache {
	return &globalCache{pathMap: path2Package{}}
}

// Put put package into global cache
func (c *globalCache) Put(pkg *Package) {
	c.mu.Lock()
	c.put(pkg)
	c.mu.Unlock()
}

func (c *globalCache) put(pkg *Package) {
	pkgPath := pkg.GetTypes().Path()
	p := &globalPackage{pkg: pkg}
	c.pathMap[pkgPath] = p
}

// Get get package by package import path from global cache
func (c *globalCache) Get(pkgPath string) *Package {
	c.mu.RLock()
	p := c.get(pkgPath)
	c.mu.RUnlock()
	return p
}

// Get get package by package import path from global cache
func (c *globalCache) get(pkgPath string) *Package {
	p := c.pathMap[pkgPath]
	if p == nil {
		return nil
	}

	return p.pkg
}

// Walk walk the global package cache
func (c *globalCache) Walk(walkFunc source.WalkFunc) {
	c.walk(walkFunc)
}

func (c *globalCache) walk(walkFunc source.WalkFunc) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, pkg := range c.pathMap {
		if walkFunc(pkg.pkg) {
			return
		}
	}
}

func (c *globalCache) Add(pkg *packages.Package) {
	c.recursiveAdd(pkg, nil)
}

func (c *globalCache) recursiveAdd(pkg *packages.Package, parent *Package) {
	if p, _ := c.pathMap[pkg.PkgPath]; p != nil {
		if parent != nil {
			parent.addImport(p.pkg)
		}
		return
	}

	p := newPackage(pkg)

	for _, ip := range pkg.Imports {
		c.recursiveAdd(ip, p)
	}

	c.put(p)

	if parent != nil {
		parent.addImport(p)
	}
}

// newPackage new package
func newPackage(pkg *packages.Package) *Package {
	return &Package{
		id:        pkg.ID,
		pkgPath:   pkg.PkgPath,
		files:     pkg.CompiledGoFiles,
		syntax:    pkg.Syntax,
		errors:    pkg.Errors,
		types:     pkg.Types,
		typesInfo: pkg.TypesInfo,
		imports:   make(map[string]*Package),
	}
}

// addImport add import package
func (pkg *Package) addImport(p *Package) {
	pkg.imports[p.pkgPath] = p
}

func (pkg *Package) GetImport(pkgPath string) source.Package {
	if p, ok := pkg.imports[pkgPath]; ok {
		return p
	}

	return nil
}

// SetCache set a global cache into view
func (v *View) SetCache(cache GlobalCache) {
	v.mu.Lock()
	v.gcache = cache
	v.mu.Unlock()
}
