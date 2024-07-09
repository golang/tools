// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore
// +build ignore

// The generate command generates command_gen.go from a combination of
// static and dynamic analysis of the command package.
package main

import (
	"log"
	"os"

	"golang.org/x/tools/gopls/internal/protocol/command/gen"
)

func main() {
	content, err := gen.Generate()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("command_gen.go", content, 0644); err != nil {
		log.Fatal(err)
	}
}
