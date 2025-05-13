// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"fmt"
)

// The []byte fields below are marked omitzero, not omitempty:
// we want to marshal an empty byte slice.

// WireContent is the wire format for content.
// It represents the protocol types TextContent, ImageContent, AudioContent
// and EmbeddedResource.
// The Type field distinguishes them. In the protocol, each type has a constant
// value for the field.
// At most one of Text, Data, and Resource is non-zero.
type WireContent struct {
	Type        string        `json:"type"`
	Text        string        `json:"text,omitempty"`
	MIMEType    string        `json:"mimeType,omitempty"`
	Data        []byte        `json:"data,omitzero"`
	Resource    *WireResource `json:"resource,omitempty"`
	Annotations *Annotations  `json:"annotations,omitempty"`
}

// A WireResource is either a TextResourceContents or a BlobResourceContents.
// See https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2025-03-26/schema.ts#L524-L551
// for the inheritance structure.
// If Blob is nil, this is a TextResourceContents; otherwise it's a BlobResourceContents.
//
// The URI field describes the resource location.
type WireResource struct {
	URI      string `json:"uri,"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
	Blob     []byte `json:"blob,omitzero"`
}

func (c *WireContent) UnmarshalJSON(data []byte) error {
	type wireContent WireContent // for naive unmarshaling
	var c2 wireContent
	if err := json.Unmarshal(data, &c2); err != nil {
		return err
	}
	switch c2.Type {
	case "text", "image", "audio", "resource":
	default:
		return fmt.Errorf("unrecognized content type %s", c.Type)
	}
	*c = WireContent(c2)
	return nil
}

// Content is the union of supported content types: [TextContent],
// [ImageContent], [AudioContent], and [ResourceContent].
//
// ToWire converts content to its jsonrpc2 wire format.
type Content interface {
	// TODO: unexport this, and move the tests that use it to this package.
	ToWire() WireContent
}

// TextContent is a textual content.
type TextContent struct {
	Text string
}

func (c TextContent) ToWire() WireContent {
	return WireContent{Type: "text", Text: c.Text}
}

// ImageContent contains base64-encoded image data.
type ImageContent struct {
	Data     []byte // base64-encoded
	MIMEType string
}

func (c ImageContent) ToWire() WireContent {
	return WireContent{Type: "image", MIMEType: c.MIMEType, Data: c.Data}
}

// AudioContent contains base64-encoded audio data.
type AudioContent struct {
	Data     []byte
	MIMEType string
}

func (c AudioContent) ToWire() WireContent {
	return WireContent{Type: "audio", MIMEType: c.MIMEType, Data: c.Data}
}

// ResourceContent contains embedded resources.
type ResourceContent struct {
	Resource EmbeddedResource
}

func (r ResourceContent) ToWire() WireContent {
	res := r.Resource.toWire()
	return WireContent{Type: "resource", Resource: &res}
}

type EmbeddedResource interface {
	toWire() WireResource
}

// The {Text,Blob}ResourceContents types match the protocol definitions,
// but we represent both as a single type on the wire.

// A TextResourceContents is the contents of a text resource.
type TextResourceContents struct {
	URI      string
	MIMEType string
	Text     string
}

func (r TextResourceContents) toWire() WireResource {
	return WireResource{
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

func (r BlobResourceContents) toWire() WireResource {
	return WireResource{
		URI:      r.URI,
		MIMEType: r.MIMEType,
		Blob:     r.Blob,
	}
}

// ContentFromWireContent converts content from the jsonrpc2 wire format to a
// typed Content value.
func ContentFromWireContent(c WireContent) Content {
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
