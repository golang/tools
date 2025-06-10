// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import "golang.org/x/telemetry/counter"

// Proposed counters for evaluating gopls extract and inline. These counters
// increment when the user attempts to perform one of these operations,
// regardless of whether it succeeds.
var (
	countExtractFunction    = counter.New("gopls/extract:func")
	countExtractMethod      = counter.New("gopls/extract:method")
	countExtractVariable    = counter.New("gopls/extract:variable")
	countExtractVariableAll = counter.New("gopls/extract:variable-all")

	countInlineCall     = counter.New("gopls/inline:call")
	countInlineVariable = counter.New("gopls/inline:variable")
)
