// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/golang/completion"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/telemetry"
	"golang.org/x/tools/gopls/internal/template"
	"golang.org/x/tools/gopls/internal/work"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/tag"
)

func (s *server) Completion(ctx context.Context, params *protocol.CompletionParams) (_ *protocol.CompletionList, rerr error) {
	recordLatency := telemetry.StartLatencyTimer("completion")
	defer func() {
		recordLatency(ctx, rerr)
	}()

	ctx, done := event.Start(ctx, "lsp.Server.completion", tag.URI.Of(params.TextDocument.URI))
	defer done()

	fh, snapshot, release, err := s.fileOf(ctx, params.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	defer release()

	var candidates []completion.CompletionItem
	var surrounding *completion.Selection
	switch snapshot.FileKind(fh) {
	case file.Go:
		candidates, surrounding, err = completion.Completion(ctx, snapshot, fh, params.Position, params.Context)
	case file.Mod:
		candidates, surrounding = nil, nil
	case file.Work:
		cl, err := work.Completion(ctx, snapshot, fh, params.Position)
		if err != nil {
			break
		}
		return cl, nil
	case file.Tmpl:
		var cl *protocol.CompletionList
		cl, err = template.Completion(ctx, snapshot, fh, params.Position, params.Context)
		if err != nil {
			break // use common error handling, candidates==nil
		}
		return cl, nil
	}
	if err != nil {
		event.Error(ctx, "no completions found", err, tag.Position.Of(params.Position))
	}
	if candidates == nil {
		complEmpty.Inc()
		return &protocol.CompletionList{
			IsIncomplete: true,
			Items:        []protocol.CompletionItem{},
		}, nil
	}

	rng, err := surrounding.Range()
	if err != nil {
		return nil, err
	}

	// When using deep completions/fuzzy matching, report results as incomplete so
	// client fetches updated completions after every key stroke.
	options := snapshot.Options()
	incompleteResults := options.DeepCompletion || options.Matcher == settings.Fuzzy

	items := toProtocolCompletionItems(candidates, rng, options)
	if snapshot.FileKind(fh) == file.Go {
		s.saveLastCompletion(fh.URI(), fh.Version(), items, params.Position)
	}

	if len(items) > 10 {
		// TODO(pjw): long completions are ok for field lists
		complLong.Inc()
	} else {
		complShort.Inc()
	}
	return &protocol.CompletionList{
		IsIncomplete: incompleteResults,
		Items:        items,
	}, nil
}

func (s *server) saveLastCompletion(uri protocol.DocumentURI, version int32, items []protocol.CompletionItem, pos protocol.Position) {
	s.efficacyMu.Lock()
	defer s.efficacyMu.Unlock()
	s.efficacyVersion = version
	s.efficacyURI = uri
	s.efficacyPos = pos
	s.efficacyItems = items
}

func toProtocolCompletionItems(candidates []completion.CompletionItem, rng protocol.Range, options *settings.Options) []protocol.CompletionItem {
	var (
		items                  = make([]protocol.CompletionItem, 0, len(candidates))
		numDeepCompletionsSeen int
	)
	for i, candidate := range candidates {
		// Limit the number of deep completions to not overwhelm the user in cases
		// with dozens of deep completion matches.
		if candidate.Depth > 0 {
			if !options.DeepCompletion {
				continue
			}
			if numDeepCompletionsSeen >= completion.MaxDeepCompletions {
				continue
			}
			numDeepCompletionsSeen++
		}
		insertText := candidate.InsertText
		if options.InsertTextFormat == protocol.SnippetTextFormat {
			insertText = candidate.Snippet()
		}

		// This can happen if the client has snippets disabled but the
		// candidate only supports snippet insertion.
		if insertText == "" {
			continue
		}

		doc := &protocol.Or_CompletionItem_documentation{
			Value: protocol.MarkupContent{
				Kind:  protocol.Markdown,
				Value: golang.CommentToMarkdown(candidate.Documentation, options),
			},
		}
		if options.PreferredContentFormat != protocol.Markdown {
			doc.Value = candidate.Documentation
		}
		item := protocol.CompletionItem{
			Label:  candidate.Label,
			Detail: candidate.Detail,
			Kind:   candidate.Kind,
			TextEdit: &protocol.TextEdit{
				NewText: insertText,
				Range:   rng,
			},
			InsertTextFormat:    &options.InsertTextFormat,
			AdditionalTextEdits: candidate.AdditionalTextEdits,
			// This is a hack so that the client sorts completion results in the order
			// according to their score. This can be removed upon the resolution of
			// https://github.com/Microsoft/language-server-protocol/issues/348.
			SortText: fmt.Sprintf("%05d", i),

			// Trim operators (VSCode doesn't like weird characters in
			// filterText).
			FilterText: strings.TrimLeft(candidate.InsertText, "&*"),

			Preselect:     i == 0,
			Documentation: doc,
			Tags:          protocol.NonNilSlice(candidate.Tags),
			Deprecated:    candidate.Deprecated,
		}
		items = append(items, item)
	}
	return items
}
