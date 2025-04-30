// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"encoding/json"
	"fmt"
)

// Content is the wire format for content, including all fields.
type Content struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	MIMEType string    `json:"mimeType,omitempty"`
	Data     string    `json:"data,omitempty"`
	Resource *Resource `json:"resource,omitempty"`
}

// Resource is the wire format for embedded resources, including all fields.
type Resource struct {
	URI      string  `json:"uri,"`
	MIMEType string  `json:"mimeType,omitempty"`
	Text     string  `json:"text"`
	Blob     *string `json:"blob"` // blob is a pointer to distinguish empty from missing data
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
