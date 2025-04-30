// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/mcp"
	"golang.org/x/tools/internal/mcp/internal/protocol"
)

func TestContent(t *testing.T) {
	tests := []struct {
		in   mcp.Content
		want protocol.Content
	}{
		{mcp.TextContent{Text: "hello"}, protocol.Content{Type: "text", Text: "hello"}},
		{
			mcp.ImageContent{Data: "a1b2c3", MimeType: "image/png"},
			protocol.Content{Type: "image", Data: "a1b2c3", MIMEType: "image/png"},
		},
		{
			mcp.AudioContent{Data: "a1b2c3", MimeType: "audio/wav"},
			protocol.Content{Type: "audio", Data: "a1b2c3", MIMEType: "audio/wav"},
		},
		{
			mcp.ResourceContent{
				Resource: mcp.TextResource{
					URI:      "file://foo",
					MimeType: "text",
					Text:     "abc",
				},
			},
			protocol.Content{
				Type: "resource",
				Resource: &protocol.Resource{
					URI:      "file://foo",
					MIMEType: "text",
					Text:     "abc",
				},
			},
		},
		{
			mcp.ResourceContent{
				Resource: mcp.BlobResource{
					URI:      "file://foo",
					MimeType: "text",
					Blob:     "a1b2c3",
				},
			},
			protocol.Content{
				Type: "resource",
				Resource: &protocol.Resource{
					URI:      "file://foo",
					MIMEType: "text",
					Blob:     ptr("a1b2c3"),
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

func ptr[T any](t T) *T {
	return &t
}
