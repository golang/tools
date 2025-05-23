// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"errors"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
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
		_, err := tt.s.Resolve("", nil)
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

func TestSchemaNonTree(t *testing.T) {
	run := func(s *Schema, kind string) {
		err := s.check()
		if err == nil || !strings.Contains(err.Error(), "tree") {
			t.Fatalf("did not detect %s", kind)
		}
	}

	s := &Schema{Type: "number"}
	run(&Schema{Items: s, Contains: s}, "DAG")

	root := &Schema{Items: s}
	s.Items = root
	run(root, "cycle")
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
					ID:            "/bar.json",
					Anchor:        "a",
					DynamicAnchor: "da",
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

			wantIDs := map[string]*Schema{
				baseURI:                       root,
				"http://b.com/foo.json":       root.Items,
				"http://b.com/bar.json":       root.Contains,
				"http://b.com/bar.json?items": root.Contains.Items.Items,
			}
			if baseURI != root.ID {
				wantIDs[root.ID] = root
			}
			wantAnchors := map[*Schema]map[string]anchorInfo{
				root.Contains: {
					"a":  anchorInfo{root.Contains, false},
					"da": anchorInfo{root.Contains, true},
					"b":  anchorInfo{root.Contains.Items, false},
				},
				root.Contains.Items.Items: {
					"c": anchorInfo{root.Contains.Items.Items, false},
				},
			}

			gotKeys := slices.Sorted(maps.Keys(got))
			wantKeys := slices.Sorted(maps.Keys(wantIDs))
			if !slices.Equal(gotKeys, wantKeys) {
				t.Errorf("ID keys:\ngot  %q\nwant %q", gotKeys, wantKeys)
			}
			if !maps.Equal(got, wantIDs) {
				t.Errorf("IDs:\ngot  %+v\n\nwant %+v", got, wantIDs)
			}
			for s := range root.all() {
				if want := wantAnchors[s]; want != nil {
					if got := s.anchors; !maps.Equal(got, want) {
						t.Errorf("anchors:\ngot  %+v\n\nwant %+v", got, want)
					}
				} else if s.anchors != nil {
					t.Errorf("non-nil anchors for %s", s)
				}
			}
		})
	}
}

func TestRefCycle(t *testing.T) {
	// Verify that cycles of refs are OK.
	// The test suite doesn't check this, surprisingly.
	schemas := map[string]*Schema{
		"root": {Ref: "a"},
		"a":    {Ref: "b"},
		"b":    {Ref: "a"},
	}

	loader := func(uri *url.URL) (*Schema, error) {
		s, ok := schemas[uri.Path[1:]]
		if !ok {
			return nil, errors.New("not found")
		}
		return s, nil
	}

	rs, err := schemas["root"].Resolve("", loader)
	if err != nil {
		t.Fatal(err)
	}

	check := func(s *Schema, key string) {
		t.Helper()
		if s.resolvedRef != schemas[key] {
			t.Errorf("%s resolvedRef != schemas[%q]", s.json(), key)
		}
	}

	check(rs.root, "a")
	check(schemas["a"], "b")
	check(schemas["b"], "a")
}
