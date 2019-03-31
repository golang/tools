package cache

import (
	"os"
	"sort"
	"strings"
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
func (c *globalCache) Walk(walkFunc source.WalkFunc, ranks []string) {
	var pkgPaths []string
	for id := range c.pathMap {
		pkgPaths = append(pkgPaths, id)
	}

	getRank := func(id string) int {
		var i int
		for i = 0; i < len(ranks); i++ {
			if strings.HasPrefix(id, ranks[i]) {
				return i
			}
		}

		if strings.Contains(id, ".") {
			return i
		}

		return i + 1
	}

	sort.Slice(pkgPaths, func(i, j int) bool {
		r1 := getRank(pkgPaths[i])
		r2 := getRank(pkgPaths[j])
		if r1 < r2 {
			return true
		}

		if r1 == r2 {
			return pkgPaths[i] <= pkgPaths[j]
		}

		return false
	})

	c.walk(pkgPaths, walkFunc)
}

func (c *globalCache) walk(pkgPaths []string, walkFunc source.WalkFunc) {
	for _, pkgPath := range pkgPaths {
		pkg := c.Get(pkgPath)
		if walkFunc(pkg) {
			return
		}
	}

	return
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
	return pkg.imports[pkgPath]
}

// SetCache set a global cache into view
func (v *View) SetCache(cache GlobalCache) {
	v.mu.Lock()
	v.gcache = cache
	v.mu.Unlock()
}

// Cache get global cache
func (v *View) Cache() GlobalCache {
	v.mu.Lock()
	cache := v.gcache
	v.mu.Unlock()
	return cache
}
