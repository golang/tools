// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package bench

import (
	"fmt"
	"path"
	"regexp"
	"testing"

	. "golang.org/x/tools/gopls/internal/test/integration"
)

// BenchmarkReload benchmarks reloading a file metadata after a change to an import.
//
// This ensures we are able to diagnose a changed file without reloading all
// invalidated packages. See also golang/go#61344
func BenchmarkReload(b *testing.B) {
	type replace map[string]string
	tests := []struct {
		repo string
		file string
		// replacements must be 'reversible', in the sense that the replacing
		// string is unique.
		replace replace
	}{
		// pkg/util/hash is transitively imported by a large number of packages. We
		// should not need to reload those packages to get a diagnostic.
		{"kubernetes", "pkg/util/hash/hash.go", replace{`"hash"`: `"hashx"`}},
		{"kubernetes", "pkg/kubelet/kubelet.go", replace{
			`"k8s.io/kubernetes/pkg/kubelet/config"`: `"k8s.io/kubernetes/pkg/kubelet/configx"`,
		}},
	}

	for _, test := range tests {
		b.Run(fmt.Sprintf("%s/%s", test.repo, path.Base(test.file)), func(b *testing.B) {
			env := getRepo(b, test.repo).sharedEnv(b)

			env.OpenFile(test.file)
			defer closeBuffer(b, env, test.file)

			env.AfterChange()

			profileName := qualifiedName("reload", test.repo, path.Base(test.file))
			if stopAndRecord := startProfileIfSupported(b, env, profileName); stopAndRecord != nil {
				defer stopAndRecord()
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Mutate the file. This may result in cache hits, but that's OK: the
				// goal is to ensure that we don't reload more than just the current
				// package.
				for k, v := range test.replace {
					env.RegexpReplace(test.file, regexp.QuoteMeta(k), v)
				}
				// Note: don't use env.AfterChange() here: we only want to await the
				// first diagnostic.
				//
				// Awaiting a full diagnosis would await diagnosing everything, which
				// would require reloading everything.
				env.Await(Diagnostics(ForFile(test.file)))
				for k, v := range test.replace {
					env.RegexpReplace(test.file, regexp.QuoteMeta(v), k)
				}
				env.Await(NoDiagnostics(ForFile(test.file)))
			}
		})
	}
}
