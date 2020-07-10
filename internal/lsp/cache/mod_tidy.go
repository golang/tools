// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cache

import (
	"context"
	"fmt"
	"io/ioutil"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/lsp/debug/tag"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/memoize"
	"golang.org/x/tools/internal/packagesinternal"
	"golang.org/x/tools/internal/span"
)

type modTidyKey struct {
	sessionID       string
	cfg             string
	gomod           string
	imports         string
	unsavedOverlays string
	view            string
}

type modTidyHandle struct {
	handle *memoize.Handle

	pmh source.ParseModHandle
}

type modTidyData struct {
	memoize.NoCopy

	// missingDeps contains dependencies that should be added to the view's
	// go.mod file.
	missingDeps map[string]*modfile.Require

	// diagnostics are any errors and associated suggested fixes for
	// the go.mod file.
	diagnostics []source.Error

	err error
}

func (mth *modTidyHandle) Tidy(ctx context.Context) (map[string]*modfile.Require, []source.Error, error) {
	v, err := mth.handle.Get(ctx)
	if err != nil {
		return nil, nil, err
	}
	data := v.(*modTidyData)
	return data.missingDeps, data.diagnostics, data.err
}

func (s *snapshot) ModTidyHandle(ctx context.Context) (source.ModTidyHandle, error) {
	if !s.view.tmpMod {
		return nil, source.ErrTmpModfileUnsupported
	}
	if handle := s.getModTidyHandle(); handle != nil {
		return handle, nil
	}
	fh, err := s.GetFile(ctx, s.view.modURI)
	if err != nil {
		return nil, err
	}
	pmh, err := s.ParseModHandle(ctx, fh)
	if err != nil {
		return nil, err
	}
	wsPackages, err := s.WorkspacePackages(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	imports, err := hashImports(ctx, wsPackages)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	overlayHash := hashUnsavedOverlays(s.files)
	s.mu.Unlock()

	var (
		folder  = s.View().Folder()
		modURI  = s.view.modURI
		cfg     = s.config(ctx)
		options = s.view.Options()
	)
	key := modTidyKey{
		sessionID:       s.view.session.id,
		view:            folder.Filename(),
		imports:         imports,
		unsavedOverlays: overlayHash,
		gomod:           pmh.Mod().Identity().String(),
		cfg:             hashConfig(cfg),
	}
	h := s.view.session.cache.store.Bind(key, func(ctx context.Context) interface{} {
		ctx, done := event.Start(ctx, "cache.ModTidyHandle", tag.URI.Of(modURI))
		defer done()

		original, m, parseErrors, err := pmh.Parse(ctx)
		if err != nil || len(parseErrors) > 0 {
			return &modTidyData{
				diagnostics: parseErrors,
				err:         err,
			}
		}
		tmpURI, inv, cleanup, err := goCommandInvocation(ctx, cfg, pmh, "mod", []string{"tidy"})
		if err != nil {
			return &modTidyData{err: err}
		}
		// Keep the temporary go.mod file around long enough to parse it.
		defer cleanup()

		if _, err := packagesinternal.GetGoCmdRunner(cfg).Run(ctx, *inv); err != nil {
			return &modTidyData{err: err}
		}
		// Go directly to disk to get the temporary mod file, since it is
		// always on disk.
		tempContents, err := ioutil.ReadFile(tmpURI.Filename())
		if err != nil {
			return &modTidyData{err: err}
		}
		ideal, err := modfile.Parse(tmpURI.Filename(), tempContents, nil)
		if err != nil {
			// We do not need to worry about the temporary file's parse errors
			// since it has been "tidied".
			return &modTidyData{err: err}
		}
		// Get the dependencies that are different between the original and
		// ideal go.mod files.
		unusedDeps := make(map[string]*modfile.Require, len(original.Require))
		missingDeps := make(map[string]*modfile.Require, len(ideal.Require))
		for _, req := range original.Require {
			unusedDeps[req.Mod.Path] = req
		}
		for _, req := range ideal.Require {
			origDep := unusedDeps[req.Mod.Path]
			if origDep != nil && origDep.Indirect == req.Indirect {
				delete(unusedDeps, req.Mod.Path)
			} else {
				missingDeps[req.Mod.Path] = req
			}
		}
		diagnostics, err := modRequireErrors(pmh.Mod().URI(), m, missingDeps, unusedDeps, options)
		if err != nil {
			return &modTidyData{err: err}
		}
		for _, req := range missingDeps {
			if unusedDeps[req.Mod.Path] != nil {
				delete(missingDeps, req.Mod.Path)
			}
		}
		return &modTidyData{
			missingDeps: missingDeps,
			diagnostics: diagnostics,
		}
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modTidyHandle = &modTidyHandle{
		handle: h,
		pmh:    pmh,
	}
	return s.modTidyHandle, nil
}

// modRequireErrors extracts the errors that occur on the require directives.
// It checks for directness issues and unused dependencies.
func modRequireErrors(uri span.URI, m *protocol.ColumnMapper, missingDeps, unusedDeps map[string]*modfile.Require, options source.Options) ([]source.Error, error) {
	var errors []source.Error
	for dep, req := range unusedDeps {
		if req.Syntax == nil {
			continue
		}
		// Handle dependencies that are incorrectly labeled indirect and vice versa.
		if missingDeps[dep] != nil && req.Indirect != missingDeps[dep].Indirect {
			directErr, err := modDirectnessErrors(uri, m, req, options)
			if err != nil {
				return nil, err
			}
			errors = append(errors, directErr)
		}
		// Handle unused dependencies.
		if missingDeps[dep] == nil {
			rng, err := rangeFromPositions(uri, m, req.Syntax.Start, req.Syntax.End)
			if err != nil {
				return nil, err
			}
			edits, err := dropDependencyEdits(uri, m, req, options)
			if err != nil {
				return nil, err
			}
			errors = append(errors, source.Error{
				Category: ModTidyError,
				Message:  fmt.Sprintf("%s is not used in this module.", dep),
				Range:    rng,
				URI:      uri,
				SuggestedFixes: []source.SuggestedFix{{
					Title: fmt.Sprintf("Remove dependency: %s", dep),
					Edits: map[span.URI][]protocol.TextEdit{
						uri: edits,
					},
				}},
			})
		}
	}
	return errors, nil
}

const ModTidyError = "go mod tidy"

// modDirectnessErrors extracts errors when a dependency is labeled indirect when it should be direct and vice versa.
func modDirectnessErrors(uri span.URI, m *protocol.ColumnMapper, req *modfile.Require, options source.Options) (source.Error, error) {
	rng, err := rangeFromPositions(uri, m, req.Syntax.Start, req.Syntax.End)
	if err != nil {
		return source.Error{}, err
	}
	if req.Indirect {
		// If the dependency should be direct, just highlight the // indirect.
		if comments := req.Syntax.Comment(); comments != nil && len(comments.Suffix) > 0 {
			end := comments.Suffix[0].Start
			end.LineRune += len(comments.Suffix[0].Token)
			end.Byte += len([]byte(comments.Suffix[0].Token))
			rng, err = rangeFromPositions(uri, m, comments.Suffix[0].Start, end)
			if err != nil {
				return source.Error{}, err
			}
		}
		edits, err := changeDirectnessEdits(uri, m, req, false, options)
		if err != nil {
			return source.Error{}, err
		}
		return source.Error{
			Category: ModTidyError,
			Message:  fmt.Sprintf("%s should be a direct dependency.", req.Mod.Path),
			Range:    rng,
			URI:      uri,
			SuggestedFixes: []source.SuggestedFix{{
				Title: fmt.Sprintf("Make %s direct", req.Mod.Path),
				Edits: map[span.URI][]protocol.TextEdit{
					uri: edits,
				},
			}},
		}, nil
	}
	// If the dependency should be indirect, add the // indirect.
	edits, err := changeDirectnessEdits(uri, m, req, true, options)
	if err != nil {
		return source.Error{}, err
	}
	return source.Error{
		Category: ModTidyError,
		Message:  fmt.Sprintf("%s should be an indirect dependency.", req.Mod.Path),
		Range:    rng,
		URI:      uri,
		SuggestedFixes: []source.SuggestedFix{{
			Title: fmt.Sprintf("Make %s indirect", req.Mod.Path),
			Edits: map[span.URI][]protocol.TextEdit{
				uri: edits,
			},
		}},
	}, nil
}

// dropDependencyEdits gets the edits needed to remove the dependency from the go.mod file.
// As an example, this function will codify the edits needed to convert the before go.mod file to the after.
// Before:
// 	module t
//
// 	go 1.11
//
// 	require golang.org/x/mod v0.1.1-0.20191105210325-c90efee705ee
// After:
// 	module t
//
// 	go 1.11
func dropDependencyEdits(uri span.URI, m *protocol.ColumnMapper, req *modfile.Require, options source.Options) ([]protocol.TextEdit, error) {
	// We need a private copy of the parsed go.mod file, since we're going to
	// modify it.
	copied, err := modfile.Parse("", m.Content, nil)
	if err != nil {
		return nil, err
	}
	if err := copied.DropRequire(req.Mod.Path); err != nil {
		return nil, err
	}
	copied.Cleanup()
	newContent, err := copied.Format()
	if err != nil {
		return nil, err
	}
	// Calculate the edits to be made due to the change.
	diff := options.ComputeEdits(uri, string(m.Content), string(newContent))
	edits, err := source.ToProtocolEdits(m, diff)
	if err != nil {
		return nil, err
	}
	return edits, nil
}

// changeDirectnessEdits gets the edits needed to change an indirect dependency to direct and vice versa.
// As an example, this function will codify the edits needed to convert the before go.mod file to the after.
// Before:
// 	module t
//
// 	go 1.11
//
// 	require golang.org/x/mod v0.1.1-0.20191105210325-c90efee705ee
// After:
// 	module t
//
// 	go 1.11
//
// 	require golang.org/x/mod v0.1.1-0.20191105210325-c90efee705ee // indirect
func changeDirectnessEdits(uri span.URI, m *protocol.ColumnMapper, req *modfile.Require, indirect bool, options source.Options) ([]protocol.TextEdit, error) {
	// We need a private copy of the parsed go.mod file, since we're going to
	// modify it.
	copied, err := modfile.Parse("", m.Content, nil)
	if err != nil {
		return nil, err
	}
	// Change the directness in the matching require statement. To avoid
	// reordering the require statements, rewrite all of them.
	var requires []*modfile.Require
	for _, r := range copied.Require {
		if r.Mod.Path == req.Mod.Path {
			requires = append(requires, &modfile.Require{
				Mod:      r.Mod,
				Syntax:   r.Syntax,
				Indirect: indirect,
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
	diff := options.ComputeEdits(uri, string(m.Content), string(newContent))
	edits, err := source.ToProtocolEdits(m, diff)
	if err != nil {
		return nil, err
	}
	return edits, nil
}

func rangeFromPositions(uri span.URI, m *protocol.ColumnMapper, s, e modfile.Position) (protocol.Range, error) {
	toPoint := func(offset int) (span.Point, error) {
		l, c, err := m.Converter.ToPosition(offset)
		if err != nil {
			return span.Point{}, err
		}
		return span.NewPoint(l, c, offset), nil
	}
	start, err := toPoint(s.Byte)
	if err != nil {
		return protocol.Range{}, err
	}
	end, err := toPoint(e.Byte)
	if err != nil {
		return protocol.Range{}, err
	}
	return m.Range(span.New(uri, start, end))
}
