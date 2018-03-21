// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package present

import (
	"strings"
	"testing"
)

func TestQuoteParsing(t *testing.T) {
	var tests = []struct {
		name string
		cmd  string
		err  string
		Quote
	}{
		{
			name: "quote with invalid citation syntax",
			cmd:  ".quote a quote //CITATION: ",
			err:  "invalid citation syntax",
		},
		{
			name: "quote without text and valid citation",
			cmd:  ".quote //CITATION: citation",
			err:  "invalid quote syntax",
		},
		{
			name: "quote with text",
			cmd:  ".quote some text",
			Quote: Quote{
				Text:     "some text",
				Citation: "",
			},
		},
		{
			name: "quote with text and citation",
			cmd:  ".quote some text //CITATION: other text",
			Quote: Quote{
				Text:     "some text",
				Citation: "other text",
			},
		},
	}

	for _, test := range tests {
		element, err := parseQuote(nil, "test.slide", 0, test.cmd)

		if err != nil {
			if test.err == "" {
				t.Errorf("%s: unexpected error %v", test.name, err)
			} else if !strings.Contains(err.Error(), test.err) {
				t.Errorf("%s: expected error %s; got %v", test.name, test.err, err)
			}
			continue
		}

		if test.err != "" {
			t.Errorf("%s: expected error %s; but got none", test.name, test.err)
			continue
		}

		quote, ok := element.(Quote)
		if !ok {
			t.Errorf("%s: expected a Code value; got %T", test.name, quote)
			continue
		}

		if quote.Text != test.Text {
			t.Errorf("%s: expected Text %s; got %s", test.name, test.Text, quote.Text)
		}

		if quote.Citation != test.Citation {
			t.Errorf("%s: expected Citation %s; got %s", test.name, test.Citation, quote.Citation)
		}
	}
}
