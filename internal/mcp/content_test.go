// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/internal/mcp"
)

func TestContent(t *testing.T) {
	tests := []struct {
		in   *mcp.Content
		want string // json serialization
	}{
		{mcp.NewTextContent("hello"), `{"type":"text","text":"hello"}`},
		{
			mcp.NewImageContent([]byte("a1b2c3"), "image/png"),
			`{"type":"image","mimeType":"image/png","data":"YTFiMmMz"}`,
		},
		{
			mcp.NewAudioContent([]byte("a1b2c3"), "audio/wav"),
			`{"type":"audio","mimeType":"audio/wav","data":"YTFiMmMz"}`,
		},
		{
			mcp.NewResourceContent(
				mcp.NewTextResourceContents("file://foo", "text", "abc"),
			),
			`{"type":"resource","resource":{"uri":"file://foo","mimeType":"text","text":"abc"}}`,
		},
		{
			mcp.NewResourceContent(
				mcp.NewBlobResourceContents("file://foo", "image/png", []byte("a1b2c3")),
			),
			`{"type":"resource","resource":{"uri":"file://foo","mimeType":"image/png","blob":"YTFiMmMz"}}`,
		},
	}

	for _, test := range tests {
		got, err := json.Marshal(test.in)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(test.want, string(got)); diff != "" {
			t.Errorf("json.Marshal(%v) mismatch (-want +got):\n%s", test.in, diff)
		}
		var out *mcp.Content
		if err := json.Unmarshal(got, &out); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(test.in, out); diff != "" {
			t.Errorf("json.Unmarshal(%q) mismatch (-want +got):\n%s", string(got), diff)
		}
	}
}

func TestResourceContents(t *testing.T) {
	for _, tt := range []struct {
		rc   mcp.ResourceContents
		want string // marshaled JSON
	}{
		{
			mcp.ResourceContents{URI: "u", Text: "t"},
			`{"uri":"u","text":"t"}`,
		},
		{
			mcp.ResourceContents{URI: "u", MIMEType: "m", Text: "t"},
			`{"uri":"u","mimeType":"m","text":"t"}`,
		},
		{
			mcp.ResourceContents{URI: "u", Text: "", Blob: nil},
			`{"uri":"u","text":""}`,
		},
		{
			mcp.ResourceContents{URI: "u", Blob: []byte{}},
			`{"uri":"u","blob":""}`,
		},
		{
			mcp.ResourceContents{URI: "u", Blob: []byte{1}},
			`{"uri":"u","blob":"AQ=="}`,
		},
	} {
		data, err := json.Marshal(tt.rc)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(data); got != tt.want {
			t.Errorf("%#v:\ngot  %s\nwant %s", tt.rc, got, tt.want)
		}
		var urc mcp.ResourceContents
		if err := json.Unmarshal(data, &urc); err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(tt.rc, urc); diff != "" {
			t.Errorf("mismatch (-want, +got):\n%s", diff)
		}
	}
}
