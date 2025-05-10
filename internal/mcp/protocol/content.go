// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"encoding/json"
	"fmt"
)

// The []byte fields below are marked omitzero, not omitempty:
// we want to marshal an empty byte slice.

// Content is the wire format for content.
// It represents the protocol types TextContent, ImageContent, AudioContent
// and EmbeddedResource.
// The Type field distinguishes them. In the protocol, each type has a constant
// value for the field.
// At most one of Text, Data, and Resource is non-zero.
type Content struct {
	Type        string            `json:"type"`
	Text        string            `json:"text,omitempty"`
	MIMEType    string            `json:"mimeType,omitempty"`
	Data        []byte            `json:"data,omitzero"`
	Resource    *ResourceContents `json:"resource,omitempty"`
	Annotations *Annotations      `json:"annotations,omitempty"`
}

// A ResourceContents is either a TextResourceContents or a BlobResourceContents.
// See https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/schema/2025-03-26/schema.ts#L524-L551
// for the inheritance structure.
// If Blob is nil, this is a TextResourceContents; otherwise it's a BlobResourceContents.
//
// The URI field describes the resource location.
type ResourceContents struct {
	URI      string `json:"uri,"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
	Blob     []byte `json:"blob,omitzero"`
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
