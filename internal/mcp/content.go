// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"fmt"

	"golang.org/x/tools/internal/mcp/internal/protocol"
)

// Content is the abstract result of a Tool call.
//
// TODO: support all content types.
type Content interface {
	toProtocol() any
}

func marshalContent(content []Content) []json.RawMessage {
	var msgs []json.RawMessage
	for _, c := range content {
		msg, err := json.Marshal(c.toProtocol())
		if err != nil {
			panic(fmt.Sprintf("marshaling content: %v", err))
		}
		msgs = append(msgs, msg)
	}
	return msgs
}

func unmarshalContent(msgs []json.RawMessage) ([]Content, error) {
	var content []Content
	for _, msg := range msgs {
		var allContent struct {
			Type string `json:"type"`
			Text json.RawMessage
		}
		if err := json.Unmarshal(msg, &allContent); err != nil {
			return nil, fmt.Errorf("content missing \"type\"")
		}
		switch allContent.Type {
		case "text":
			var text string
			if err := json.Unmarshal(allContent.Text, &text); err != nil {
				return nil, fmt.Errorf("unmarshalling text content: %v", err)
			}
			content = append(content, TextContent{Text: text})
		default:
			return nil, fmt.Errorf("unsupported content type %q", allContent.Type)
		}
	}
	return content, nil
}

// TextContent is a textual content.
type TextContent struct {
	Text string
}

func (c TextContent) toProtocol() any {
	return protocol.TextContent{Type: "text", Text: c.Text}
}
