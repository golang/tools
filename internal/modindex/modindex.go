// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package modindex contains code for building and searching an index to
// the Go module cache. The directory containing the index, returned by
// IndexDir(), contains a file index-name-<ver> that contains the name
// of the current index. We believe writing that short file is atomic.
// ReadIndex reads that file to get the file name of the index.
// WriteIndex writes an index with a unique name and then
// writes that name into a new version of index-name-<ver>.
// (<ver> stands for the CurrentVersion of the index format.)
package modindex

import (
	"log"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Modindex writes an index current as of when it is called.
// If clear is true the index is constructed from all of GOMODCACHE
// otherwise the index is constructed from the last previous index
// and the updates to the cache.
func IndexModCache(cachedir string, clear bool) error {
	cachedir, err := filepath.Abs(cachedir)
	if err != nil {
		return err
	}
	cd := Abspath(cachedir)
	future := time.Now().Add(24 * time.Hour) // safely in the future
	err = modindexTimed(future, cd, clear)
	if err != nil {
		return err
	}
	return nil
}

// modindexTimed writes an index current as of onlyBefore.
// If clear is true the index is constructed from all of GOMODCACHE
// otherwise the index is constructed from the last previous index
// and all the updates to the cache before onlyBefore.
// (this is useful for testing; perhaps it should not be exported)
func modindexTimed(onlyBefore time.Time, cachedir Abspath, clear bool) error {
	var curIndex *Index
	if !clear {
		var err error
		curIndex, err = ReadIndex(string(cachedir))
		if clear && err != nil {
			return err
		}
		// TODO(pjw): check that most of those directorie still exist
	}
	cfg := &work{
		onlyBefore: onlyBefore,
		oldIndex:   curIndex,
		cacheDir:   cachedir,
	}
	if curIndex != nil {
		cfg.onlyAfter = curIndex.Changed
	}
	if err := cfg.buildIndex(); err != nil {
		return err
	}
	if err := cfg.writeIndex(); err != nil {
		return err
	}
	return nil
}

type work struct {
	onlyBefore time.Time // do not use directories later than this
	onlyAfter  time.Time // only interested in directories after this
	// directories from before onlyAfter come from oldIndex
	oldIndex *Index
	newIndex *Index
	cacheDir Abspath
}

func (w *work) buildIndex() error {
	// The effective date of the new index should be at least
	// slightly earlier than when the directories are scanned
	// so set it now.
	w.newIndex = &Index{Changed: time.Now(), Cachedir: w.cacheDir}
	dirs := findDirs(string(w.cacheDir), w.onlyAfter, w.onlyBefore)
	newdirs, err := byImportPath(dirs)
	if err != nil {
		return err
	}
	log.Printf("%d dirs, %d ips", len(dirs), len(newdirs))
	// for each import path it might occur only in newdirs,
	// only in w.oldIndex, or in both.
	// If it occurs in both, use the semantically later one
	if w.oldIndex != nil {
		killed := 0
		for _, e := range w.oldIndex.Entries {
			found, ok := newdirs[e.ImportPath]
			if !ok {
				continue
			}
			if semver.Compare(found[0].version, e.Version) > 0 {
				// the new one is better, disable the old one
				e.ImportPath = ""
				killed++
			} else {
				// use the old one, forget the new one
				delete(newdirs, e.ImportPath)
			}
		}
		log.Printf("%d killed, %d ips", killed, len(newdirs))
	}
	// Build the skeleton of the new index using newdirs,
	// and include the surviving parts of the old index
	if w.oldIndex != nil {
		for _, e := range w.oldIndex.Entries {
			if e.ImportPath != "" {
				w.newIndex.Entries = append(w.newIndex.Entries, e)
			}
		}
	}
	for k, v := range newdirs {
		d := v[0]
		entry := Entry{
			Dir:        d.path,
			ImportPath: k,
			Version:    d.version,
		}
		w.newIndex.Entries = append(w.newIndex.Entries, entry)
	}
	// find symbols for the incomplete entries
	log.Print("not finding any symbols yet")
	// sort the entries in the new index
	slices.SortFunc(w.newIndex.Entries, func(l, r Entry) int {
		if n := strings.Compare(l.PkgName, r.PkgName); n != 0 {
			return n
		}
		return strings.Compare(l.ImportPath, r.ImportPath)
	})
	return nil
}

func (w *work) writeIndex() error {
	return writeIndex(w.cacheDir, w.newIndex)
}
