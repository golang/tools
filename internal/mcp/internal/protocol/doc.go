// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run generate.go

// The protocol package contains types that define the MCP protocol.
//
// It is auto-generated from the MCP spec. Run go generate to update it.
// The generated set of types is intended to be minimal, in the sense that we
// only generate types that are actually used by the SDK. See generate.go for
// instructions on how to generate more (or different) types.
package protocol
