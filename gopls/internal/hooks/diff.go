// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package hooks

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sergi/go-diff/diffmatchpatch"
	"golang.org/x/tools/internal/bug"
	"golang.org/x/tools/internal/diff"
)

// structure for saving information about diffs
// while the new code is being rolled out
type diffstat struct {
	Before, After      int
	Oldedits, Newedits int
	Oldtime, Newtime   time.Duration
	Stack              string
	Msg                string `json:",omitempty"` // for errors
	Ignored            int    `json:",omitempty"` // numbr of skipped records with 0 edits
}

var (
	mu      sync.Mutex // serializes writes and protects ignored
	difffd  io.Writer
	ignored int // lots of the diff calls have 0 diffs
)

var fileonce sync.Once

func (s *diffstat) save() {
	// save log records in a file in os.TempDir().
	// diff is frequently called with identical strings, so
	// these are somewhat compressed out
	fileonce.Do(func() {
		fname := filepath.Join(os.TempDir(), fmt.Sprintf("gopls-diff-%x", os.Getpid()))
		fd, err := os.Create(fname)
		if err != nil {
			// now what?
		}
		difffd = fd
	})

	mu.Lock()
	defer mu.Unlock()
	if s.Oldedits == 0 && s.Newedits == 0 {
		if ignored < 15 {
			// keep track of repeated instances of no diffs
			// but only print every 15th
			ignored++
			return
		}
		s.Ignored = ignored + 1
	} else {
		s.Ignored = ignored
	}
	ignored = 0
	// it would be really nice to see why diff was called
	_, f, l, ok := runtime.Caller(2)
	if ok {
		var fname string
		fname = filepath.Base(f) // diff is only called from a few places
		s.Stack = fmt.Sprintf("%s:%d", fname, l)
	}
	x, err := json.Marshal(s)
	if err != nil {
		log.Print(err) // failure to print statistics should not stop gopls
	}
	fmt.Fprintf(difffd, "%s\n", x)
}

// save encrypted versions of the broken input and return the file name
// (the saved strings will have the same diff behavior as the user's strings)
func disaster(before, after string) string {
	// encrypt before and after for privacy. (randomized monoalphabetic cipher)
	// got will contain the substitution cipher
	// for the runes in before and after
	got := map[rune]rune{}
	for _, r := range before {
		got[r] = ' ' // value doesn't matter
	}
	for _, r := range after {
		got[r] = ' '
	}
	repl := initrepl(len(got))
	i := 0
	for k := range got { // randomized
		got[k] = repl[i]
		i++
	}
	// use got to encrypt before and after
	subst := func(r rune) rune { return got[r] }
	first := strings.Map(subst, before)
	second := strings.Map(subst, after)

	// one failure per session is enough, and more private.
	// this saves the last one.
	fname := fmt.Sprintf("%s/gopls-failed-%x", os.TempDir(), os.Getpid())
	fd, err := os.Create(fname)
	defer fd.Close()
	_, err = fmt.Fprintf(fd, "%s\n%s\n", first, second)
	if err != nil {
		// what do we tell the user?
		return ""
	}
	// ask the user to send us the file, somehow
	return fname
}

func initrepl(n int) []rune {
	repl := make([]rune, 0, n)
	for r := rune(0); len(repl) < n; r++ {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			repl = append(repl, r)
		}
	}
	// randomize repl
	rdr := rand.Reader
	lim := big.NewInt(int64(len(repl)))
	for i := 1; i < n; i++ {
		v, _ := rand.Int(rdr, lim)
		k := v.Int64()
		repl[i], repl[k] = repl[k], repl[i]
	}
	return repl
}

// BothDiffs edits calls both the new and old diffs, checks that the new diffs
// change before into after, and attempts to preserve some statistics.
func BothDiffs(before, after string) (edits []diff.Edit) {
	// The new diff code contains a lot of internal checks that panic when they
	// fail. This code catches the panics, or other failures, tries to save
	// the failing example (and ut wiykd ask the user to send it back to us, and
	// changes options.newDiff to 'old', if only we could figure out how.)
	stat := diffstat{Before: len(before), After: len(after)}
	now := time.Now()
	oldedits := ComputeEdits(before, after)
	stat.Oldedits = len(oldedits)
	stat.Oldtime = time.Since(now)
	defer func() {
		if r := recover(); r != nil {
			disaster(before, after)
			edits = oldedits
		}
	}()
	now = time.Now()
	newedits := diff.Strings(before, after)
	stat.Newedits = len(newedits)
	stat.Newtime = time.Now().Sub(now)
	got, err := diff.Apply(before, newedits)
	if err != nil || got != after {
		stat.Msg += "FAIL"
		disaster(before, after)
		stat.save()
		return oldedits
	}
	stat.save()
	return newedits
}

// ComputeEdits computes a diff using the github.com/sergi/go-diff implementation.
func ComputeEdits(before, after string) (edits []diff.Edit) {
	// The go-diff library has an unresolved panic (see golang/go#278774).
	// TODO(rstambler): Remove the recover once the issue has been fixed
	// upstream.
	defer func() {
		if r := recover(); r != nil {
			bug.Reportf("unable to compute edits: %s", r)
			// Report one big edit for the whole file.
			edits = []diff.Edit{{
				Start: 0,
				End:   len(before),
				New:   after,
			}}
		}
	}()
	diffs := diffmatchpatch.New().DiffMain(before, after, true)
	edits = make([]diff.Edit, 0, len(diffs))
	offset := 0
	for _, d := range diffs {
		start := offset
		switch d.Type {
		case diffmatchpatch.DiffDelete:
			offset += len(d.Text)
			edits = append(edits, diff.Edit{Start: start, End: offset})
		case diffmatchpatch.DiffEqual:
			offset += len(d.Text)
		case diffmatchpatch.DiffInsert:
			edits = append(edits, diff.Edit{Start: start, End: start, New: d.Text})
		}
	}
	return edits
}
