// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonschema

import (
	"regexp"
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
		_, err := tt.s.Resolve()
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
