// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/gopls/internal/vulncheck/govulncheck"
	"golang.org/x/tools/gopls/internal/vulncheck/osv"
	isem "golang.org/x/tools/gopls/internal/vulncheck/semver"
	"golang.org/x/tools/internal/memoize"
	"golang.org/x/vuln/scan"
)

// ModVuln returns import vulnerability analysis for the given go.mod URI.
// Concurrent requests are combined into a single command.
func (s *Snapshot) ModVuln(ctx context.Context, modURI protocol.DocumentURI) (*vulncheck.Result, error) {
	s.mu.Lock()
	entry, hit := s.modVulnHandles.Get(modURI)
	s.mu.Unlock()

	type modVuln struct {
		result *vulncheck.Result
		err    error
	}

	// Cache miss?
	if !hit {
		handle := memoize.NewPromise("modVuln", func(ctx context.Context, arg interface{}) interface{} {
			result, err := modVulnImpl(ctx, arg.(*Snapshot))
			return modVuln{result, err}
		})

		entry = handle
		s.mu.Lock()
		s.modVulnHandles.Set(modURI, entry, nil)
		s.mu.Unlock()
	}

	// Await result.
	v, err := s.awaitPromise(ctx, entry)
	if err != nil {
		return nil, err
	}
	res := v.(modVuln)
	return res.result, res.err
}

// GoVersionForVulnTest is an internal environment variable used in gopls
// testing to examine govulncheck behavior with a go version different
// than what `go version` returns in the system.
const GoVersionForVulnTest = "_GOPLS_TEST_VULNCHECK_GOVERSION"

// modVulnImpl queries the vulndb and reports which vulnerabilities
// apply to this snapshot. The result contains a set of packages,
// grouped by vuln ID and by module. This implements the "import-based"
// vulnerability report on go.mod files.
func modVulnImpl(ctx context.Context, snapshot *Snapshot) (*vulncheck.Result, error) {
	// TODO(hyangah): can we let 'govulncheck' take a package list
	// used in the workspace and implement this function?

	// We want to report the intersection of vulnerable packages in the vulndb
	// and packages transitively imported by this module ('go list -deps all').
	// We use snapshot.AllMetadata to retrieve the list of packages
	// as an approximation.
	//
	// TODO(hyangah): snapshot.AllMetadata is a superset of
	// `go list all` - e.g. when the workspace has multiple main modules
	// (multiple go.mod files), that can include packages that are not
	// used by this module. Vulncheck behavior with go.work is not well
	// defined. Figure out the meaning, and if we decide to present
	// the result as if each module is analyzed independently, make
	// gopls track a separate build list for each module and use that
	// information instead of snapshot.AllMetadata.
	allMeta, err := snapshot.AllMetadata(ctx)
	if err != nil {
		return nil, err
	}

	// TODO(hyangah): handle vulnerabilities in the standard library.

	// Group packages by modules since vuln db is keyed by module.
	packagesByModule := map[metadata.PackagePath][]*metadata.Package{}
	for _, mp := range allMeta {
		modulePath := metadata.PackagePath(osv.GoStdModulePath)
		if mi := mp.Module; mi != nil {
			modulePath = metadata.PackagePath(mi.Path)
		}
		packagesByModule[modulePath] = append(packagesByModule[modulePath], mp)
	}

	var (
		mu sync.Mutex
		// Keys are osv.Entry.ID
		osvs     = map[string]*osv.Entry{}
		findings []*govulncheck.Finding
	)

	goVersion := snapshot.Options().Env[GoVersionForVulnTest]
	if goVersion == "" {
		goVersion = snapshot.GoVersionString()
	}

	stdlibModule := &packages.Module{
		Path:    osv.GoStdModulePath,
		Version: goVersion,
	}

	// GOVULNDB may point the test db URI.
	db := GetEnv(snapshot, "GOVULNDB")

	var group errgroup.Group
	group.SetLimit(10) // limit govulncheck api runs
	for _, mps := range packagesByModule {
		mps := mps
		group.Go(func() error {
			effectiveModule := stdlibModule
			if m := mps[0].Module; m != nil {
				effectiveModule = m
			}
			for effectiveModule.Replace != nil {
				effectiveModule = effectiveModule.Replace
			}
			ver := effectiveModule.Version
			if ver == "" || !isem.Valid(ver) {
				// skip invalid version strings. the underlying scan api is strict.
				return nil
			}

			// TODO(hyangah): batch these requests and add in-memory cache for efficiency.
			vulns, err := osvsByModule(ctx, db, effectiveModule.Path+"@"+ver)
			if err != nil {
				return err
			}
			if len(vulns) == 0 { // No known vulnerability.
				return nil
			}

			// set of packages in this module known to gopls.
			// This will be lazily initialized when we need it.
			var knownPkgs map[metadata.PackagePath]bool

			// Report vulnerabilities that affect packages of this module.
			for _, entry := range vulns {
				var vulnerablePkgs []*govulncheck.Finding
				fixed := fixedVersion(effectiveModule.Path, entry.Affected)

				for _, a := range entry.Affected {
					if a.Module.Ecosystem != osv.GoEcosystem || a.Module.Path != effectiveModule.Path {
						continue
					}
					for _, imp := range a.EcosystemSpecific.Packages {
						if knownPkgs == nil {
							knownPkgs = toPackagePathSet(mps)
						}
						if knownPkgs[metadata.PackagePath(imp.Path)] {
							vulnerablePkgs = append(vulnerablePkgs, &govulncheck.Finding{
								OSV:          entry.ID,
								FixedVersion: fixed,
								Trace: []*govulncheck.Frame{
									{
										Module:  effectiveModule.Path,
										Version: effectiveModule.Version,
										Package: imp.Path,
									},
								},
							})
						}
					}
				}
				if len(vulnerablePkgs) == 0 {
					continue
				}
				mu.Lock()
				osvs[entry.ID] = entry
				findings = append(findings, vulnerablePkgs...)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	// Sort so the results are deterministic.
	sort.Slice(findings, func(i, j int) bool {
		x, y := findings[i], findings[j]
		if x.OSV != y.OSV {
			return x.OSV < y.OSV
		}
		return x.Trace[0].Package < y.Trace[0].Package
	})
	ret := &vulncheck.Result{
		Entries:  osvs,
		Findings: findings,
		Mode:     vulncheck.ModeImports,
	}
	return ret, nil
}

// TODO(rfindley): this function was exposed during refactoring. Reconsider it.
func GetEnv(snapshot *Snapshot, key string) string {
	val, ok := snapshot.Options().Env[key]
	if ok {
		return val
	}
	return os.Getenv(key)
}

// toPackagePathSet transforms the metadata to a set of package paths.
func toPackagePathSet(mds []*metadata.Package) map[metadata.PackagePath]bool {
	pkgPaths := make(map[metadata.PackagePath]bool, len(mds))
	for _, md := range mds {
		pkgPaths[md.PkgPath] = true
	}
	return pkgPaths
}

func fixedVersion(modulePath string, affected []osv.Affected) string {
	fixed := latestFixed(modulePath, affected)
	if fixed != "" {
		fixed = versionString(modulePath, fixed)
	}
	return fixed
}

// latestFixed returns the latest fixed version in the list of affected ranges,
// or the empty string if there are no fixed versions.
func latestFixed(modulePath string, as []osv.Affected) string {
	v := ""
	for _, a := range as {
		if a.Module.Path != modulePath {
			continue
		}
		for _, r := range a.Ranges {
			if r.Type == osv.RangeTypeSemver {
				for _, e := range r.Events {
					if e.Fixed != "" && (v == "" ||
						semver.Compare(isem.CanonicalizeSemverPrefix(e.Fixed), isem.CanonicalizeSemverPrefix(v)) > 0) {
						v = e.Fixed
					}
				}
			}
		}
	}
	return v
}

// versionString prepends a version string prefix (`v` or `go`
// depending on the modulePath) to the given semver-style version string.
func versionString(modulePath, version string) string {
	if version == "" {
		return ""
	}
	v := "v" + version
	// These are internal Go module paths used by the vuln DB
	// when listing vulns in standard library and the go command.
	if modulePath == "stdlib" || modulePath == "toolchain" {
		return semverToGoTag(v)
	}
	return v
}

// semverToGoTag returns the Go standard library repository tag corresponding
// to semver, a version string without the initial "v".
// Go tags differ from standard semantic versions in a few ways,
// such as beginning with "go" instead of "v".
func semverToGoTag(v string) string {
	if strings.HasPrefix(v, "v0.0.0") {
		return "master"
	}
	// Special case: v1.0.0 => go1.
	if v == "v1.0.0" {
		return "go1"
	}
	if !semver.IsValid(v) {
		return fmt.Sprintf("<!%s:invalid semver>", v)
	}
	goVersion := semver.Canonical(v)
	prerelease := semver.Prerelease(goVersion)
	versionWithoutPrerelease := strings.TrimSuffix(goVersion, prerelease)
	patch := strings.TrimPrefix(versionWithoutPrerelease, semver.MajorMinor(goVersion)+".")
	if patch == "0" {
		versionWithoutPrerelease = strings.TrimSuffix(versionWithoutPrerelease, ".0")
	}
	goVersion = fmt.Sprintf("go%s", strings.TrimPrefix(versionWithoutPrerelease, "v"))
	if prerelease != "" {
		// Go prereleases look like  "beta1" instead of "beta.1".
		// "beta1" is bad for sorting (since beta10 comes before beta9), so
		// require the dot form.
		i := finalDigitsIndex(prerelease)
		if i >= 1 {
			if prerelease[i-1] != '.' {
				return fmt.Sprintf("<!%s:final digits in a prerelease must follow a period>", v)
			}
			// Remove the dot.
			prerelease = prerelease[:i-1] + prerelease[i:]
		}
		goVersion += strings.TrimPrefix(prerelease, "-")
	}
	return goVersion
}

// finalDigitsIndex returns the index of the first digit in the sequence of digits ending s.
// If s doesn't end in digits, it returns -1.
func finalDigitsIndex(s string) int {
	// Assume ASCII (since the semver package does anyway).
	var i int
	for i = len(s) - 1; i >= 0; i-- {
		if s[i] < '0' || s[i] > '9' {
			break
		}
	}
	if i == len(s)-1 {
		return -1
	}
	return i + 1
}

// osvsByModule runs a govulncheck database query.
func osvsByModule(ctx context.Context, db, moduleVersion string) ([]*osv.Entry, error) {
	var args []string
	args = append(args, "-mode=query", "-json")
	if db != "" {
		args = append(args, "-db="+db)
	}
	args = append(args, moduleVersion)

	ir, iw := io.Pipe()
	handler := &osvReader{}

	var g errgroup.Group
	g.Go(func() error {
		defer iw.Close() // scan API doesn't close cmd.Stderr/cmd.Stdout.
		cmd := scan.Command(ctx, args...)
		cmd.Stdout = iw
		// TODO(hakim): Do we need to set cmd.Env = getEnvSlices(),
		// or is the process environment good enough?
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Wait()
	})
	g.Go(func() error {
		return govulncheck.HandleJSON(ir, handler)
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return handler.entry, nil
}

// osvReader implements govulncheck.Handler.
type osvReader struct {
	entry []*osv.Entry
}

func (h *osvReader) OSV(entry *osv.Entry) error {
	h.entry = append(h.entry, entry)
	return nil
}

func (h *osvReader) Config(config *govulncheck.Config) error {
	return nil
}

func (h *osvReader) Finding(finding *govulncheck.Finding) error {
	return nil
}

func (h *osvReader) Progress(progress *govulncheck.Progress) error {
	return nil
}
