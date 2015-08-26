// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package present

import (
	"fmt"
	"strings"
)

func init() {
	Register("latex", parseLatex)
}

func parseLatex(ctx *Context, fileName string, lineno int, text string) (Elem, error) {
	args := strings.Fields(text)
	if len(args) >= 2 {
		args[1] = "https://latex.codecogs.com/svg.latex?" + args[1]
	} else {
		return nil, fmt.Errorf("incorrect latex invocation: %v", text)
	}
	text = strings.Join(args, " ")
	return parseImage(ctx, fileName, lineno, text)
}