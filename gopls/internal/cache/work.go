// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/internal/event"
)

// A ParsedWorkFile contains the results of parsing a go.work file.
type ParsedWorkFile struct {
	URI         protocol.DocumentURI
	File        *modfile.WorkFile
	Mapper      *protocol.Mapper
	ParseErrors []*Diagnostic
}

// ParseWork parses a go.work file, using a cache. It may return partial results and an error.
func (s *Snapshot) ParseWork(ctx context.Context, fh file.Handle) (*ParsedWorkFile, error) {
	uri := fh.URI()

	s.mu.Lock()
	entry, hit := s.parseWorkPromises.Get(uri)
	s.mu.Unlock()

	type parseWorkKey file.Identity
	type parseWorkResult struct {
		parsed *ParsedWorkFile
		err    error
	}

	// cache miss?
	if !hit {
		promise, release := s.store.Promise(parseWorkKey(fh.Identity()), func(ctx context.Context, _ any) any {
			parsed, err := parseWorkImpl(ctx, fh)
			return parseWorkResult{parsed, err}
		})

		entry = promise
		s.mu.Lock()
		s.parseWorkPromises.Set(uri, entry, func(_, _ any) { release() })
		s.mu.Unlock()
	}

	// Await result.
	v, err := s.awaitPromise(ctx, entry)
	if err != nil {
		return nil, err
	}
	res := v.(parseWorkResult)
	return res.parsed, res.err
}

// parseWorkImpl parses a go.work file. It may return partial results and an error.
func parseWorkImpl(ctx context.Context, fh file.Handle) (*ParsedWorkFile, error) {
	_, done := event.Start(ctx, "cache.ParseWork", label.URI.Of(fh.URI()))
	defer done()

	content, err := fh.Content()
	if err != nil {
		return nil, err
	}
	m := protocol.NewMapper(fh.URI(), content)
	file, parseErr := modfile.ParseWork(fh.URI().Path(), content, nil)
	// Attempt to convert the error to a standardized parse error.
	var parseErrors []*Diagnostic
	if parseErr != nil {
		mfErrList, ok := parseErr.(modfile.ErrorList)
		if !ok {
			return nil, fmt.Errorf("unexpected parse error type %v", parseErr)
		}
		for _, mfErr := range mfErrList {
			rng, err := m.OffsetRange(mfErr.Pos.Byte, mfErr.Pos.Byte)
			if err != nil {
				return nil, err
			}
			parseErrors = append(parseErrors, &Diagnostic{
				URI:      fh.URI(),
				Range:    rng,
				Severity: protocol.SeverityError,
				Source:   ParseError,
				Message:  mfErr.Err.Error(),
			})
		}
	}
	return &ParsedWorkFile{
		URI:         fh.URI(),
		Mapper:      m,
		File:        file,
		ParseErrors: parseErrors,
	}, parseErr
}
