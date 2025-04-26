// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"maps"
	"net/url"
	"regexp"
	"slices"
	"testing"
)

func TestCheckLocal(t *testing.T) {
	for _, tt := range []struct {
		s    *Schema
		want string // error must be non-nil and match this regexp
	}{
		{nil, "nil"},
		{
			&Schema{Pattern: "]["},
			"regexp",
		},
		{
			&Schema{PatternProperties: map[string]*Schema{"*": nil}},
			"regexp",
		},
	} {
		_, err := tt.s.Resolve("")
		if err == nil {
			t.Errorf("%s: unexpectedly passed", tt.s.json())
			continue
		}
		if !regexp.MustCompile(tt.want).MatchString(err.Error()) {
			t.Errorf("%s: did not match\nerror: %s\nregexp: %s",
				tt.s.json(), err, tt.want)
		}
	}
}

func TestResolveURIs(t *testing.T) {
	for _, baseURI := range []string{"", "http://a.com"} {
		t.Run(baseURI, func(t *testing.T) {
			root := &Schema{
				ID: "http://b.com",
				Items: &Schema{
					ID: "/foo.json",
				},
				Contains: &Schema{
					ID:     "/bar.json",
					Anchor: "a",
					Items: &Schema{
						Anchor: "b",
						Items: &Schema{
							// An ID shouldn't be a query param, but this tests
							// resolving an ID with its parent.
							ID:     "?items",
							Anchor: "c",
						},
					},
				},
			}
			base, err := url.Parse(baseURI)
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolveURIs(root, base)
			if err != nil {
				t.Fatal(err)
			}

			want := map[string]*Schema{
				baseURI:                       root,
				"http://b.com/foo.json":       root.Items,
				"http://b.com/bar.json":       root.Contains,
				"http://b.com/bar.json?items": root.Contains.Items.Items,
			}
			if baseURI != root.ID {
				want[root.ID] = root
			}

			gotKeys := slices.Sorted(maps.Keys(got))
			wantKeys := slices.Sorted(maps.Keys(want))
			if !slices.Equal(gotKeys, wantKeys) {
				t.Errorf("ID keys:\ngot  %q\nwant %q", gotKeys, wantKeys)
			}
			if !maps.Equal(got, want) {
				t.Errorf("IDs:\ngot  %+v\n\nwant %+v", got, want)
			}
		})
	}
}
