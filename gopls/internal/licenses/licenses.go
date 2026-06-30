// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate ./gen-licenses.sh licenses.txt
package licenses

import _ "embed"

//go:embed licenses.txt
var Text string
