// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"fmt"

	"golang.org/x/tools/internal/mcp/protocol"
)

// Content is the union of supported content types: [TextContent],
// [ImageContent], [AudioContent], and [ResourceContent].
//
// ToWire converts content to its jsonrpc2 wire format.
type Content interface {
	// TODO: unexport this, and move the tests that use it to this package.
	ToWire() protocol.Content
}

// TextContent is a textual content.
type TextContent struct {
	Text string
}

func (c TextContent) ToWire() protocol.Content {
	return protocol.Content{Type: "text", Text: c.Text}
}

// ImageContent contains base64-encoded image data.
type ImageContent struct {
	Data     []byte // base64-encoded
	MIMEType string
}

func (c ImageContent) ToWire() protocol.Content {
	return protocol.Content{Type: "image", MIMEType: c.MIMEType, Data: c.Data}
}

// AudioContent contains base64-encoded audio data.
type AudioContent struct {
	Data     []byte
	MIMEType string
}

func (c AudioContent) ToWire() protocol.Content {
	return protocol.Content{Type: "audio", MIMEType: c.MIMEType, Data: c.Data}
}

// ResourceContent contains embedded resources.
type ResourceContent struct {
	Resource EmbeddedResource
}

func (r ResourceContent) ToWire() protocol.Content {
	res := r.Resource.toWire()
	return protocol.Content{Type: "resource", Resource: &res}
}

type EmbeddedResource interface {
	toWire() protocol.ResourceContents
}

// The {Text,Blob}ResourceContents types match the protocol definitions,
// but we represent both as a single type on the wire.

// A TextResourceContents is the contents of a text resource.
type TextResourceContents struct {
	URI      string
	MIMEType string
	Text     string
}

func (r TextResourceContents) toWire() protocol.ResourceContents {
	return protocol.ResourceContents{
		URI:      r.URI,
		MIMEType: r.MIMEType,
		Text:     r.Text,
		// Blob is nil, indicating this is a TextResourceContents.
	}
}

// A BlobResourceContents is the contents of a blob resource.
type BlobResourceContents struct {
	URI      string
	MIMEType string
	Blob     []byte
}

func (r BlobResourceContents) toWire() protocol.ResourceContents {
	return protocol.ResourceContents{
		URI:      r.URI,
		MIMEType: r.MIMEType,
		Blob:     r.Blob,
	}
}

// ContentFromWireContent converts content from the jsonrpc2 wire format to a
// typed Content value.
func ContentFromWireContent(c protocol.Content) Content {
	switch c.Type {
	case "text":
		return TextContent{Text: c.Text}
	case "image":
		return ImageContent{Data: c.Data, MIMEType: c.MIMEType}
	case "audio":
		return AudioContent{Data: c.Data, MIMEType: c.MIMEType}
	case "resource":
		r := ResourceContent{}
		if c.Resource != nil {
			if c.Resource.Blob != nil {
				r.Resource = BlobResourceContents{
					URI:      c.Resource.URI,
					MIMEType: c.Resource.MIMEType,
					Blob:     c.Resource.Blob,
				}
			} else {
				r.Resource = TextResourceContents{
					URI:      c.Resource.URI,
					MIMEType: c.Resource.MIMEType,
					Text:     c.Resource.Text,
				}
			}
		}
		return r
	default:
		panic(fmt.Sprintf("unrecognized wire content type %q", c.Type))
	}
}
