// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The mcp package provides an SDK for writing model context protocol clients
// and servers. It is a work-in-progress. As of writing, it is a prototype to
// explore the design space of client/server lifecycle and binding.
//
// To get started, create an MCP client or server with [NewClient] or
// [NewServer], then add features to your client or server using Add<feature>
// methods, then connect to a peer using a [Transport] instance and a call to
// [Client.Connect] or [Server.Connect].
//
// TODO:
//   - Support pagination.
//   - Support all client/server operations.
//   - Support Streamable HTTP transport.
//   - Support multiple versions of the spec.
//   - Implement proper JSON schema support, with both client-side and
//     server-side validation..
package mcp
