// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated with somegen DO NOT EDIT.

package testdata

import "log"

func mgeneratedcode() {
	maps := make(map[string]string)
	for k, _ := range maps { // No simplification fix is offered in generated code.
		log.Println(k)
	}
	for _ = range maps { // No simplification fix is offered in generated code.
	}
}
