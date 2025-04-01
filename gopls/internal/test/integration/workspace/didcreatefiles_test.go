// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package workspace

import (
	"context"
	"fmt"
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

// TestAutoFillPackageDecl tests that creation of a new .go file causes
// gopls to choose a sensible package name and fill in the package declaration.
func TestAutoFillPackageDecl(t *testing.T) {
	const existFiles = `
-- go.mod --
module mod.com

go 1.12

-- dog/a_test.go --
package dog
-- fruits/apple.go --
package apple

fun apple() int {
	return 0
}

-- license/license.go --
/* Copyright 2025 The Go Authors. All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file. */

package license

-- license1/license.go --
// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package license1

-- cmd/main.go --
package main

-- integration/a_test.go --
package integration_test

-- nopkg/testfile.go --
package
`
	for _, tc := range []struct {
		name    string
		newfile string
		want    string
	}{
		{
			name:    "new file in folder with a_test.go",
			newfile: "dog/newfile.go",
			want:    "package dog\n",
		},
		{
			name:    "new file in folder with go file",
			newfile: "fruits/newfile.go",
			want:    "package apple\n",
		},
		{
			name:    "new test file in folder with go file",
			newfile: "fruits/newfile_test.go",
			want:    "package apple\n",
		},
		{
			name:    "new file in folder with go file that contains license comment",
			newfile: "license/newfile.go",
			want: `/* Copyright 2025 The Go Authors. All rights reserved.
Use of this source code is governed by a BSD-style
license that can be found in the LICENSE file. */

package license
`,
		},
		{
			name:    "new file in folder with go file that contains license comment",
			newfile: "license1/newfile.go",
			want: `// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package license1
`,
		},
		{
			name:    "new file in folder with main package",
			newfile: "cmd/newfile.go",
			want:    "package main\n",
		},
		{
			name:    "new file in empty folder",
			newfile: "empty_folder/newfile.go",
			want:    "package emptyfolder\n",
		},
		{
			name:    "new file in folder with integration_test package",
			newfile: "integration/newfile.go",
			want:    "package integration\n",
		},
		{
			name:    "new test file in folder with integration_test package",
			newfile: "integration/newfile_test.go",
			want:    "package integration\n",
		},
		{
			name:    "new file in folder with incomplete package clause",
			newfile: "incomplete/newfile.go",
			want:    "package incomplete\n",
		},
		{
			name:    "package completion for dir name with punctuation",
			newfile: "123f_r.u~its-123/newfile.go",
			want:    "package fruits123\n",
		},
		{
			name:    "package completion for dir name with invalid dir name",
			newfile: "123f_r.u~its-123/newfile.go",
			want:    "package fruits123\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			createFiles := fmt.Sprintf("%s\n-- %s --", existFiles, tc.newfile)
			Run(t, createFiles, func(t *testing.T, env *Env) {
				env.DidCreateFiles(env.Editor.DocumentURI(tc.newfile))
				// save buffer to ensure the edits take effects in the file system.
				if err := env.Editor.SaveBuffer(context.Background(), tc.newfile); err != nil {
					t.Fatal(err)
				}
				if got := env.FileContent(tc.newfile); tc.want != got {
					t.Fatalf("want '%s' but got '%s'", tc.want, got)
				}
			})
		})
	}
}
