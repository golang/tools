package lsp

import (
	"context"
	"errors"

	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
)

// formatRange formats a document with a given range.
func formatRange(ctx context.Context, v source.View, uri protocol.DocumentURI, rng *protocol.Range) ([]protocol.TextEdit, error) {
	sourceURI, err := fromProtocolURI(uri)
	if err != nil {
		return nil, err
	}
	f, err := v.GetFile(ctx, sourceURI)
	if err != nil {
		return nil, err
	}
	tok, err := f.GetToken()
	if err != nil {
		return nil, err
	}
	var r source.Range
	if rng == nil {
		r.Start = tok.Pos(0)
		r.End = tok.Pos(tok.Size())
	} else {
		r = fromProtocolRange(tok, *rng)
	}
	edits, err := source.Format(ctx, f, r)
	if err != nil {
		return nil, err
	}

	protocolEdits, err := toProtocolEdits(f, edits)
	if err != nil {
		return nil, err
	}
	return protocolEdits, nil
}

func toProtocolEdits(f source.File, edits []source.TextEdit) ([]protocol.TextEdit, error) {
	if edits == nil {
		return nil, errors.New("toProtocolEdits, edits == nil")
	}
	tok, err := f.GetToken()
	if err != nil {
		return nil, err
	}
	content, err := f.GetContent()
	if err != nil {
		return nil, err
	}
	// When a file ends with an empty line, the newline character is counted
	// as part of the previous line. This causes the formatter to insert
	// another unnecessary newline on each formatting. We handle this case by
	// checking if the file already ends with a newline character.
	hasExtraNewline := content[len(content)-1] == '\n'
	result := make([]protocol.TextEdit, len(edits))
	for i, edit := range edits {
		rng := toProtocolRange(tok, edit.Range)
		// If the edit ends at the end of the file, add the extra line.
		if hasExtraNewline && tok.Offset(edit.Range.End) == len(content) {
			rng.End.Line++
			rng.End.Character = 0
		}
		result[i] = protocol.TextEdit{
			Range:   rng,
			NewText: edit.NewText,
		}
	}
	return result, nil
}
