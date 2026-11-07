// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package export

import (
	"io"

	"golang.org/x/tools/internal/event/core"
	"golang.org/x/tools/internal/event/keys"
	"golang.org/x/tools/internal/event/label"
)

type Printer struct {
	buffer [128]byte
}

// WriteEvent writes the formatted event to w. It is not concurrency-safe.
func (p *Printer) WriteEvent(w io.Writer, ev core.Event, lm label.Map) {
	// TODO(adonovan): any function that accepts an arbitrary
	// io.Writer should really deal with errors. Use bytes.Buffer?
	buf := p.buffer[:0]
	if !ev.At().IsZero() {
		w.Write(ev.At().AppendFormat(buf, "2006/01/02 15:04:05 ")) // ignore error
	}
	msg := keys.Msg.Get(lm)
	io.WriteString(w, msg)
	if err := keys.Err.Get(lm); err != nil {
		if msg != "" {
			io.WriteString(w, ": ")
		}
		io.WriteString(w, err.Error())
	}
	for l := range ev.Labels() {
		if l.Key() == keys.Msg || l.Key() == keys.Err {
			continue
		}
		io.WriteString(w, "\n\t")
		io.WriteString(w, l.Key().Name())
		io.WriteString(w, "=")
		w.Write(l.Key().Append(buf, l)) // ignore error
	}
	io.WriteString(w, "\n")
}
