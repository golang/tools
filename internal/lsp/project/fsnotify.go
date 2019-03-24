// +build !darwin

package project

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

type fsSubject struct {
	observer Observer
	watched  int
}

func (s *fsSubject) notify() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.observer.notifyLog(err.Error())
		return
	}

	s.watch(s.observer.root(), watcher)

	s.observer.notifyLog(fmt.Sprintf("fsnotify watch dir number: %d", s.watched))

	go func() {
		defer func() {
			_ = watcher.Close()
		}()

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				if event.Op&fsnotify.Write == fsnotify.Write {
					s.observer.update(event.Name)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				s.observer.notifyLog(fmt.Sprintf("receive an fsNotify error: %s", err))
			}
		}
	}()
}

func (s *fsSubject) watch(rootDir string, watcher *fsnotify.Watcher) {
	err := watcher.Add(rootDir)
	if err != nil {
		s.observer.notifyLog(err.Error())
	}

	if err == nil {
		s.watched++
	}
	//p.NotifyLog(fmt.Sprintf("watch %s", rootPath))

	files, err := ioutil.ReadDir(rootDir)
	if err != nil {
		s.observer.notifyLog(err.Error())
		return
	}

	for _, fi := range files {
		if isExclude(fi.Name()) {
			continue
		}

		fullpath := filepath.Join(rootDir, fi.Name())
		if fi.IsDir() {
			s.watch(fullpath, watcher)
		}
	}
}
