// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package present

import (
	"fmt"
	"strings"
)

func init() {
	Register("quote", parseQuote)
}

type Quote struct {
	Text     string
	Citation string
}

func (c Quote) TemplateName() string { return "quote" }

const citationToken = "//CITATION:"

// parseQuote parses a quote present directive. Its syntax:
//   .quote <text> [citation]
func parseQuote(_ *Context, sourceFile string, sourceLine int, cmd string) (Elem, error) {

	cmd = strings.TrimSpace(strings.TrimPrefix(cmd, ".quote"))

	tokens := strings.Split(cmd, citationToken)

	text := strings.TrimSpace(strings.TrimPrefix(tokens[0], ".quote"))
	citation := ""

	if text == "" {
		return nil, fmt.Errorf("%s:%d invalid quote syntax", sourceFile, sourceLine)
	}

	if len(tokens) == 1 {
		return Quote{text, citation}, nil
	}

	citation = strings.TrimSpace(tokens[1])
	if citation == "" || len(tokens) > 2 {
		return nil, fmt.Errorf("%s:%d invalid citation syntax", sourceFile, sourceLine)
	}

	return Quote{text, citation}, nil
}
