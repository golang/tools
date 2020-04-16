// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package metric aggregates events into metrics that can be exported.
package metric

import (
	"context"
	"sync"
	"time"

	"golang.org/x/tools/internal/telemetry/event"
)

var Entries = event.NewKey("metric_entries", "The set of metrics calculated for an event")

type Config struct {
	subscribers map[interface{}][]subscriber
}

type subscriber func(time.Time, event.TagMap, event.Tag) Data

func (e *Config) subscribe(key event.Key, s subscriber) {
	if e.subscribers == nil {
		e.subscribers = make(map[interface{}][]subscriber)
	}
	e.subscribers[key] = append(e.subscribers[key], s)
}

func (e *Config) Exporter(output event.Exporter) event.Exporter {
	var mu sync.Mutex
	return func(ctx context.Context, ev event.Event, tagMap event.TagMap) context.Context {
		if !ev.IsRecord() {
			return output(ctx, ev, tagMap)
		}
		mu.Lock()
		defer mu.Unlock()
		var metrics []Data
		for index := 0; ev.Valid(index); index++ {
			tag := ev.Tag(index)
			if !tag.Valid() {
				continue
			}
			id := tag.Key()
			if list := e.subscribers[id]; len(list) > 0 {
				for _, s := range list {
					metrics = append(metrics, s(ev.At, tagMap, tag))
				}
			}
		}
		tagMap = event.MergeTagMaps(event.NewTagMap(Entries.Of(metrics)), tagMap)
		return output(ctx, ev, tagMap)
	}
}
