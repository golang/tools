// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Content is the wire format for content.
// It represents the protocol types TextContent, ImageContent, AudioContent
// and EmbeddedResource.
// Use [NewTextContent], [NewImageContent], [NewAudioContent] or [NewResourceContent]
// to create one.
//
// The Type field must be one of "text", "image", "audio" or "resource". The
// constructors above populate this field appropriately.
// Although at most one of Text, Data, and Resource should be non-zero, consumers of Content
// use the Type field to determine which value to use; values in the other fields are ignored.
type Content struct {
	Type        string            `json:"type"`
	Text        string            `json:"text,omitempty"`
	MIMEType    string            `json:"mimeType,omitempty"`
	Data        []byte            `json:"data,omitempty"`
	Resource    *ResourceContents `json:"resource,omitempty"`
	Annotations *Annotations      `json:"annotations,omitempty"`
}

func (c *Content) UnmarshalJSON(data []byte) error {
	type wireContent Content // for naive unmarshaling
	var c2 wireContent
	if err := json.Unmarshal(data, &c2); err != nil {
		return err
	}
	switch c2.Type {
	case "text", "image", "audio", "resource":
	default:
		return fmt.Errorf("unrecognized content type %s", c.Type)
	}
	*c = Content(c2)
	return nil
}

// NewTextContent creates a [Content] with text.
func NewTextContent(text string) *Content {
	return &Content{Type: "text", Text: text}
}

// NewImageContent creates a [Content] with image data.
func NewImageContent(data []byte, mimeType string) *Content {
	return &Content{Type: "image", Data: data, MIMEType: mimeType}
}

// NewAudioContent creates a [Content] with audio data.
func NewAudioContent(data []byte, mimeType string) *Content {
	return &Content{Type: "audio", Data: data, MIMEType: mimeType}
}

// NewResourceContent creates a [Content] with an embedded resource.
func NewResourceContent(resource *ResourceContents) *Content {
	return &Content{Type: "resource", Resource: resource}
}

// ResourceContents represents the union of the spec's {Text,Blob}ResourceContents types.
// See https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2025-03-26/schema.ts#L524-L551
// for the inheritance structure.

// A ResourceContents is either a TextResourceContents or a BlobResourceContents.
// Use [NewTextResourceContents] or [NextBlobResourceContents] to create one.
type ResourceContents struct {
	URI      string `json:"uri"` // resource location; must not be empty
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
	Blob     []byte `json:"blob,omitempty"` // if nil, then text; else blob
}

func (r ResourceContents) MarshalJSON() ([]byte, error) {
	// If we could assume Go 1.24, we could use omitzero for Blob and avoid this method.
	if r.URI == "" {
		return nil, errors.New("ResourceContents missing URI")
	}
	if r.Blob == nil {
		// Text. Marshal normally.
		type wireResourceContents ResourceContents // (lacks MarshalJSON method)
		return json.Marshal((wireResourceContents)(r))
	}
	// Blob.
	if r.Text != "" {
		return nil, errors.New("ResourceContents has non-zero Text and Blob fields")
	}
	// r.Blob may be the empty slice, so marshal with an alternative definition.
	br := struct {
		URI      string `json:"uri,omitempty"`
		MIMEType string `json:"mimeType,omitempty"`
		Blob     []byte `json:"blob"`
	}{
		URI:      r.URI,
		MIMEType: r.MIMEType,
		Blob:     r.Blob,
	}
	return json.Marshal(br)
}

// NewTextResourceContents returns a [ResourceContents] containing text.
func NewTextResourceContents(uri, mimeType, text string) *ResourceContents {
	return &ResourceContents{
		URI:      uri,
		MIMEType: mimeType,
		Text:     text,
		// Blob is nil, indicating this is a TextResourceContents.
	}
}

// NewBlobResourceContents returns a [ResourceContents] containing a byte slice.
func NewBlobResourceContents(uri, mimeType string, blob []byte) *ResourceContents {
	// The only way to distinguish text from blob is a non-nil Blob field.
	if blob == nil {
		blob = []byte{}
	}
	return &ResourceContents{
		URI:      uri,
		MIMEType: mimeType,
		Blob:     blob,
	}
}
