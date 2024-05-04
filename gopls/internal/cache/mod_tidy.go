// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/label"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/internal/diff"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/gocommand"
	"golang.org/x/tools/internal/memoize"
)

// This error is sought by mod diagnostics.
var ErrNoModOnDisk = errors.New("go.mod file is not on disk")

// A TidiedModule contains the results of running `go mod tidy` on a module.
type TidiedModule struct {
	// Diagnostics representing changes made by `go mod tidy`.
	Diagnostics []*Diagnostic
	// The bytes of the go.mod file after it was tidied.
	TidiedContent []byte
}

// ModTidy returns the go.mod file that would be obtained by running
// "go mod tidy". Concurrent requests are combined into a single command.
func (s *Snapshot) ModTidy(ctx context.Context, pm *ParsedModule) (*TidiedModule, error) {
	ctx, done := event.Start(ctx, "cache.snapshot.ModTidy")
	defer done()

	uri := pm.URI
	if pm.File == nil {
		return nil, fmt.Errorf("cannot tidy unparseable go.mod file: %v", uri)
	}

	s.mu.Lock()
	entry, hit := s.modTidyHandles.Get(uri)
	s.mu.Unlock()

	type modTidyResult struct {
		tidied *TidiedModule
		err    error
	}

	// Cache miss?
	if !hit {
		// If the file handle is an overlay, it may not be written to disk.
		// The go.mod file has to be on disk for `go mod tidy` to work.
		// TODO(rfindley): is this still true with Go 1.16 overlay support?
		fh, err := s.ReadFile(ctx, pm.URI)
		if err != nil {
			return nil, err
		}
		if _, ok := fh.(*overlay); ok {
			if info, _ := os.Stat(uri.Path()); info == nil {
				return nil, ErrNoModOnDisk
			}
		}

		if err := s.awaitLoaded(ctx); err != nil {
			return nil, err
		}

		handle := memoize.NewPromise("modTidy", func(ctx context.Context, arg interface{}) interface{} {
			tidied, err := modTidyImpl(ctx, arg.(*Snapshot), pm)
			return modTidyResult{tidied, err}
		})

		entry = handle
		s.mu.Lock()
		s.modTidyHandles.Set(uri, entry, nil)
		s.mu.Unlock()
	}

	// Await result.
	v, err := s.awaitPromise(ctx, entry)
	if err != nil {
		return nil, err
	}
	res := v.(modTidyResult)
	return res.tidied, res.err
}

// modTidyImpl runs "go mod tidy" on a go.mod file.
func modTidyImpl(ctx context.Context, snapshot *Snapshot, pm *ParsedModule) (*TidiedModule, error) {
	ctx, done := event.Start(ctx, "cache.ModTidy", label.URI.Of(pm.URI))
	defer done()

	tempDir, cleanup, err := TempModDir(ctx, snapshot, pm.URI)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	inv, cleanupInvocation, err := snapshot.GoCommandInvocation(false, &gocommand.Invocation{
		Verb:       "mod",
		Args:       []string{"tidy", "-modfile=" + filepath.Join(tempDir, "go.mod")},
		Env:        []string{"GOWORK=off"},
		WorkingDir: pm.URI.Dir().Path(),
	})
	if err != nil {
		return nil, err
	}
	defer cleanupInvocation()
	if _, err := snapshot.view.gocmdRunner.Run(ctx, *inv); err != nil {
		return nil, err
	}

	// Go directly to disk to get the temporary mod file,
	// since it is always on disk.
	tempMod := filepath.Join(tempDir, "go.mod")
	tempContents, err := os.ReadFile(tempMod)
	if err != nil {
		return nil, err
	}
	ideal, err := modfile.Parse(tempMod, tempContents, nil)
	if err != nil {
		// We do not need to worry about the temporary file's parse errors
		// since it has been "tidied".
		return nil, err
	}

	// Compare the original and tidied go.mod files to compute errors and
	// suggested fixes.
	diagnostics, err := modTidyDiagnostics(ctx, snapshot, pm, ideal)
	if err != nil {
		return nil, err
	}

	return &TidiedModule{
		Diagnostics:   diagnostics,
		TidiedContent: tempContents,
	}, nil
}

// modTidyDiagnostics computes the differences between the original and tidied
// go.mod files to produce diagnostic and suggested fixes. Some diagnostics
// may appear on the Go files that import packages from missing modules.
func modTidyDiagnostics(ctx context.Context, snapshot *Snapshot, pm *ParsedModule, ideal *modfile.File) (diagnostics []*Diagnostic, err error) {
	// First, determine which modules are unused and which are missing from the
	// original go.mod file.
	var (
		unused          = make(map[string]*modfile.Require, len(pm.File.Require))
		missing         = make(map[string]*modfile.Require, len(ideal.Require))
		wrongDirectness = make(map[string]*modfile.Require, len(pm.File.Require))
	)
	for _, req := range pm.File.Require {
		unused[req.Mod.Path] = req
	}
	for _, req := range ideal.Require {
		origReq := unused[req.Mod.Path]
		if origReq == nil {
			missing[req.Mod.Path] = req
			continue
		} else if origReq.Indirect != req.Indirect {
			wrongDirectness[req.Mod.Path] = origReq
		}
		delete(unused, req.Mod.Path)
	}
	for _, req := range wrongDirectness {
		// Handle dependencies that are incorrectly labeled indirect and
		// vice versa.
		srcDiag, err := directnessDiagnostic(pm.Mapper, req)
		if err != nil {
			// We're probably in a bad state if we can't compute a
			// directnessDiagnostic, but try to keep going so as to not suppress
			// other, valid diagnostics.
			event.Error(ctx, "computing directness diagnostic", err)
			continue
		}
		diagnostics = append(diagnostics, srcDiag)
	}
	// Next, compute any diagnostics for modules that are missing from the
	// go.mod file. The fixes will be for the go.mod file, but the
	// diagnostics should also appear in both the go.mod file and the import
	// statements in the Go files in which the dependencies are used.
	// Finally, add errors for any unused dependencies.
	if len(missing) > 0 {
		missingModuleDiagnostics, err := missingModuleDiagnostics(ctx, snapshot, pm, ideal, missing)
		if err != nil {
			return nil, err
		}
		diagnostics = append(diagnostics, missingModuleDiagnostics...)
	}

	// Opt: if this is the only diagnostic, we can avoid textual edits and just
	// run the Go command.
	//
	// See also the documentation for command.RemoveDependencyArgs.OnlyDiagnostic.
	onlyDiagnostic := len(diagnostics) == 0 && len(unused) == 1
	for _, req := range unused {
		srcErr, err := unusedDiagnostic(pm.Mapper, req, onlyDiagnostic)
		if err != nil {
			return nil, err
		}
		diagnostics = append(diagnostics, srcErr)
	}
	return diagnostics, nil
}

func missingModuleDiagnostics(ctx context.Context, snapshot *Snapshot, pm *ParsedModule, ideal *modfile.File, missing map[string]*modfile.Require) ([]*Diagnostic, error) {
	missingModuleFixes := map[*modfile.Require][]SuggestedFix{}
	var diagnostics []*Diagnostic
	for _, req := range missing {
		srcDiag, err := missingModuleDiagnostic(pm, req)
		if err != nil {
			return nil, err
		}
		missingModuleFixes[req] = srcDiag.SuggestedFixes
		diagnostics = append(diagnostics, srcDiag)
	}

	// Add diagnostics for missing modules anywhere they are imported in the
	// workspace.
	metas, err := snapshot.WorkspaceMetadata(ctx)
	if err != nil {
		return nil, err
	}
	// TODO(adonovan): opt: opportunities for parallelism abound.
	for _, mp := range metas {
		// Read both lists of files of this package.
		//
		// Parallelism is not necessary here as the files will have already been
		// pre-read at load time.
		goFiles, err := readFiles(ctx, snapshot, mp.GoFiles)
		if err != nil {
			return nil, err
		}
		compiledGoFiles, err := readFiles(ctx, snapshot, mp.CompiledGoFiles)
		if err != nil {
			return nil, err
		}

		missingImports := map[string]*modfile.Require{}

		// If -mod=readonly is not set we may have successfully imported
		// packages from missing modules. Otherwise they'll be in
		// MissingDependencies. Combine both.
		imps, err := parseImports(ctx, snapshot, goFiles)
		if err != nil {
			return nil, err
		}
		for imp := range imps {
			if req, ok := missing[imp]; ok {
				missingImports[imp] = req
				break
			}
			// If the import is a package of the dependency, then add the
			// package to the map, this will eliminate the need to do this
			// prefix package search on each import for each file.
			// Example:
			//
			// import (
			//   "golang.org/x/tools/go/expect"
			//   "golang.org/x/tools/go/packages"
			// )
			// They both are related to the same module: "golang.org/x/tools".
			var match string
			for _, req := range ideal.Require {
				if strings.HasPrefix(imp, req.Mod.Path) && len(req.Mod.Path) > len(match) {
					match = req.Mod.Path
				}
			}
			if req, ok := missing[match]; ok {
				missingImports[imp] = req
			}
		}
		// None of this package's imports are from missing modules.
		if len(missingImports) == 0 {
			continue
		}
		for _, goFile := range compiledGoFiles {
			pgf, err := snapshot.ParseGo(ctx, goFile, parsego.Header)
			if err != nil {
				continue
			}
			file, m := pgf.File, pgf.Mapper
			if file == nil || m == nil {
				continue
			}
			imports := make(map[string]*ast.ImportSpec)
			for _, imp := range file.Imports {
				if imp.Path == nil {
					continue
				}
				if target, err := strconv.Unquote(imp.Path.Value); err == nil {
					imports[target] = imp
				}
			}
			if len(imports) == 0 {
				continue
			}
			for importPath, req := range missingImports {
				imp, ok := imports[importPath]
				if !ok {
					continue
				}
				fixes, ok := missingModuleFixes[req]
				if !ok {
					return nil, fmt.Errorf("no missing module fix for %q (%q)", importPath, req.Mod.Path)
				}
				srcErr, err := missingModuleForImport(pgf, imp, req, fixes)
				if err != nil {
					return nil, err
				}
				diagnostics = append(diagnostics, srcErr)
			}
		}
	}
	return diagnostics, nil
}

// unusedDiagnostic returns a Diagnostic for an unused require.
func unusedDiagnostic(m *protocol.Mapper, req *modfile.Require, onlyDiagnostic bool) (*Diagnostic, error) {
	rng, err := m.OffsetRange(req.Syntax.Start.Byte, req.Syntax.End.Byte)
	if err != nil {
		return nil, err
	}
	title := fmt.Sprintf("Remove dependency: %s", req.Mod.Path)
	cmd, err := command.NewRemoveDependencyCommand(title, command.RemoveDependencyArgs{
		URI:            m.URI,
		OnlyDiagnostic: onlyDiagnostic,
		ModulePath:     req.Mod.Path,
	})
	if err != nil {
		return nil, err
	}
	return &Diagnostic{
		URI:            m.URI,
		Range:          rng,
		Severity:       protocol.SeverityWarning,
		Source:         ModTidyError,
		Message:        fmt.Sprintf("%s is not used in this module", req.Mod.Path),
		SuggestedFixes: []SuggestedFix{SuggestedFixFromCommand(cmd, protocol.QuickFix)},
	}, nil
}

// directnessDiagnostic extracts errors when a dependency is labeled indirect when
// it should be direct and vice versa.
func directnessDiagnostic(m *protocol.Mapper, req *modfile.Require) (*Diagnostic, error) {
	rng, err := m.OffsetRange(req.Syntax.Start.Byte, req.Syntax.End.Byte)
	if err != nil {
		return nil, err
	}
	direction := "indirect"
	if req.Indirect {
		direction = "direct"

		// If the dependency should be direct, just highlight the // indirect.
		if comments := req.Syntax.Comment(); comments != nil && len(comments.Suffix) > 0 {
			end := comments.Suffix[0].Start
			end.LineRune += len(comments.Suffix[0].Token)
			end.Byte += len(comments.Suffix[0].Token)
			rng, err = m.OffsetRange(comments.Suffix[0].Start.Byte, end.Byte)
			if err != nil {
				return nil, err
			}
		}
	}
	// If the dependency should be indirect, add the // indirect.
	edits, err := switchDirectness(req, m)
	if err != nil {
		return nil, err
	}
	return &Diagnostic{
		URI:      m.URI,
		Range:    rng,
		Severity: protocol.SeverityWarning,
		Source:   ModTidyError,
		Message:  fmt.Sprintf("%s should be %s", req.Mod.Path, direction),
		SuggestedFixes: []SuggestedFix{{
			Title: fmt.Sprintf("Change %s to %s", req.Mod.Path, direction),
			Edits: map[protocol.DocumentURI][]protocol.TextEdit{
				m.URI: edits,
			},
			ActionKind: protocol.QuickFix,
		}},
	}, nil
}

func missingModuleDiagnostic(pm *ParsedModule, req *modfile.Require) (*Diagnostic, error) {
	var rng protocol.Range
	// Default to the start of the file if there is no module declaration.
	if pm.File != nil && pm.File.Module != nil && pm.File.Module.Syntax != nil {
		start, end := pm.File.Module.Syntax.Span()
		var err error
		rng, err = pm.Mapper.OffsetRange(start.Byte, end.Byte)
		if err != nil {
			return nil, err
		}
	}
	title := fmt.Sprintf("Add %s to your go.mod file", req.Mod.Path)
	cmd, err := command.NewAddDependencyCommand(title, command.DependencyArgs{
		URI:        pm.Mapper.URI,
		AddRequire: !req.Indirect,
		GoCmdArgs:  []string{req.Mod.Path + "@" + req.Mod.Version},
	})
	if err != nil {
		return nil, err
	}
	return &Diagnostic{
		URI:            pm.Mapper.URI,
		Range:          rng,
		Severity:       protocol.SeverityError,
		Source:         ModTidyError,
		Message:        fmt.Sprintf("%s is not in your go.mod file", req.Mod.Path),
		SuggestedFixes: []SuggestedFix{SuggestedFixFromCommand(cmd, protocol.QuickFix)},
	}, nil
}

// switchDirectness gets the edits needed to change an indirect dependency to
// direct and vice versa.
func switchDirectness(req *modfile.Require, m *protocol.Mapper) ([]protocol.TextEdit, error) {
	// We need a private copy of the parsed go.mod file, since we're going to
	// modify it.
	copied, err := modfile.Parse("", m.Content, nil)
	if err != nil {
		return nil, err
	}
	// Change the directness in the matching require statement. To avoid
	// reordering the require statements, rewrite all of them.
	var requires []*modfile.Require
	seenVersions := make(map[string]string)
	for _, r := range copied.Require {
		if seen := seenVersions[r.Mod.Path]; seen != "" && seen != r.Mod.Version {
			// Avoid a panic in SetRequire below, which panics on conflicting
			// versions.
			return nil, fmt.Errorf("%q has conflicting versions: %q and %q", r.Mod.Path, seen, r.Mod.Version)
		}
		seenVersions[r.Mod.Path] = r.Mod.Version
		if r.Mod.Path == req.Mod.Path {
			requires = append(requires, &modfile.Require{
				Mod:      r.Mod,
				Syntax:   r.Syntax,
				Indirect: !r.Indirect,
			})
			continue
		}
		requires = append(requires, r)
	}
	copied.SetRequire(requires)
	newContent, err := copied.Format()
	if err != nil {
		return nil, err
	}
	// Calculate the edits to be made due to the change.
	edits := diff.Bytes(m.Content, newContent)
	return protocol.EditsFromDiffEdits(m, edits)
}

// missingModuleForImport creates an error for a given import path that comes
// from a missing module.
func missingModuleForImport(pgf *parsego.File, imp *ast.ImportSpec, req *modfile.Require, fixes []SuggestedFix) (*Diagnostic, error) {
	if req.Syntax == nil {
		return nil, fmt.Errorf("no syntax for %v", req)
	}
	rng, err := pgf.NodeRange(imp.Path)
	if err != nil {
		return nil, err
	}
	return &Diagnostic{
		URI:            pgf.URI,
		Range:          rng,
		Severity:       protocol.SeverityError,
		Source:         ModTidyError,
		Message:        fmt.Sprintf("%s is not in your go.mod file", req.Mod.Path),
		SuggestedFixes: fixes,
	}, nil
}

// parseImports parses the headers of the specified files and returns
// the set of strings that appear in import declarations within
// GoFiles. Errors are ignored.
//
// (We can't simply use Metadata.Imports because it is based on
// CompiledGoFiles, after cgo processing.)
//
// TODO(rfindley): this should key off ImportPath.
func parseImports(ctx context.Context, s *Snapshot, files []file.Handle) (map[string]bool, error) {
	pgfs, err := s.view.parseCache.parseFiles(ctx, token.NewFileSet(), parsego.Header, false, files...)
	if err != nil { // e.g. context cancellation
		return nil, err
	}

	seen := make(map[string]bool)
	for _, pgf := range pgfs {
		for _, spec := range pgf.File.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			seen[path] = true
		}
	}
	return seen, nil
}
