// +build darwin

package project

import (
	"time"

	"github.com/fsnotify/fsevents"
)

type fsSubject struct {
	observer Observer
}

func (o *fsSubject) notify() {
	dev, err := fsevents.DeviceForPath(o.observer.root())
	if err != nil {
		o.observer.notifyLog(err.Error())
		return
	}

	es := &fsevents.EventStream{
		Paths:   []string{o.observer.root()},
		Latency: 500 * time.Millisecond,
		Device:  dev,
		Flags:   fsevents.FileEvents | fsevents.WatchRoot}
	es.Start()

	go func() {
		defer func() {
			es.Stop()
		}()

		for {
			select {
			case <-o.observer.getContext().Done():
				return
			case events, ok := <-es.Events:
				if !ok {
					return
				}

				for _, event := range events {
					if event.Flags&fsevents.ItemIsFile != 0 &&
						event.Flags&fsevents.ItemCreated != 0 ||
						event.Flags&fsevents.ItemModified != 0 ||
						event.Flags&fsevents.ItemRemoved != 0 ||
						event.Flags&fsevents.ItemRenamed != 0 {
						o.observer.update("/" + event.Path)
					}
				}
			}
		}
	}()
}
