// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"slices"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/telemetry/counter"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/metadata"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/debug"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/golang"
	"golang.org/x/tools/gopls/internal/progress"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/gopls/internal/vulncheck/scan"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/tokeninternal"
	"golang.org/x/tools/internal/xcontext"
)

func (s *server) ExecuteCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (interface{}, error) {
	ctx, done := event.Start(ctx, "lsp.Server.executeCommand")
	defer done()

	// For test synchronization, always create a progress notification.
	//
	// This may be in addition to user-facing progress notifications created in
	// the course of command execution.
	if s.Options().VerboseWorkDoneProgress {
		work := s.progress.Start(ctx, params.Command, "Verbose: running command...", nil, nil)
		defer work.End(ctx, "Done.")
	}

	var found bool
	for _, name := range s.Options().SupportedCommands {
		if name == params.Command {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%s is not a supported command", params.Command)
	}

	handler := &commandHandler{
		s:      s,
		params: params,
	}
	return command.Dispatch(ctx, params, handler)
}

type commandHandler struct {
	s      *server
	params *protocol.ExecuteCommandParams
}

func (h *commandHandler) Modules(ctx context.Context, args command.ModulesArgs) (command.ModulesResult, error) {
	// keepModule filters modules based on the command args
	keepModule := func(goMod protocol.DocumentURI) bool {
		// Does the directory enclose the view's go.mod file?
		if !args.Dir.Encloses(goMod) {
			return false
		}

		// Calculate the relative path
		rel, err := filepath.Rel(args.Dir.Path(), goMod.Path())
		if err != nil {
			return false // "can't happen" (see prior Encloses check)
		}

		assert(filepath.Base(goMod.Path()) == "go.mod", fmt.Sprintf("invalid go.mod path: want go.mod, got %q", goMod.Path()))

		// Invariant: rel is a relative path without "../" segments and the last
		// segment is "go.mod"
		nparts := strings.Count(rel, string(filepath.Separator))
		return args.MaxDepth < 0 || nparts <= args.MaxDepth
	}

	// Views may include:
	//   - go.work views containing one or more modules each;
	//   - go.mod views containing a single module each;
	//   - GOPATH and/or ad hoc views containing no modules.
	//
	// Retrieving a view via the request path would only work for a
	// non-recursive query for a go.mod view, and even in that case
	// [Session.SnapshotOf] doesn't work on directories. Thus we check every
	// view.
	var result command.ModulesResult
	seen := map[protocol.DocumentURI]bool{}
	for _, v := range h.s.session.Views() {
		s, release, err := v.Snapshot()
		if err != nil {
			return command.ModulesResult{}, err
		}
		defer release()

		for _, modFile := range v.ModFiles() {
			if !keepModule(modFile) {
				continue
			}

			// Deduplicate
			if seen[modFile] {
				continue
			}
			seen[modFile] = true

			fh, err := s.ReadFile(ctx, modFile)
			if err != nil {
				return command.ModulesResult{}, err
			}
			mod, err := s.ParseMod(ctx, fh)
			if err != nil {
				return command.ModulesResult{}, err
			}
			if mod.File.Module == nil {
				continue // syntax contains errors
			}
			result.Modules = append(result.Modules, command.Module{
				Path:    mod.File.Module.Mod.Path,
				Version: mod.File.Module.Mod.Version,
				GoMod:   mod.URI,
			})
		}
	}
	return result, nil
}

func (h *commandHandler) Packages(ctx context.Context, args command.PackagesArgs) (command.PackagesResult, error) {
	// Convert file arguments into directories
	dirs := make([]protocol.DocumentURI, len(args.Files))
	for i, file := range args.Files {
		if filepath.Ext(file.Path()) == ".go" {
			dirs[i] = file.Dir()
		} else {
			dirs[i] = file
		}
	}

	keepPackage := func(pkg *metadata.Package) bool {
		for _, file := range pkg.GoFiles {
			for _, dir := range dirs {
				if file.Dir() == dir || args.Recursive && dir.Encloses(file) {
					return true
				}
			}
		}
		return false
	}

	result := command.PackagesResult{
		Module: make(map[string]command.Module),
	}

	err := h.run(ctx, commandConfig{
		progress: "Packages",
	}, func(ctx context.Context, _ commandDeps) error {
		for _, view := range h.s.session.Views() {
			snapshot, release, err := view.Snapshot()
			if err != nil {
				return err
			}
			defer release()

			metas, err := snapshot.WorkspaceMetadata(ctx)
			if err != nil {
				return err
			}

			// Filter out unwanted packages
			metas = slices.DeleteFunc(metas, func(meta *metadata.Package) bool {
				return meta.IsIntermediateTestVariant() ||
					!keepPackage(meta)
			})

			start := len(result.Packages)
			for _, meta := range metas {
				var mod command.Module
				if meta.Module != nil {
					mod = command.Module{
						Path:    meta.Module.Path,
						Version: meta.Module.Version,
						GoMod:   protocol.URIFromPath(meta.Module.GoMod),
					}
					result.Module[mod.Path] = mod // Overwriting is ok
				}

				result.Packages = append(result.Packages, command.Package{
					Path:       string(meta.PkgPath),
					ForTest:    string(meta.ForTest),
					ModulePath: mod.Path,
				})
			}

			if args.Mode&command.NeedTests == 0 {
				continue
			}

			// Make a single request to the index (per snapshot) to minimize the
			// performance hit
			var ids []cache.PackageID
			for _, meta := range metas {
				ids = append(ids, meta.ID)
			}

			allTests, err := snapshot.Tests(ctx, ids...)
			if err != nil {
				return err
			}

			for i, tests := range allTests {
				pkg := &result.Packages[start+i]
				fileByPath := map[protocol.DocumentURI]*command.TestFile{}
				for _, test := range tests.All() {
					test := command.TestCase{
						Name: test.Name,
						Loc:  test.Location,
					}

					file, ok := fileByPath[test.Loc.URI]
					if !ok {
						f := command.TestFile{
							URI: test.Loc.URI,
						}
						i := len(pkg.TestFiles)
						pkg.TestFiles = append(pkg.TestFiles, f)
						file = &pkg.TestFiles[i]
						fileByPath[test.Loc.URI] = file
					}
					file.Tests = append(file.Tests, test)
				}
			}
		}

		return nil
	})
	return result, err
}

func (h *commandHandler) MaybePromptForTelemetry(ctx context.Context) error {
	// if the server's TelemetryPrompt is true, it's likely the server already
	// handled prompting for it. Don't try to prompt again.
	if !h.s.options.TelemetryPrompt {
		go h.s.maybePromptForTelemetry(ctx, true)
	}
	return nil
}

func (*commandHandler) AddTelemetryCounters(_ context.Context, args command.AddTelemetryCountersArgs) error {
	if len(args.Names) != len(args.Values) {
		return fmt.Errorf("Names and Values must have the same length")
	}
	// invalid counter update requests will be silently dropped. (no audience)
	for i, n := range args.Names {
		v := args.Values[i]
		if n == "" || v < 0 {
			continue
		}
		counter.Add("fwd/"+n, v)
	}
	return nil
}

func (c *commandHandler) AddTest(ctx context.Context, loc protocol.Location) (*protocol.WorkspaceEdit, error) {
	var result *protocol.WorkspaceEdit
	err := c.run(ctx, commandConfig{
		forURI: loc.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		if deps.snapshot.FileKind(deps.fh) != file.Go {
			return fmt.Errorf("can't add test for non-Go file")
		}
		docedits, err := golang.AddTestForFunc(ctx, deps.snapshot, loc)
		if err != nil {
			return err
		}
		return applyChanges(ctx, c.s.client, docedits)
	})
	// TODO(hxjiang): move the cursor to the new test once edits applied.
	return result, err
}

// commandConfig configures common command set-up and execution.
type commandConfig struct {
	requireSave   bool                           // whether all files must be saved for the command to work
	progress      string                         // title to use for progress reporting. If empty, no progress will be reported.
	progressStyle settings.WorkDoneProgressStyle // style information for client-side progress display.
	forView       string                         // view to resolve to a snapshot; incompatible with forURI
	forURI        protocol.DocumentURI           // URI to resolve to a snapshot. If unset, snapshot will be nil.
}

// commandDeps is evaluated from a commandConfig. Note that not all fields may
// be populated, depending on which configuration is set. See comments in-line
// for details.
type commandDeps struct {
	snapshot *cache.Snapshot    // present if cfg.forURI or forView was set
	fh       file.Handle        // present if cfg.forURI was set
	work     *progress.WorkDone // present if cfg.progress was set
}

type commandFunc func(context.Context, commandDeps) error

// These strings are reported as the final WorkDoneProgressEnd message
// for each workspace/executeCommand request.
const (
	CommandCanceled  = "canceled"
	CommandFailed    = "failed"
	CommandCompleted = "completed"
)

// run performs command setup for command execution, and invokes the given run
// function. If cfg.async is set, run executes the given func in a separate
// goroutine, and returns as soon as setup is complete and the goroutine is
// scheduled.
//
// Invariant: if the resulting error is non-nil, the given run func will
// (eventually) be executed exactly once.
func (c *commandHandler) run(ctx context.Context, cfg commandConfig, run commandFunc) (err error) {
	if cfg.requireSave {
		var unsaved []string
		for _, overlay := range c.s.session.Overlays() {
			if !overlay.SameContentsOnDisk() {
				unsaved = append(unsaved, overlay.URI().Path())
			}
		}
		if len(unsaved) > 0 {
			return fmt.Errorf("All files must be saved first (unsaved: %v).", unsaved)
		}
	}
	var deps commandDeps
	var release func()
	if cfg.forURI != "" && cfg.forView != "" {
		return bug.Errorf("internal error: forURI=%q, forView=%q", cfg.forURI, cfg.forView)
	}
	if cfg.forURI != "" {
		deps.fh, deps.snapshot, release, err = c.s.fileOf(ctx, cfg.forURI)
		if err != nil {
			return err
		}

	} else if cfg.forView != "" {
		view, err := c.s.session.View(cfg.forView)
		if err != nil {
			return err
		}
		deps.snapshot, release, err = view.Snapshot()
		if err != nil {
			return err
		}

	} else {
		release = func() {}
	}
	// Inv: release() must be called exactly once after this point.
	// In the async case, runcmd may outlive run().

	ctx, cancel := context.WithCancel(xcontext.Detach(ctx))
	if cfg.progress != "" {
		header := ""
		if _, ok := c.s.options.SupportedWorkDoneProgressFormats[cfg.progressStyle]; ok && cfg.progressStyle != "" {
			header = fmt.Sprintf("style: %s\n\n", cfg.progressStyle)
		}
		deps.work = c.s.progress.Start(ctx, cfg.progress, header+"Running...", c.params.WorkDoneToken, cancel)
	}
	runcmd := func() error {
		defer release()
		defer cancel()
		err := run(ctx, deps)
		if deps.work != nil {
			switch {
			case errors.Is(err, context.Canceled):
				deps.work.End(ctx, CommandCanceled)
			case err != nil:
				event.Error(ctx, "command error", err)
				deps.work.End(ctx, CommandFailed)
			default:
				deps.work.End(ctx, CommandCompleted)
			}
		}
		return err
	}

	// For legacy reasons, gopls.run_govulncheck must run asynchronously.
	// TODO(golang/vscode-go#3572): remove this (along with the
	// gopls.run_govulncheck command entirely) once VS Code only uses the new
	// gopls.vulncheck command.
	if c.params.Command == "gopls.run_govulncheck" {
		if cfg.progress == "" {
			log.Fatalf("asynchronous command gopls.run_govulncheck does not enable progress reporting")
		}
		go func() {
			if err := runcmd(); err != nil {
				showMessage(ctx, c.s.client, protocol.Error, err.Error())
			}
		}()
		return nil
	}

	return runcmd()
}

func (c *commandHandler) ApplyFix(ctx context.Context, args command.ApplyFixArgs) (*protocol.WorkspaceEdit, error) {
	var result *protocol.WorkspaceEdit
	err := c.run(ctx, commandConfig{
		// Note: no progress here. Applying fixes should be quick.
		forURI: args.Location.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		changes, err := golang.ApplyFix(ctx, args.Fix, deps.snapshot, deps.fh, args.Location.Range)
		if err != nil {
			return err
		}
		wsedit := protocol.NewWorkspaceEdit(changes...)
		if args.ResolveEdits {
			result = wsedit
			return nil
		}
		return applyChanges(ctx, c.s.client, changes)
	})
	return result, err
}

func (c *commandHandler) RegenerateCgo(ctx context.Context, args command.URIArg) error {
	return c.run(ctx, commandConfig{
		progress: "Regenerating Cgo",
	}, func(ctx context.Context, _ commandDeps) error {
		return c.modifyState(ctx, FromRegenerateCgo, func() (*cache.Snapshot, func(), error) {
			// Resetting the view causes cgo to be regenerated via `go list`.
			v, err := c.s.session.ResetView(ctx, args.URI)
			if err != nil {
				return nil, nil, err
			}
			return v.Snapshot()
		})
	})
}

// modifyState performs an operation that modifies the snapshot state.
//
// It causes a snapshot diagnosis for the provided ModificationSource.
func (c *commandHandler) modifyState(ctx context.Context, source ModificationSource, work func() (*cache.Snapshot, func(), error)) error {
	var wg sync.WaitGroup // tracks work done on behalf of this function, incl. diagnostics
	wg.Add(1)
	defer wg.Done()

	// Track progress on this operation for testing.
	if c.s.Options().VerboseWorkDoneProgress {
		work := c.s.progress.Start(ctx, DiagnosticWorkTitle(source), "Calculating file diagnostics...", nil, nil)
		go func() {
			wg.Wait()
			work.End(ctx, "Done.")
		}()
	}
	snapshot, release, err := work()
	if err != nil {
		return err
	}
	wg.Add(1)
	go func() {
		// Diagnosing with the background context ensures new snapshots are fully
		// diagnosed.
		c.s.diagnoseSnapshot(snapshot.BackgroundContext(), snapshot, nil, 0)
		release()
		wg.Done()
	}()
	return nil
}

func (c *commandHandler) CheckUpgrades(ctx context.Context, args command.CheckUpgradesArgs) error {
	return c.run(ctx, commandConfig{
		forURI:   args.URI,
		progress: "Checking for upgrades",
	}, func(ctx context.Context, deps commandDeps) error {
		return c.modifyState(ctx, FromCheckUpgrades, func() (*cache.Snapshot, func(), error) {
			upgrades, err := c.s.getUpgrades(ctx, deps.snapshot, args.URI, args.Modules)
			if err != nil {
				return nil, nil, err
			}
			return c.s.session.InvalidateView(ctx, deps.snapshot.View(), cache.StateChange{
				ModuleUpgrades: map[protocol.DocumentURI]map[string]string{args.URI: upgrades},
			})
		})
	})
}

func (c *commandHandler) AddDependency(ctx context.Context, args command.DependencyArgs) error {
	return c.GoGetModule(ctx, args)
}

func (c *commandHandler) UpgradeDependency(ctx context.Context, args command.DependencyArgs) error {
	return c.GoGetModule(ctx, args)
}

func (c *commandHandler) ResetGoModDiagnostics(ctx context.Context, args command.ResetGoModDiagnosticsArgs) error {
	return c.run(ctx, commandConfig{
		forURI: args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		return c.modifyState(ctx, FromResetGoModDiagnostics, func() (*cache.Snapshot, func(), error) {
			return c.s.session.InvalidateView(ctx, deps.snapshot.View(), cache.StateChange{
				ModuleUpgrades: map[protocol.DocumentURI]map[string]string{
					deps.fh.URI(): nil,
				},
				Vulns: map[protocol.DocumentURI]*vulncheck.Result{
					deps.fh.URI(): nil,
				},
			})
		})
	})
}

func (c *commandHandler) GoGetModule(ctx context.Context, args command.DependencyArgs) error {
	return c.run(ctx, commandConfig{
		progress: "Running go get",
		forURI:   args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		return c.s.runGoModUpdateCommands(ctx, deps.snapshot, args.URI, func(invoke func(...string) (*bytes.Buffer, error)) error {
			return runGoGetModule(invoke, args.AddRequire, args.GoCmdArgs)
		})
	})
}

// TODO(rFindley): UpdateGoSum, Tidy, and Vendor could probably all be one command.
func (c *commandHandler) UpdateGoSum(ctx context.Context, args command.URIArgs) error {
	return c.run(ctx, commandConfig{
		progress: "Updating go.sum",
	}, func(ctx context.Context, _ commandDeps) error {
		for _, uri := range args.URIs {
			fh, snapshot, release, err := c.s.fileOf(ctx, uri)
			if err != nil {
				return err
			}
			defer release()
			if err := c.s.runGoModUpdateCommands(ctx, snapshot, fh.URI(), func(invoke func(...string) (*bytes.Buffer, error)) error {
				_, err := invoke("list", "all")
				return err
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *commandHandler) Tidy(ctx context.Context, args command.URIArgs) error {
	return c.run(ctx, commandConfig{
		progress: "Running go mod tidy",
	}, func(ctx context.Context, _ commandDeps) error {
		for _, uri := range args.URIs {
			fh, snapshot, release, err := c.s.fileOf(ctx, uri)
			if err != nil {
				return err
			}
			defer release()
			if err := c.s.runGoModUpdateCommands(ctx, snapshot, fh.URI(), func(invoke func(...string) (*bytes.Buffer, error)) error {
				_, err := invoke("mod", "tidy")
				return err
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *commandHandler) Vendor(ctx context.Context, args command.URIArg) error {
	return c.run(ctx, commandConfig{
		requireSave: true, // TODO(adonovan): probably not needed; but needs a test.
		progress:    "Running go mod vendor",
		forURI:      args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		// Use RunGoCommandPiped here so that we don't compete with any other go
		// command invocations. go mod vendor deletes modules.txt before recreating
		// it, and therefore can run into file locking issues on Windows if that
		// file is in use by another process, such as go list.
		//
		// If golang/go#44119 is resolved, go mod vendor will instead modify
		// modules.txt in-place. In that case we could theoretically allow this
		// command to run concurrently.
		stderr := new(bytes.Buffer)
		inv, cleanupInvocation, err := deps.snapshot.GoCommandInvocation(cache.NetworkOK, args.URI.DirPath(), "mod", []string{"vendor"})
		if err != nil {
			return err
		}
		defer cleanupInvocation()
		err = deps.snapshot.View().GoCommandRunner().RunPiped(ctx, *inv, &bytes.Buffer{}, stderr)
		if err != nil {
			return fmt.Errorf("running go mod vendor failed: %v\nstderr:\n%s", err, stderr.String())
		}
		return nil
	})
}

func (c *commandHandler) EditGoDirective(ctx context.Context, args command.EditGoDirectiveArgs) error {
	return c.run(ctx, commandConfig{
		requireSave: true, // if go.mod isn't saved it could cause a problem
		forURI:      args.URI,
	}, func(ctx context.Context, _ commandDeps) error {
		fh, snapshot, release, err := c.s.fileOf(ctx, args.URI)
		if err != nil {
			return err
		}
		defer release()
		if err := c.s.runGoModUpdateCommands(ctx, snapshot, fh.URI(), func(invoke func(...string) (*bytes.Buffer, error)) error {
			_, err := invoke("mod", "edit", "-go", args.Version)
			return err
		}); err != nil {
			return err
		}
		return nil
	})
}

func (c *commandHandler) RemoveDependency(ctx context.Context, args command.RemoveDependencyArgs) error {
	return c.run(ctx, commandConfig{
		progress: "Removing dependency",
		forURI:   args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		// See the documentation for OnlyDiagnostic.
		//
		// TODO(rfindley): In Go 1.17+, we will be able to use the go command
		// without checking if the module is tidy.
		if args.OnlyDiagnostic {
			return c.s.runGoModUpdateCommands(ctx, deps.snapshot, args.URI, func(invoke func(...string) (*bytes.Buffer, error)) error {
				if err := runGoGetModule(invoke, false, []string{args.ModulePath + "@none"}); err != nil {
					return err
				}
				_, err := invoke("mod", "tidy")
				return err
			})
		}
		pm, err := deps.snapshot.ParseMod(ctx, deps.fh)
		if err != nil {
			return err
		}
		edits, err := dropDependency(pm, args.ModulePath)
		if err != nil {
			return err
		}
		return applyChanges(ctx, c.s.client, []protocol.DocumentChange{protocol.DocumentChangeEdit(deps.fh, edits)})
	})
}

// dropDependency returns the edits to remove the given require from the go.mod
// file.
func dropDependency(pm *cache.ParsedModule, modulePath string) ([]protocol.TextEdit, error) {
	// We need a private copy of the parsed go.mod file, since we're going to
	// modify it.
	copied, err := modfile.Parse("", pm.Mapper.Content, nil)
	if err != nil {
		return nil, err
	}
	if err := copied.DropRequire(modulePath); err != nil {
		return nil, err
	}
	copied.Cleanup()
	newContent, err := copied.Format()
	if err != nil {
		return nil, err
	}
	// Calculate the edits to be made due to the change.
	diff := diff.Bytes(pm.Mapper.Content, newContent)
	return protocol.EditsFromDiffEdits(pm.Mapper, diff)
}

func (c *commandHandler) Doc(ctx context.Context, args command.DocArgs) (protocol.URI, error) {
	if args.Location.URI == "" {
		return "", errors.New("missing location URI")
	}

	var result protocol.URI
	err := c.run(ctx, commandConfig{
		progress: "", // the operation should be fast
		forURI:   args.Location.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		pkg, pgf, err := golang.NarrowestPackageForFile(ctx, deps.snapshot, args.Location.URI)
		if err != nil {
			return err
		}
		start, end, err := pgf.RangePos(args.Location.Range)
		if err != nil {
			return err
		}

		// Start web server.
		web, err := c.s.getWeb()
		if err != nil {
			return err
		}

		// Compute package path and optional symbol fragment
		// (e.g. "#Buffer.Len") from the the selection.
		pkgpath, fragment, _ := golang.DocFragment(pkg, pgf, start, end)

		// Direct the client to open the /pkg page.
		result = web.PkgURL(deps.snapshot.View().ID(), pkgpath, fragment)
		if args.ShowDocument {
			openClientBrowser(ctx, c.s.client, "Doc", result, c.s.Options())
		}

		return nil
	})
	return result, err
}

func (c *commandHandler) RunTests(ctx context.Context, args command.RunTestsArgs) error {
	return c.run(ctx, commandConfig{
		progress:    "Running go test", // (asynchronous)
		requireSave: true,              // go test honors overlays, but tests themselves cannot
		forURI:      args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		jsonrpc2.Async(ctx) // don't block RPCs behind this command, since it can take a while
		return c.runTests(ctx, deps.snapshot, deps.work, args.URI, args.Tests, args.Benchmarks)
	})
}

func (c *commandHandler) runTests(ctx context.Context, snapshot *cache.Snapshot, work *progress.WorkDone, uri protocol.DocumentURI, tests, benchmarks []string) error {
	// TODO: fix the error reporting when this runs async.
	meta, err := golang.NarrowestMetadataForFile(ctx, snapshot, uri)
	if err != nil {
		return err
	}
	pkgPath := string(meta.ForTest)

	// create output
	buf := &bytes.Buffer{}
	ew := progress.NewEventWriter(ctx, "test")
	out := io.MultiWriter(ew, progress.NewWorkDoneWriter(ctx, work), buf)

	// Run `go test -run Func` on each test.
	var failedTests int
	for _, funcName := range tests {
		args := []string{pkgPath, "-v", "-count=1", fmt.Sprintf("-run=^%s$", regexp.QuoteMeta(funcName))}
		inv, cleanupInvocation, err := snapshot.GoCommandInvocation(cache.NoNetwork, uri.DirPath(), "test", args)
		if err != nil {
			return err
		}
		defer cleanupInvocation()
		if err := snapshot.View().GoCommandRunner().RunPiped(ctx, *inv, out, out); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			failedTests++
		}
	}

	// Run `go test -run=^$ -bench Func` on each test.
	var failedBenchmarks int
	for _, funcName := range benchmarks {
		inv, cleanupInvocation, err := snapshot.GoCommandInvocation(cache.NoNetwork, uri.DirPath(), "test", []string{
			pkgPath, "-v", "-run=^$", fmt.Sprintf("-bench=^%s$", regexp.QuoteMeta(funcName)),
		})
		if err != nil {
			return err
		}
		defer cleanupInvocation()
		if err := snapshot.View().GoCommandRunner().RunPiped(ctx, *inv, out, out); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			failedBenchmarks++
		}
	}

	var title string
	if len(tests) > 0 && len(benchmarks) > 0 {
		title = "tests and benchmarks"
	} else if len(tests) > 0 {
		title = "tests"
	} else if len(benchmarks) > 0 {
		title = "benchmarks"
	} else {
		return errors.New("No functions were provided")
	}
	message := fmt.Sprintf("all %s passed", title)
	if failedTests > 0 && failedBenchmarks > 0 {
		message = fmt.Sprintf("%d / %d tests failed and %d / %d benchmarks failed", failedTests, len(tests), failedBenchmarks, len(benchmarks))
	} else if failedTests > 0 {
		message = fmt.Sprintf("%d / %d tests failed", failedTests, len(tests))
	} else if failedBenchmarks > 0 {
		message = fmt.Sprintf("%d / %d benchmarks failed", failedBenchmarks, len(benchmarks))
	}
	if failedTests > 0 || failedBenchmarks > 0 {
		message += "\n" + buf.String()
	}

	showMessage(ctx, c.s.client, protocol.Info, message)

	if failedTests > 0 || failedBenchmarks > 0 {
		return errors.New("gopls.test command failed")
	}
	return nil
}

func (c *commandHandler) Generate(ctx context.Context, args command.GenerateArgs) error {
	title := "Running go generate ."
	if args.Recursive {
		title = "Running go generate ./..."
	}
	return c.run(ctx, commandConfig{
		requireSave: true, // commands executed by go generate cannot honor overlays
		progress:    title,
		forURI:      args.Dir,
	}, func(ctx context.Context, deps commandDeps) error {
		er := progress.NewEventWriter(ctx, "generate")

		pattern := "."
		if args.Recursive {
			pattern = "./..."
		}
		inv, cleanupInvocation, err := deps.snapshot.GoCommandInvocation(cache.NetworkOK, args.Dir.Path(), "generate", []string{"-x", pattern})
		if err != nil {
			return err
		}
		defer cleanupInvocation()
		stderr := io.MultiWriter(er, progress.NewWorkDoneWriter(ctx, deps.work))
		if err := deps.snapshot.View().GoCommandRunner().RunPiped(ctx, *inv, er, stderr); err != nil {
			return err
		}
		return nil
	})
}

func (c *commandHandler) GoGetPackage(ctx context.Context, args command.GoGetPackageArgs) error {
	return c.run(ctx, commandConfig{
		forURI:   args.URI,
		progress: "Running go get",
	}, func(ctx context.Context, deps commandDeps) error {
		snapshot := deps.snapshot
		modURI := snapshot.GoModForFile(args.URI)
		if modURI == "" {
			return fmt.Errorf("no go.mod file found for %s", args.URI)
		}
		tempDir, cleanupModDir, err := cache.TempModDir(ctx, snapshot, modURI)
		if err != nil {
			return fmt.Errorf("creating a temp go.mod: %v", err)
		}
		defer cleanupModDir()

		inv, cleanupInvocation, err := snapshot.GoCommandInvocation(cache.NetworkOK, modURI.DirPath(), "list",
			[]string{"-f", "{{.Module.Path}}@{{.Module.Version}}", "-mod=mod", "-modfile=" + filepath.Join(tempDir, "go.mod"), args.Pkg},
			"GOWORK=off",
		)
		if err != nil {
			return err
		}
		defer cleanupInvocation()
		stdout, err := snapshot.View().GoCommandRunner().Run(ctx, *inv)
		if err != nil {
			return err
		}
		ver := strings.TrimSpace(stdout.String())
		return c.s.runGoModUpdateCommands(ctx, snapshot, args.URI, func(invoke func(...string) (*bytes.Buffer, error)) error {
			if args.AddRequire {
				if err := addModuleRequire(invoke, []string{ver}); err != nil {
					return err
				}
			}
			_, err := invoke(append([]string{"get", "-d"}, args.Pkg)...)
			return err
		})
	})
}

func (s *server) runGoModUpdateCommands(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, run func(invoke func(...string) (*bytes.Buffer, error)) error) error {
	// TODO(rfindley): can/should this use findRootPattern?
	modURI := snapshot.GoModForFile(uri)
	if modURI == "" {
		return fmt.Errorf("no go.mod file found for %s", uri.Path())
	}
	newModBytes, newSumBytes, err := snapshot.RunGoModUpdateCommands(ctx, modURI, run)
	if err != nil {
		return err
	}
	sumURI := protocol.URIFromPath(strings.TrimSuffix(modURI.Path(), ".mod") + ".sum")

	modChange, err := computeEditChange(ctx, snapshot, modURI, newModBytes)
	if err != nil {
		return err
	}
	sumChange, err := computeEditChange(ctx, snapshot, sumURI, newSumBytes)
	if err != nil {
		return err
	}

	var changes []protocol.DocumentChange
	if modChange.Valid() {
		changes = append(changes, modChange)
	}
	if sumChange.Valid() {
		changes = append(changes, sumChange)
	}
	return applyChanges(ctx, s.client, changes)
}

// computeEditChange computes the edit change required to transform the
// snapshot file specified by uri to the provided new content.
// Beware: returns a DocumentChange that is !Valid() if none were necessary.
//
// If the file is not open, computeEditChange simply writes the new content to
// disk.
//
// TODO(rfindley): fix this API asymmetry. It should be up to the caller to
// write the file or apply the edits.
func computeEditChange(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, newContent []byte) (protocol.DocumentChange, error) {
	fh, err := snapshot.ReadFile(ctx, uri)
	if err != nil {
		return protocol.DocumentChange{}, err
	}
	oldContent, err := fh.Content()
	if err != nil && !os.IsNotExist(err) {
		return protocol.DocumentChange{}, err
	}

	if bytes.Equal(oldContent, newContent) {
		return protocol.DocumentChange{}, nil // note: result is !Valid()
	}

	// Sending a workspace edit to a closed file causes VS Code to open the
	// file and leave it unsaved. We would rather apply the changes directly,
	// especially to go.sum, which should be mostly invisible to the user.
	if !snapshot.IsOpen(uri) {
		err := os.WriteFile(uri.Path(), newContent, 0666)
		return protocol.DocumentChange{}, err
	}

	m := protocol.NewMapper(fh.URI(), oldContent)
	diff := diff.Bytes(oldContent, newContent)
	textedits, err := protocol.EditsFromDiffEdits(m, diff)
	if err != nil {
		return protocol.DocumentChange{}, err
	}
	return protocol.DocumentChangeEdit(fh, textedits), nil
}

func applyChanges(ctx context.Context, cli protocol.Client, changes []protocol.DocumentChange) error {
	if len(changes) == 0 {
		return nil
	}
	response, err := cli.ApplyEdit(ctx, &protocol.ApplyWorkspaceEditParams{
		Edit: *protocol.NewWorkspaceEdit(changes...),
	})
	if err != nil {
		return err
	}
	if !response.Applied {
		return fmt.Errorf("edits not applied because of %s", response.FailureReason)
	}
	return nil
}

func runGoGetModule(invoke func(...string) (*bytes.Buffer, error), addRequire bool, args []string) error {
	if addRequire {
		if err := addModuleRequire(invoke, args); err != nil {
			return err
		}
	}
	_, err := invoke(append([]string{"get", "-d"}, args...)...)
	return err
}

func addModuleRequire(invoke func(...string) (*bytes.Buffer, error), args []string) error {
	// Using go get to create a new dependency results in an
	// `// indirect` comment we may not want. The only way to avoid it
	// is to add the require as direct first. Then we can use go get to
	// update go.sum and tidy up.
	_, err := invoke(append([]string{"mod", "edit", "-require"}, args...)...)
	return err
}

// TODO(rfindley): inline.
func (s *server) getUpgrades(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, modules []string) (map[string]string, error) {
	args := append([]string{"-mod=readonly", "-m", "-u", "-json"}, modules...)
	inv, cleanup, err := snapshot.GoCommandInvocation(cache.NetworkOK, uri.DirPath(), "list", args)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	stdout, err := snapshot.View().GoCommandRunner().Run(ctx, *inv)
	if err != nil {
		return nil, err
	}

	upgrades := map[string]string{}
	for dec := json.NewDecoder(stdout); dec.More(); {
		mod := &gocommand.ModuleJSON{}
		if err := dec.Decode(mod); err != nil {
			return nil, err
		}
		if mod.Update == nil {
			continue
		}
		upgrades[mod.Path] = mod.Update.Version
	}
	return upgrades, nil
}

func (c *commandHandler) GCDetails(ctx context.Context, uri protocol.DocumentURI) error {
	return c.run(ctx, commandConfig{
		forURI: uri,
	}, func(ctx context.Context, deps commandDeps) error {
		return c.modifyState(ctx, FromToggleCompilerOptDetails, func() (*cache.Snapshot, func(), error) {
			// Don't blindly use "dir := deps.fh.URI().Dir()"; validate.
			meta, err := golang.NarrowestMetadataForFile(ctx, deps.snapshot, deps.fh.URI())
			if err != nil {
				return nil, nil, err
			}
			if len(meta.CompiledGoFiles) == 0 {
				return nil, nil, fmt.Errorf("package %q does not compile file %q", meta.ID, deps.fh.URI())
			}
			dir := meta.CompiledGoFiles[0].Dir()

			want := !deps.snapshot.WantCompilerOptDetails(dir) // toggle per-directory flag
			return c.s.session.InvalidateView(ctx, deps.snapshot.View(), cache.StateChange{
				CompilerOptDetails: map[protocol.DocumentURI]bool{dir: want},
			})
		})
	})
}

func (c *commandHandler) ListKnownPackages(ctx context.Context, args command.URIArg) (command.ListKnownPackagesResult, error) {
	var result command.ListKnownPackagesResult
	err := c.run(ctx, commandConfig{
		progress: "Listing packages",
		forURI:   args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		pkgs, err := golang.KnownPackagePaths(ctx, deps.snapshot, deps.fh)
		for _, pkg := range pkgs {
			result.Packages = append(result.Packages, string(pkg))
		}
		return err
	})
	return result, err
}

func (c *commandHandler) ListImports(ctx context.Context, args command.URIArg) (command.ListImportsResult, error) {
	var result command.ListImportsResult
	err := c.run(ctx, commandConfig{
		forURI: args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		fh, err := deps.snapshot.ReadFile(ctx, args.URI)
		if err != nil {
			return err
		}
		pgf, err := deps.snapshot.ParseGo(ctx, fh, parsego.Header)
		if err != nil {
			return err
		}
		fset := tokeninternal.FileSetFor(pgf.Tok)
		for _, group := range astutil.Imports(fset, pgf.File) {
			for _, imp := range group {
				if imp.Path == nil {
					continue
				}
				var name string
				if imp.Name != nil {
					name = imp.Name.Name
				}
				result.Imports = append(result.Imports, command.FileImport{
					Path: string(metadata.UnquoteImportPath(imp)),
					Name: name,
				})
			}
		}
		meta, err := golang.NarrowestMetadataForFile(ctx, deps.snapshot, args.URI)
		if err != nil {
			return err // e.g. cancelled
		}
		for pkgPath := range meta.DepsByPkgPath {
			result.PackageImports = append(result.PackageImports,
				command.PackageImport{Path: string(pkgPath)})
		}
		sort.Slice(result.PackageImports, func(i, j int) bool {
			return result.PackageImports[i].Path < result.PackageImports[j].Path
		})
		return nil
	})
	return result, err
}

func (c *commandHandler) AddImport(ctx context.Context, args command.AddImportArgs) error {
	return c.run(ctx, commandConfig{
		progress: "Adding import",
		forURI:   args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		edits, err := golang.AddImport(ctx, deps.snapshot, deps.fh, args.ImportPath)
		if err != nil {
			return fmt.Errorf("could not add import: %v", err)
		}
		return applyChanges(ctx, c.s.client, []protocol.DocumentChange{protocol.DocumentChangeEdit(deps.fh, edits)})
	})
}

func (c *commandHandler) ExtractToNewFile(ctx context.Context, args protocol.Location) error {
	return c.run(ctx, commandConfig{
		progress: "Extract to a new file",
		forURI:   args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		changes, err := golang.ExtractToNewFile(ctx, deps.snapshot, deps.fh, args.Range)
		if err != nil {
			return err
		}
		return applyChanges(ctx, c.s.client, changes)
	})
}

func (c *commandHandler) StartDebugging(ctx context.Context, args command.DebuggingArgs) (result command.DebuggingResult, _ error) {
	addr := args.Addr
	if addr == "" {
		addr = "localhost:0"
	}
	di := debug.GetInstance(ctx)
	if di == nil {
		return result, errors.New("internal error: server has no debugging instance")
	}
	listenedAddr, err := di.Serve(ctx, addr)
	if err != nil {
		return result, fmt.Errorf("starting debug server: %w", err)
	}
	result.URLs = []string{"http://" + listenedAddr}
	openClientBrowser(ctx, c.s.client, "Debug", result.URLs[0], c.s.Options())
	return result, nil
}

func (c *commandHandler) StartProfile(ctx context.Context, args command.StartProfileArgs) (result command.StartProfileResult, _ error) {
	file, err := os.CreateTemp("", "gopls-profile-*")
	if err != nil {
		return result, fmt.Errorf("creating temp profile file: %v", err)
	}

	c.s.ongoingProfileMu.Lock()
	defer c.s.ongoingProfileMu.Unlock()

	if c.s.ongoingProfile != nil {
		file.Close() // ignore error
		return result, fmt.Errorf("profile already started (for %q)", c.s.ongoingProfile.Name())
	}

	if err := pprof.StartCPUProfile(file); err != nil {
		file.Close() // ignore error
		return result, fmt.Errorf("starting profile: %v", err)
	}

	c.s.ongoingProfile = file
	return result, nil
}

func (c *commandHandler) StopProfile(ctx context.Context, args command.StopProfileArgs) (result command.StopProfileResult, _ error) {
	c.s.ongoingProfileMu.Lock()
	defer c.s.ongoingProfileMu.Unlock()

	prof := c.s.ongoingProfile
	c.s.ongoingProfile = nil

	if prof == nil {
		return result, fmt.Errorf("no ongoing profile")
	}

	pprof.StopCPUProfile()
	if err := prof.Close(); err != nil {
		return result, fmt.Errorf("closing profile file: %v", err)
	}
	result.File = prof.Name()
	return result, nil
}

func (c *commandHandler) FetchVulncheckResult(ctx context.Context, arg command.URIArg) (map[protocol.DocumentURI]*vulncheck.Result, error) {
	ret := map[protocol.DocumentURI]*vulncheck.Result{}
	err := c.run(ctx, commandConfig{forURI: arg.URI}, func(ctx context.Context, deps commandDeps) error {
		if deps.snapshot.Options().Vulncheck == settings.ModeVulncheckImports {
			for _, modfile := range deps.snapshot.View().ModFiles() {
				res, err := deps.snapshot.ModVuln(ctx, modfile)
				if err != nil {
					return err
				}
				ret[modfile] = res
			}
		}
		// Overwrite if there is any govulncheck-based result.
		for modfile, result := range deps.snapshot.Vulnerabilities() {
			ret[modfile] = result
		}
		return nil
	})
	return ret, err
}

const GoVulncheckCommandTitle = "govulncheck"

func (c *commandHandler) Vulncheck(ctx context.Context, args command.VulncheckArgs) (command.VulncheckResult, error) {
	if args.URI == "" {
		return command.VulncheckResult{}, errors.New("VulncheckArgs is missing URI field")
	}

	var commandResult command.VulncheckResult
	err := c.run(ctx, commandConfig{
		progress:      GoVulncheckCommandTitle,
		progressStyle: settings.WorkDoneProgressStyleLog,
		requireSave:   true, // govulncheck cannot honor overlays
		forURI:        args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		jsonrpc2.Async(ctx) // run this in parallel with other requests: vulncheck can be slow.

		workDoneWriter := progress.NewWorkDoneWriter(ctx, deps.work)
		dir := args.URI.DirPath()
		pattern := args.Pattern

		result, err := scan.RunGovulncheck(ctx, pattern, deps.snapshot, dir, workDoneWriter)
		if err != nil {
			return err
		}
		commandResult.Result = result
		commandResult.Token = deps.work.Token()

		snapshot, release, err := c.s.session.InvalidateView(ctx, deps.snapshot.View(), cache.StateChange{
			Vulns: map[protocol.DocumentURI]*vulncheck.Result{args.URI: result},
		})
		if err != nil {
			return err
		}
		defer release()

		// Diagnosing with the background context ensures new snapshots are fully
		// diagnosed.
		c.s.diagnoseSnapshot(snapshot.BackgroundContext(), snapshot, nil, 0)

		affecting := make(map[string]bool, len(result.Entries))
		for _, finding := range result.Findings {
			if len(finding.Trace) > 1 { // at least 2 frames if callstack exists (vulnerability, entry)
				affecting[finding.OSV] = true
			}
		}
		if len(affecting) == 0 {
			showMessage(ctx, c.s.client, protocol.Info, "No vulnerabilities found")
			return nil
		}
		affectingOSVs := make([]string, 0, len(affecting))
		for id := range affecting {
			affectingOSVs = append(affectingOSVs, id)
		}
		sort.Strings(affectingOSVs)

		showMessage(ctx, c.s.client, protocol.Warning, fmt.Sprintf("Found %v", strings.Join(affectingOSVs, ", ")))

		return nil
	})
	if err != nil {
		return command.VulncheckResult{}, err
	}
	return commandResult, nil
}

// RunGovulncheck is like Vulncheck (in fact, a copy), but is tweaked slightly
// to run asynchronously rather than return a result.
//
// This logic was copied, rather than factored out, as this implementation is
// slated for deletion.
//
// TODO(golang/vscode-go#3572)
// TODO(hxjiang): deprecate gopls.run_govulncheck.
func (c *commandHandler) RunGovulncheck(ctx context.Context, args command.VulncheckArgs) (command.RunVulncheckResult, error) {
	if args.URI == "" {
		return command.RunVulncheckResult{}, errors.New("VulncheckArgs is missing URI field")
	}

	// Return the workdone token so that clients can identify when this
	// vulncheck invocation is complete.
	//
	// Since the run function executes asynchronously, we use a channel to
	// synchronize the start of the run and return the token.
	tokenChan := make(chan protocol.ProgressToken, 1)
	err := c.run(ctx, commandConfig{
		progress:    GoVulncheckCommandTitle,
		requireSave: true, // govulncheck cannot honor overlays
		forURI:      args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		tokenChan <- deps.work.Token()

		workDoneWriter := progress.NewWorkDoneWriter(ctx, deps.work)
		dir := filepath.Dir(args.URI.Path())
		pattern := args.Pattern

		result, err := scan.RunGovulncheck(ctx, pattern, deps.snapshot, dir, workDoneWriter)
		if err != nil {
			return err
		}

		snapshot, release, err := c.s.session.InvalidateView(ctx, deps.snapshot.View(), cache.StateChange{
			Vulns: map[protocol.DocumentURI]*vulncheck.Result{args.URI: result},
		})
		if err != nil {
			return err
		}
		defer release()

		// Diagnosing with the background context ensures new snapshots are fully
		// diagnosed.
		c.s.diagnoseSnapshot(snapshot.BackgroundContext(), snapshot, nil, 0)

		affecting := make(map[string]bool, len(result.Entries))
		for _, finding := range result.Findings {
			if len(finding.Trace) > 1 { // at least 2 frames if callstack exists (vulnerability, entry)
				affecting[finding.OSV] = true
			}
		}
		if len(affecting) == 0 {
			showMessage(ctx, c.s.client, protocol.Info, "No vulnerabilities found")
			return nil
		}
		affectingOSVs := make([]string, 0, len(affecting))
		for id := range affecting {
			affectingOSVs = append(affectingOSVs, id)
		}
		sort.Strings(affectingOSVs)

		showMessage(ctx, c.s.client, protocol.Warning, fmt.Sprintf("Found %v", strings.Join(affectingOSVs, ", ")))

		return nil
	})
	if err != nil {
		return command.RunVulncheckResult{}, err
	}
	select {
	case <-ctx.Done():
		return command.RunVulncheckResult{}, ctx.Err()
	case token := <-tokenChan:
		return command.RunVulncheckResult{Token: token}, nil
	}
}

// MemStats implements the MemStats command. It returns an error as a
// future-proof API, but the resulting error is currently always nil.
func (c *commandHandler) MemStats(ctx context.Context) (command.MemStatsResult, error) {
	// GC a few times for stable results.
	runtime.GC()
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return command.MemStatsResult{
		HeapAlloc:  m.HeapAlloc,
		HeapInUse:  m.HeapInuse,
		TotalAlloc: m.TotalAlloc,
	}, nil
}

// WorkspaceStats implements the WorkspaceStats command, reporting information
// about the current state of the loaded workspace for the current session.
func (c *commandHandler) WorkspaceStats(ctx context.Context) (command.WorkspaceStatsResult, error) {
	var res command.WorkspaceStatsResult
	res.Files = c.s.session.Cache().FileStats()

	for _, view := range c.s.session.Views() {
		vs, err := collectViewStats(ctx, view)
		if err != nil {
			return res, err
		}
		res.Views = append(res.Views, vs)
	}
	return res, nil
}

func collectViewStats(ctx context.Context, view *cache.View) (command.ViewStats, error) {
	s, release, err := view.Snapshot()
	if err != nil {
		return command.ViewStats{}, err
	}
	defer release()

	allMD, err := s.AllMetadata(ctx)
	if err != nil {
		return command.ViewStats{}, err
	}
	allPackages := collectPackageStats(allMD)

	wsMD, err := s.WorkspaceMetadata(ctx)
	if err != nil {
		return command.ViewStats{}, err
	}
	workspacePackages := collectPackageStats(wsMD)

	var ids []golang.PackageID
	for _, mp := range wsMD {
		ids = append(ids, mp.ID)
	}

	diags, err := s.PackageDiagnostics(ctx, ids...)
	if err != nil {
		return command.ViewStats{}, err
	}

	ndiags := 0
	for _, d := range diags {
		ndiags += len(d)
	}

	return command.ViewStats{
		GoCommandVersion:  view.GoVersionString(),
		AllPackages:       allPackages,
		WorkspacePackages: workspacePackages,
		Diagnostics:       ndiags,
	}, nil
}

func collectPackageStats(mps []*metadata.Package) command.PackageStats {
	var stats command.PackageStats
	stats.Packages = len(mps)
	modules := make(map[string]bool)

	for _, mp := range mps {
		n := len(mp.CompiledGoFiles)
		stats.CompiledGoFiles += n
		if n > stats.LargestPackage {
			stats.LargestPackage = n
		}
		if mp.Module != nil {
			modules[mp.Module.Path] = true
		}
	}
	stats.Modules = len(modules)

	return stats
}

// RunGoWorkCommand invokes `go work <args>` with the provided arguments.
//
// args.InitFirst controls whether to first run `go work init`. This allows a
// single command to both create and recursively populate a go.work file -- as
// of writing there is no `go work init -r`.
//
// Some thought went into implementing this command. Unlike the go.mod commands
// above, this command simply invokes the go command and relies on the client
// to notify gopls of file changes via didChangeWatchedFile notifications.
// We could instead run these commands with GOWORK set to a temp file, but that
// poses the following problems:
//   - directory locations in the resulting temp go.work file will be computed
//     relative to the directory containing that go.work. If the go.work is in a
//     tempdir, the directories will need to be translated to/from that dir.
//   - it would be simpler to use a temp go.work file in the workspace
//     directory, or whichever directory contains the real go.work file, but
//     that sets a bad precedent of writing to a user-owned directory. We
//     shouldn't start doing that.
//   - Sending workspace edits to create a go.work file would require using
//     the CreateFile resource operation, which would need to be tested in every
//     client as we haven't used it before. We don't have time for that right
//     now.
//
// Therefore, we simply require that the current go.work file is saved (if it
// exists), and delegate to the go command.
func (c *commandHandler) RunGoWorkCommand(ctx context.Context, args command.RunGoWorkArgs) error {
	return c.run(ctx, commandConfig{
		progress: "Running go work command",
		forView:  args.ViewID,
	}, func(ctx context.Context, deps commandDeps) (runErr error) {
		snapshot := deps.snapshot
		view := snapshot.View()
		viewDir := snapshot.Folder().Path()

		if view.Type() != cache.GoWorkView && view.GoWork() != "" {
			// If we are not using an existing go.work file, GOWORK must be explicitly off.
			// TODO(rfindley): what about GO111MODULE=off?
			return fmt.Errorf("cannot modify go.work files when GOWORK=off")
		}

		var gowork string
		// If the user has explicitly set GOWORK=off, we should warn them
		// explicitly and avoid potentially misleading errors below.
		if view.GoWork() != "" {
			gowork = view.GoWork().Path()
			fh, err := snapshot.ReadFile(ctx, view.GoWork())
			if err != nil {
				return err // e.g. canceled
			}
			if !fh.SameContentsOnDisk() {
				return fmt.Errorf("must save workspace file %s before running go work commands", view.GoWork())
			}
		} else {
			if !args.InitFirst {
				// If go.work does not exist, we should have detected that and asked
				// for InitFirst.
				return bug.Errorf("internal error: cannot run go work command: required go.work file not found")
			}
			gowork = filepath.Join(viewDir, "go.work")
			if err := c.invokeGoWork(ctx, viewDir, gowork, []string{"init"}); err != nil {
				return fmt.Errorf("running `go work init`: %v", err)
			}
		}

		return c.invokeGoWork(ctx, viewDir, gowork, args.Args)
	})
}

func (c *commandHandler) invokeGoWork(ctx context.Context, viewDir, gowork string, args []string) error {
	inv := gocommand.Invocation{
		Verb:       "work",
		Args:       args,
		WorkingDir: viewDir,
		Env:        append(os.Environ(), fmt.Sprintf("GOWORK=%s", gowork)),
	}
	if _, err := c.s.session.GoCommandRunner().Run(ctx, inv); err != nil {
		return fmt.Errorf("running go work command: %v", err)
	}
	return nil
}

// showMessage causes the client to show a progress or error message.
//
// It reports whether it succeeded. If it fails, it writes an error to
// the server log, so most callers can safely ignore the result.
func showMessage(ctx context.Context, cli protocol.Client, typ protocol.MessageType, message string) bool {
	err := cli.ShowMessage(ctx, &protocol.ShowMessageParams{
		Type:    typ,
		Message: message,
	})
	if err != nil {
		event.Error(ctx, "client.showMessage: %v", err)
		return false
	}
	return true
}

// openClientBrowser causes the LSP client to open the specified URL
// in an external browser.
//
// If the client does not support window/showDocument, a window/showMessage
// request is instead used, with the format "$title: open your browser to $url".
func openClientBrowser(ctx context.Context, cli protocol.Client, title string, url protocol.URI, opts *settings.Options) {
	if opts.ShowDocumentSupported {
		showDocumentImpl(ctx, cli, url, nil, opts)
	} else {
		params := &protocol.ShowMessageParams{
			Type:    protocol.Info,
			Message: fmt.Sprintf("%s: open your browser to %s", title, url),
		}
		if err := cli.ShowMessage(ctx, params); err != nil {
			event.Error(ctx, "failed to show brower url", err)
		}
	}
}

// openClientEditor causes the LSP client to open the specified document
// and select the indicated range.
//
// Note that VS Code 1.87.2 doesn't currently raise the window; this is
// https://github.com/microsoft/vscode/issues/207634
func openClientEditor(ctx context.Context, cli protocol.Client, loc protocol.Location, opts *settings.Options) {
	if !opts.ShowDocumentSupported {
		return // no op
	}
	showDocumentImpl(ctx, cli, protocol.URI(loc.URI), &loc.Range, opts)
}

func showDocumentImpl(ctx context.Context, cli protocol.Client, url protocol.URI, rangeOpt *protocol.Range, opts *settings.Options) {
	if !opts.ShowDocumentSupported {
		return // no op
	}
	// In principle we shouldn't send a showDocument request to a
	// client that doesn't support it, as reported by
	// ShowDocumentClientCapabilities. But even clients that do
	// support it may defer the real work of opening the document
	// asynchronously, to avoid deadlocks due to rentrancy.
	//
	// For example: client sends request to server; server sends
	// showDocument to client; client opens editor; editor causes
	// new RPC to be sent to server, which is still busy with
	// previous request. (This happens in eglot.)
	//
	// So we can't rely on the success/failure information.
	// That's the reason this function doesn't return an error.

	// "External" means run the system-wide handler (e.g. open(1)
	// on macOS or xdg-open(1) on Linux) for this URL, ignoring
	// TakeFocus and Selection. Note that this may still end up
	// opening the same editor (e.g. VSCode) for a file: URL.
	res, err := cli.ShowDocument(ctx, &protocol.ShowDocumentParams{
		URI:       url,
		External:  rangeOpt == nil,
		TakeFocus: true,
		Selection: rangeOpt, // optional
	})
	if err != nil {
		event.Error(ctx, "client.showDocument: %v", err)
	} else if res != nil && !res.Success {
		event.Log(ctx, fmt.Sprintf("client declined to open document %v", url))
	}
}

func (c *commandHandler) ChangeSignature(ctx context.Context, args command.ChangeSignatureArgs) (*protocol.WorkspaceEdit, error) {
	var result *protocol.WorkspaceEdit
	err := c.run(ctx, commandConfig{
		forURI: args.Location.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		pkg, pgf, err := golang.NarrowestPackageForFile(ctx, deps.snapshot, args.Location.URI)
		if err != nil {
			return err
		}

		// For now, gopls only supports parameter permutation or removal.
		var perm []int
		for _, newParam := range args.NewParams {
			if newParam.NewField != "" {
				return fmt.Errorf("adding new parameters is currently unsupported")
			}
			perm = append(perm, newParam.OldIndex)
		}

		docedits, err := golang.ChangeSignature(ctx, deps.snapshot, pkg, pgf, args.Location.Range, perm)
		if err != nil {
			return err
		}
		wsedit := protocol.NewWorkspaceEdit(docedits...)
		if args.ResolveEdits {
			result = wsedit
			return nil
		}
		return applyChanges(ctx, c.s.client, docedits)
	})
	return result, err
}

func (c *commandHandler) DiagnoseFiles(ctx context.Context, args command.DiagnoseFilesArgs) error {
	return c.run(ctx, commandConfig{
		progress: "Diagnose files",
	}, func(ctx context.Context, _ commandDeps) error {

		// TODO(rfindley): even better would be textDocument/diagnostics (golang/go#60122).
		// Though note that implementing pull diagnostics may cause some servers to
		// request diagnostics in an ad-hoc manner, and break our intentional pacing.

		ctx, done := event.Start(ctx, "lsp.server.DiagnoseFiles")
		defer done()

		snapshots := make(map[*cache.Snapshot]bool)
		for _, uri := range args.Files {
			fh, snapshot, release, err := c.s.fileOf(ctx, uri)
			if err != nil {
				return err
			}
			if snapshots[snapshot] || snapshot.FileKind(fh) != file.Go {
				release()
				continue
			}
			defer release()
			snapshots[snapshot] = true
		}

		var wg sync.WaitGroup
		for snapshot := range snapshots {
			snapshot := snapshot
			wg.Add(1)
			go func() {
				defer wg.Done()

				// Use the operation context for diagnosis, rather than
				// snapshot.BackgroundContext, because this operation does not create
				// new snapshots (so they should also be diagnosed by other means).
				c.s.diagnoseSnapshot(ctx, snapshot, nil, 0)
			}()
		}
		wg.Wait()

		return nil
	})
}

func (c *commandHandler) Views(ctx context.Context) ([]command.View, error) {
	var summaries []command.View
	for _, view := range c.s.session.Views() {
		summaries = append(summaries, command.View{
			ID:         view.ID(),
			Type:       view.Type().String(),
			Root:       view.Root(),
			Folder:     view.Folder().Dir,
			EnvOverlay: view.EnvOverlay(),
		})
	}
	return summaries, nil
}

func (c *commandHandler) FreeSymbols(ctx context.Context, viewID string, loc protocol.Location) error {
	web, err := c.s.getWeb()
	if err != nil {
		return err
	}
	url := web.freesymbolsURL(viewID, loc)
	openClientBrowser(ctx, c.s.client, "Free symbols", url, c.s.Options())
	return nil
}

func (c *commandHandler) Assembly(ctx context.Context, viewID, packageID, symbol string) error {
	web, err := c.s.getWeb()
	if err != nil {
		return err
	}
	url := web.assemblyURL(viewID, packageID, symbol)
	openClientBrowser(ctx, c.s.client, "Assembly", url, c.s.Options())
	return nil
}

func (c *commandHandler) ClientOpenURL(ctx context.Context, url string) error {
	// Fall back to "Gopls: open your browser..." if we must send a showMessage
	// request, since we don't know the context of this command.
	openClientBrowser(ctx, c.s.client, "Gopls", url, c.s.Options())
	return nil
}

func (c *commandHandler) ScanImports(ctx context.Context) error {
	for _, v := range c.s.session.Views() {
		v.ScanImports()
	}
	return nil
}

func (c *commandHandler) PackageSymbols(ctx context.Context, args command.PackageSymbolsArgs) (command.PackageSymbolsResult, error) {
	var result command.PackageSymbolsResult
	err := c.run(ctx, commandConfig{
		forURI: args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		if deps.snapshot.FileKind(deps.fh) != file.Go {
			// golang/vscode-go#3681: fail silently, to avoid spurious error popups.
			return nil
		}
		res, err := golang.PackageSymbols(ctx, deps.snapshot, args.URI)
		if err != nil {
			return err
		}
		result = res
		return nil
	})

	// sort symbols for determinism
	sort.SliceStable(result.Symbols, func(i, j int) bool {
		iv, jv := result.Symbols[i], result.Symbols[j]
		if iv.Name == jv.Name {
			return iv.Range.Start.Line < jv.Range.Start.Line
		}
		return iv.Name < jv.Name
	})

	return result, err
}
