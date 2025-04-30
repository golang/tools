// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"fmt"

	"golang.org/x/tools/internal/mcp/internal/protocol"
)

// Content is the union of supported content types: [TextContent],
// [ImageContent], [AudioContent], and [ResourceContent].
//
// ToWire converts content to its jsonrpc2 wire format.
type Content interface {
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
	Data     string
	MimeType string
}

func (c ImageContent) ToWire() protocol.Content {
	return protocol.Content{Type: "image", MIMEType: c.MimeType, Data: c.Data}
}

// AudioContent contains base64-encoded audio data.
type AudioContent struct {
	Data     string
	MimeType string
}

func (c AudioContent) ToWire() protocol.Content {
	return protocol.Content{Type: "audio", MIMEType: c.MimeType, Data: c.Data}
}

// ResourceContent contains embedded resources.
type ResourceContent struct {
	Resource Resource
}

func (r ResourceContent) ToWire() protocol.Content {
	res := r.Resource.ToWire()
	return protocol.Content{Type: "resource", Resource: &res}
}

type Resource interface {
	ToWire() protocol.Resource
}

type TextResource struct {
	URI      string
	MimeType string
	Text     string
}

func (r TextResource) ToWire() protocol.Resource {
	return protocol.Resource{
		URI:      r.URI,
		MIMEType: r.MimeType,
		Text:     r.Text,
	}
}

type BlobResource struct {
	URI      string
	MimeType string
	Blob     string
}

func (r BlobResource) ToWire() protocol.Resource {
	blob := r.Blob
	return protocol.Resource{
		URI:      r.URI,
		MIMEType: r.MimeType,
		Blob:     &blob,
	}
}

// ContentFromWireContent converts content from the jsonrpc2 wire format to a
// typed Content value.
func ContentFromWireContent(c protocol.Content) Content {
	switch c.Type {
	case "text":
		return TextContent{Text: c.Text}
	case "image":
		return ImageContent{Data: c.Data, MimeType: c.MIMEType}
	case "audio":
		return AudioContent{Data: c.Data, MimeType: c.MIMEType}
	case "resource":
		r := ResourceContent{}
		if c.Resource != nil {
			if c.Resource.Blob != nil {
				r.Resource = BlobResource{
					URI:      c.Resource.URI,
					MimeType: c.Resource.MIMEType,
					Blob:     *c.Resource.Blob,
				}
			} else {
				r.Resource = TextResource{
					URI:      c.Resource.URI,
					MimeType: c.Resource.MIMEType,
					Text:     c.Resource.Text,
				}
			}
		}
		return r
	default:
		panic(fmt.Sprintf("unrecognized wire content type %q", c.Type))
	}
}
