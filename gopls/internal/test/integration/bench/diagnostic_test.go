// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"sync"
	"testing"

	"golang.org/x/tools/gopls/internal/protocol"
	. "golang.org/x/tools/gopls/internal/test/integration"
	"golang.org/x/tools/gopls/internal/test/integration/fake"
)

// BenchmarkDiagnosePackageFiles measures how long it takes to request
// diagnostics for 10 files in a single package, following a change to that
// package.
//
// This can be used to measure the efficiency of pull diagnostics
// (golang/go#53275).
func BenchmarkDiagnosePackageFiles(b *testing.B) {
	if testing.Short() {
		b.Skip("pull diagnostics are not supported by the benchmark dashboard baseline")
	}

	env := getRepo(b, "kubernetes").newEnv(b, fake.EditorConfig{
		Settings: map[string]any{
			"pullDiagnostics": true, // currently required for pull diagnostic support
		},
	}, "diagnosePackageFiles", false)

	// 10 arbitrary files in a single package.
	files := []string{
		"pkg/kubelet/active_deadline.go",      // 98 lines
		"pkg/kubelet/active_deadline_test.go", // 95 lines
		"pkg/kubelet/kubelet.go",              // 2439 lines
		"pkg/kubelet/kubelet_pods.go",         // 2061 lines
		"pkg/kubelet/kubelet_network.go",      // 70 lines
		"pkg/kubelet/kubelet_network_test.go", // 46 lines
		"pkg/kubelet/pod_workers.go",          // 1323 lines
		"pkg/kubelet/pod_workers_test.go",     // 1758 lines
		"pkg/kubelet/runonce.go",              // 175 lines
		"pkg/kubelet/volume_host.go",          // 297 lines
	}

	env.Await(InitialWorkspaceLoad)

	for _, file := range files {
		env.OpenFile(file)
	}

	env.AfterChange()

	edit := makeEditFunc(env, files[0])

	if stopAndRecord := startProfileIfSupported(b, env, qualifiedName("kubernetes", "diagnosePackageFiles")); stopAndRecord != nil {
		defer stopAndRecord()
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		edit()
		var wg sync.WaitGroup
		for _, file := range files {
			wg.Add(1)
			go func() {
				defer wg.Done()
				fileDiags := env.Diagnostics(file)
				for _, d := range fileDiags {
					if d.Severity == protocol.SeverityError {
						b.Errorf("unexpected error diagnostic: %s", d.Message)
					}
				}
			}()
		}
		wg.Wait()
	}
}
