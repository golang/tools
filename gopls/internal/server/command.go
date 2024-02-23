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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
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
	"golang.org/x/tools/gopls/internal/telemetry"
	"golang.org/x/tools/gopls/internal/util/bug"
	"golang.org/x/tools/gopls/internal/vulncheck"
	"golang.org/x/tools/gopls/internal/vulncheck/scan"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/tokeninternal"
	"golang.org/x/tools/internal/xcontext"
)

func (s *server) ExecuteCommand(ctx context.Context, params *protocol.ExecuteCommandParams) (interface{}, error) {
	ctx, done := event.Start(ctx, "lsp.Server.executeCommand")
	defer done()

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

func (h *commandHandler) MaybePromptForTelemetry(ctx context.Context) error {
	go h.s.maybePromptForTelemetry(ctx, true)
	return nil
}

func (*commandHandler) AddTelemetryCounters(_ context.Context, args command.AddTelemetryCountersArgs) error {
	if len(args.Names) != len(args.Values) {
		return fmt.Errorf("Names and Values must have the same length")
	}
	// invalid counter update requests will be silently dropped. (no audience)
	telemetry.AddForwardedCounters(args.Names, args.Values)
	return nil
}

// commandConfig configures common command set-up and execution.
type commandConfig struct {
	// TODO(adonovan): whether a command is synchronous or
	// asynchronous is part of the server interface contract, not
	// a mere implementation detail of the handler.
	// Export a (command.Command).IsAsync() property so that
	// clients can tell. (The tricky part is ensuring the handler
	// remains consistent with the command.Command metadata, as at
	// the point were we read the 'async' field below, we no
	// longer know that command.Command.)

	async       bool                 // whether to run the command asynchronously. Async commands can only return errors.
	requireSave bool                 // whether all files must be saved for the command to work
	progress    string               // title to use for progress reporting. If empty, no progress will be reported.
	forView     string               // view to resolve to a snapshot; incompatible with forURI
	forURI      protocol.DocumentURI // URI to resolve to a snapshot. If unset, snapshot will be nil.
}

// commandDeps is evaluated from a commandConfig. Note that not all fields may
// be populated, depending on which configuration is set. See comments in-line
// for details.
type commandDeps struct {
	snapshot *cache.Snapshot    // present if cfg.forURI was set
	fh       file.Handle        // present if cfg.forURI was set
	work     *progress.WorkDone // present cfg.progress was set
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
		deps.work = c.s.progress.Start(ctx, cfg.progress, "Running...", c.params.WorkDoneToken, cancel)
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
	if cfg.async {
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
		forURI: args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		edits, err := golang.ApplyFix(ctx, args.Fix, deps.snapshot, deps.fh, args.Range)
		if err != nil {
			return err
		}
		changes := []protocol.DocumentChanges{} // must be a slice
		for _, edit := range edits {
			edit := edit
			changes = append(changes, protocol.DocumentChanges{
				TextDocumentEdit: &edit,
			})
		}
		edit := protocol.WorkspaceEdit{
			DocumentChanges: changes,
		}
		if args.ResolveEdits {
			result = &edit
			return nil
		}
		r, err := c.s.client.ApplyEdit(ctx, &protocol.ApplyWorkspaceEditParams{
			Edit: edit,
		})
		if err != nil {
			return err
		}
		if !r.Applied {
			return errors.New(r.FailureReason)
		}
		return nil
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
		c.s.diagnoseSnapshot(snapshot, nil, 0)
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
		requireSave: true,
		progress:    "Running go mod tidy",
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
		requireSave: true,
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
		err := deps.snapshot.RunGoCommandPiped(ctx, cache.Normal|cache.AllowNetwork, &gocommand.Invocation{
			Verb:       "mod",
			Args:       []string{"vendor"},
			WorkingDir: filepath.Dir(args.URI.Path()),
		}, &bytes.Buffer{}, stderr)
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
		response, err := c.s.client.ApplyEdit(ctx, &protocol.ApplyWorkspaceEditParams{
			Edit: protocol.WorkspaceEdit{
				DocumentChanges: documentChanges(deps.fh, edits),
			},
		})
		if err != nil {
			return err
		}
		if !response.Applied {
			return fmt.Errorf("edits not applied because of %s", response.FailureReason)
		}
		return nil
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

func (c *commandHandler) Test(ctx context.Context, uri protocol.DocumentURI, tests, benchmarks []string) error {
	return c.RunTests(ctx, command.RunTestsArgs{
		URI:        uri,
		Tests:      tests,
		Benchmarks: benchmarks,
	})
}

func (c *commandHandler) RunTests(ctx context.Context, args command.RunTestsArgs) error {
	return c.run(ctx, commandConfig{
		async:       true,
		progress:    "Running go test",
		requireSave: true,
		forURI:      args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
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
		inv := &gocommand.Invocation{
			Verb:       "test",
			Args:       []string{pkgPath, "-v", "-count=1", fmt.Sprintf("-run=^%s$", regexp.QuoteMeta(funcName))},
			WorkingDir: filepath.Dir(uri.Path()),
		}
		if err := snapshot.RunGoCommandPiped(ctx, cache.Normal, inv, out, out); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			failedTests++
		}
	}

	// Run `go test -run=^$ -bench Func` on each test.
	var failedBenchmarks int
	for _, funcName := range benchmarks {
		inv := &gocommand.Invocation{
			Verb:       "test",
			Args:       []string{pkgPath, "-v", "-run=^$", fmt.Sprintf("-bench=^%s$", regexp.QuoteMeta(funcName))},
			WorkingDir: filepath.Dir(uri.Path()),
		}
		if err := snapshot.RunGoCommandPiped(ctx, cache.Normal, inv, out, out); err != nil {
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
		requireSave: true,
		progress:    title,
		forURI:      args.Dir,
	}, func(ctx context.Context, deps commandDeps) error {
		er := progress.NewEventWriter(ctx, "generate")

		pattern := "."
		if args.Recursive {
			pattern = "./..."
		}
		inv := &gocommand.Invocation{
			Verb:       "generate",
			Args:       []string{"-x", pattern},
			WorkingDir: args.Dir.Path(),
		}
		stderr := io.MultiWriter(er, progress.NewWorkDoneWriter(ctx, deps.work))
		if err := deps.snapshot.RunGoCommandPiped(ctx, cache.AllowNetwork, inv, er, stderr); err != nil {
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
		// Run on a throwaway go.mod, otherwise it'll write to the real one.
		stdout, err := deps.snapshot.RunGoCommandDirect(ctx, cache.WriteTemporaryModFile|cache.AllowNetwork, &gocommand.Invocation{
			Verb:       "list",
			Args:       []string{"-f", "{{.Module.Path}}@{{.Module.Version}}", args.Pkg},
			WorkingDir: filepath.Dir(args.URI.Path()),
		})
		if err != nil {
			return err
		}
		ver := strings.TrimSpace(stdout.String())
		return c.s.runGoModUpdateCommands(ctx, deps.snapshot, args.URI, func(invoke func(...string) (*bytes.Buffer, error)) error {
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
	newModBytes, newSumBytes, err := snapshot.RunGoModUpdateCommands(ctx, filepath.Dir(uri.Path()), run)
	if err != nil {
		return err
	}
	modURI := snapshot.GoModForFile(uri)
	sumURI := protocol.URIFromPath(strings.TrimSuffix(modURI.Path(), ".mod") + ".sum")
	modEdits, err := collectFileEdits(ctx, snapshot, modURI, newModBytes)
	if err != nil {
		return err
	}
	sumEdits, err := collectFileEdits(ctx, snapshot, sumURI, newSumBytes)
	if err != nil {
		return err
	}
	return applyFileEdits(ctx, s.client, append(sumEdits, modEdits...))
}

// collectFileEdits collects any file edits required to transform the snapshot
// file specified by uri to the provided new content.
//
// If the file is not open, collectFileEdits simply writes the new content to
// disk.
//
// TODO(rfindley): fix this API asymmetry. It should be up to the caller to
// write the file or apply the edits.
func collectFileEdits(ctx context.Context, snapshot *cache.Snapshot, uri protocol.DocumentURI, newContent []byte) ([]protocol.TextDocumentEdit, error) {
	fh, err := snapshot.ReadFile(ctx, uri)
	if err != nil {
		return nil, err
	}
	oldContent, err := fh.Content()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	if bytes.Equal(oldContent, newContent) {
		return nil, nil
	}

	// Sending a workspace edit to a closed file causes VS Code to open the
	// file and leave it unsaved. We would rather apply the changes directly,
	// especially to go.sum, which should be mostly invisible to the user.
	if !snapshot.IsOpen(uri) {
		err := os.WriteFile(uri.Path(), newContent, 0666)
		return nil, err
	}

	m := protocol.NewMapper(fh.URI(), oldContent)
	diff := diff.Bytes(oldContent, newContent)
	edits, err := protocol.EditsFromDiffEdits(m, diff)
	if err != nil {
		return nil, err
	}
	return []protocol.TextDocumentEdit{{
		TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
			Version: fh.Version(),
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{
				URI: uri,
			},
		},
		Edits: protocol.AsAnnotatedTextEdits(edits),
	}}, nil
}

func applyFileEdits(ctx context.Context, cli protocol.Client, edits []protocol.TextDocumentEdit) error {
	if len(edits) == 0 {
		return nil
	}
	response, err := cli.ApplyEdit(ctx, &protocol.ApplyWorkspaceEditParams{
		Edit: protocol.WorkspaceEdit{
			DocumentChanges: protocol.TextDocumentEditsToDocumentChanges(edits),
		},
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
	stdout, err := snapshot.RunGoCommandDirect(ctx, cache.Normal|cache.AllowNetwork, &gocommand.Invocation{
		Verb:       "list",
		Args:       append([]string{"-m", "-u", "-json"}, modules...),
		WorkingDir: filepath.Dir(uri.Path()),
	})
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
	return c.ToggleGCDetails(ctx, command.URIArg{URI: uri})
}

func (c *commandHandler) ToggleGCDetails(ctx context.Context, args command.URIArg) error {
	return c.run(ctx, commandConfig{
		requireSave: true,
		progress:    "Toggling GC Details",
		forURI:      args.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		return c.modifyState(ctx, FromToggleGCDetails, func() (*cache.Snapshot, func(), error) {
			meta, err := golang.NarrowestMetadataForFile(ctx, deps.snapshot, deps.fh.URI())
			if err != nil {
				return nil, nil, err
			}
			wantDetails := !deps.snapshot.WantGCDetails(meta.ID) // toggle the gc details state
			return c.s.session.InvalidateView(ctx, deps.snapshot.View(), cache.StateChange{
				GCDetails: map[metadata.PackageID]bool{
					meta.ID: wantDetails,
				},
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
		if _, err := c.s.client.ApplyEdit(ctx, &protocol.ApplyWorkspaceEditParams{
			Edit: protocol.WorkspaceEdit{
				DocumentChanges: documentChanges(deps.fh, edits),
			},
		}); err != nil {
			return fmt.Errorf("could not apply import edits: %v", err)
		}
		return nil
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
	openClientBrowser(ctx, c.s.client, result.URLs[0])
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
		async:       true, // need to be async to be cancellable
		progress:    "govulncheck",
		requireSave: true,
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
		c.s.diagnoseSnapshot(snapshot, nil, 0)

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
func openClientBrowser(ctx context.Context, cli protocol.Client, url protocol.URI) {
	showDocumentImpl(ctx, cli, url, nil)
}

// openClientEditor causes the LSP client to open the specified document
// and select the indicated range.
func openClientEditor(ctx context.Context, cli protocol.Client, loc protocol.Location) {
	showDocumentImpl(ctx, cli, protocol.URI(loc.URI), &loc.Range)
}

func showDocumentImpl(ctx context.Context, cli protocol.Client, url protocol.URI, rangeOpt *protocol.Range) {
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
		forURI: args.RemoveParameter.URI,
	}, func(ctx context.Context, deps commandDeps) error {
		// For now, gopls only supports removing unused parameters.
		changes, err := golang.RemoveUnusedParameter(ctx, deps.fh, args.RemoveParameter.Range, deps.snapshot)
		if err != nil {
			return err
		}
		edit := protocol.WorkspaceEdit{
			DocumentChanges: changes,
		}
		if args.ResolveEdits {
			result = &edit
			return nil
		}
		r, err := c.s.client.ApplyEdit(ctx, &protocol.ApplyWorkspaceEditParams{
			Edit: edit,
		})
		if !r.Applied {
			return fmt.Errorf("failed to apply edits: %v", r.FailureReason)
		}

		return nil
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
				c.s.diagnoseSnapshot(snapshot, nil, 0)
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
			Type:       view.Type().String(),
			Root:       view.Root(),
			Folder:     view.Folder().Dir,
			EnvOverlay: view.EnvOverlay(),
		})
	}
	return summaries, nil
}
