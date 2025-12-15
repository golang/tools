// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package template

import (
	"context"
	"fmt"
	"regexp"

	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
)

func Highlight(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, loc protocol.Position) ([]protocol.DocumentHighlight, error) {
	buf, err := fh.Content()
	if err != nil {
		return nil, err
	}
	p := parseBuffer(fh.URI(), buf)
	pos, err := p.mapper.PositionOffset(loc)
	if err != nil {
		return nil, err
	}

	if p.parseErr == nil {
		for _, s := range p.symbols {
			if s.start <= pos && pos < s.start+s.len {
				return markSymbols(p, s)
			}
		}
	}

	// these tokens exist whether or not there was a parse error
	// (symbols require a successful parse)
	for _, tok := range p.tokens {
		if tok.start <= pos && pos < tok.end {
			wordAt := wordAt(p.buf, pos)
			if len(wordAt) > 0 {
				return markWordInToken(p, wordAt)
			}
		}
	}

	// TODO: find the 'word' at pos, etc: someday
	// until then we get the default action, which doesn't respect word boundaries
	return nil, nil
}

func markSymbols(p *parsed, sym symbol) ([]protocol.DocumentHighlight, error) {
	var ans []protocol.DocumentHighlight
	for _, s := range p.symbols {
		if s.name == sym.name {
			kind := protocol.Read
			if s.vardef {
				kind = protocol.Write
			}
			rng, err := p.mapper.OffsetRange(s.offsets())
			if err != nil {
				return nil, err
			}
			ans = append(ans, protocol.DocumentHighlight{
				Range: rng,
				Kind:  kind,
			})
		}
	}
	return ans, nil
}

// A token is {{...}}, and this marks words in the token that equal the give word
func markWordInToken(p *parsed, wordAt string) ([]protocol.DocumentHighlight, error) {
	var ans []protocol.DocumentHighlight
	pat, err := regexp.Compile(fmt.Sprintf(`\b%s\b`, wordAt))
	if err != nil {
		return nil, fmt.Errorf("%q: unmatchable word (%v)", wordAt, err)
	}
	for _, tok := range p.tokens {
		matches := pat.FindAllIndex(p.buf[tok.start:tok.end], -1)
		for _, match := range matches {
			rng, err := p.mapper.OffsetRange(match[0], match[1])
			if err != nil {
				return nil, err
			}
			ans = append(ans, protocol.DocumentHighlight{
				Range: rng,
				Kind:  protocol.Text,
			})
		}
	}
	return ans, nil
}

// wordAt returns the word the cursor is in (meaning in or just before)
func wordAt(buf []byte, pos int) string {
	if pos >= len(buf) {
		return ""
	}
	after := moreRe.Find(buf[pos:])
	if len(after) == 0 {
		return "" // end of the word
	}
	got := wordRe.Find(buf[:pos+len(after)])
	return string(got)
}

var (
	wordRe = regexp.MustCompile(`[$]?\w+$`)
	moreRe = regexp.MustCompile(`^[$]?\w+`)
)
