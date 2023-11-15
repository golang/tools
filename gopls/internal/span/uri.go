// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package span

import "golang.org/x/tools/gopls/internal/lsp/protocol"

// TODO(adonovan): inline this package away.
// It exists for now only to avoid a big renaming.

type URI = protocol.DocumentURI

var URIFromPath = protocol.URIFromPath
