// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/mcp"
)

func TestContent(t *testing.T) {
	tests := []struct {
		in   mcp.Content
		want mcp.WireContent
	}{
		{mcp.TextContent{Text: "hello"}, mcp.WireContent{Type: "text", Text: "hello"}},
		{
			mcp.ImageContent{Data: []byte("a1b2c3"), MIMEType: "image/png"},
			mcp.WireContent{Type: "image", Data: []byte("a1b2c3"), MIMEType: "image/png"},
		},
		{
			mcp.AudioContent{Data: []byte("a1b2c3"), MIMEType: "audio/wav"},
			mcp.WireContent{Type: "audio", Data: []byte("a1b2c3"), MIMEType: "audio/wav"},
		},
		{
			mcp.ResourceContent{
				Resource: mcp.TextResourceContents{
					URI:      "file://foo",
					MIMEType: "text",
					Text:     "abc",
				},
			},
			mcp.WireContent{
				Type: "resource",
				Resource: &mcp.WireResource{
					URI:      "file://foo",
					MIMEType: "text",
					Text:     "abc",
				},
			},
		},
		{
			mcp.ResourceContent{
				Resource: mcp.BlobResourceContents{
					URI:      "file://foo",
					MIMEType: "text",
					Blob:     []byte("a1b2c3"),
				},
			},
			mcp.WireContent{
				Type: "resource",
				Resource: &mcp.WireResource{
					URI:      "file://foo",
					MIMEType: "text",
					Blob:     []byte("a1b2c3"),
				},
			},
		},
	}

	for _, test := range tests {
		got := test.in.ToWire()
		if diff := cmp.Diff(test.want, got); diff != "" {
			t.Errorf("ToWire mismatch (-want +got):\n%s", diff)
		}
	}
}
