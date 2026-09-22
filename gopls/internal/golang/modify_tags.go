// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"slices"
	"strings"
	"unicode"

	"github.com/fatih/gomodifytags/modifytags"
	"golang.org/x/tools/gopls/internal/cache"
	"golang.org/x/tools/gopls/internal/cache/parsego"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
	"golang.org/x/tools/gopls/internal/settings"
	"golang.org/x/tools/gopls/internal/util/cursorutil"
	"golang.org/x/tools/gopls/internal/util/tokeninternal"
	internalastutil "golang.org/x/tools/internal/astutil"
	"golang.org/x/tools/internal/diff"
)

// addTagsForm asks which struct tags to add, and how to derive their values
// from the field names.
var addTagsForm = []protocol.FormField{
	{
		ID:          "tags",
		Description: `comma-separated list of tags to add; e.g.. "json,xml"`,
		Type:        protocol.FormFieldTypeString{Kind: protocol.FormFieldKindString},
		Required:    true,
		Default:     "json",
	},
	{
		ID:          "transform",
		Description: `transform rule for added tags, e.g., "camelcase' or 'snakecase"`,
		Type: protocol.FormFieldTypeEnum{
			Kind: protocol.FormFieldKindEnum,
			Entries: []protocol.FormEnumEntry{
				{
					Value:       "camelcase",
					Description: "camelCase",
				},
				{
					Value:       "lispcase",
					Description: "lisp-case",
				},
				{
					Value:       "pascalcase",
					Description: "PascalCase",
				},
				{
					Value:       "titlecase",
					Description: "Title Case",
				},
				{
					Value:       "snakecase",
					Description: "snake_case",
				},
			},
		},
		Required: true,
		Default:  "camelcase",
	},
}

// removeTagsForm asks which struct tags to remove.
var removeTagsForm = []protocol.FormField{
	{
		ID:          "tags",
		Description: `comma-separated list of tags to remove; e.g., "json,xml"`,
		Type:        protocol.FormFieldTypeString{Kind: protocol.FormFieldKindString},
		Required:    true,
		Default:     "json", // TODO(?): put the existing tags here?
	},
}

func resolveModifyTags(options settings.ClientOptions, param *protocol.ExecuteCommandParams) error {
	var a0 command.ModifyTagsArgs
	if err := command.UnmarshalArgs(param.Arguments, &a0); err != nil {
		return err
	}
	switch a0.Modification {
	case "add":
		if !supportsDialog(options, addTagsForm) {
			return nil
		}

		// First call, return the form.
		if len(param.FormAnswers) == 0 {
			param.FormFields = addTagsForm
			return nil
		}

		v0, err := param.RequiredAnswer[string]("tags")
		if err != nil {
			return err
		}

		if _, err = SanitizeTags(v0); err != nil {
			form := slices.Clone(addTagsForm)
			form[0].Error = err.Error()
			param.FormFields = form
			return nil
		}

		if _, err = param.RequiredAnswer[string]("transform"); err != nil {
			return err
		}
		// PJW: what happens when the user enters a bad value? (i think the client handles it)

		param.FormFields = nil
		return nil
	case "remove":
		if !supportsDialog(options, removeTagsForm) {
			return nil
		}

		// First call, return the form
		if len(param.FormAnswers) == 0 {
			// TODO? show the user the current list of tags?
			param.FormFields = removeTagsForm
			return nil
		}

		v, err := param.RequiredAnswer[string]("tags")
		if err != nil {
			return err
		}
		if _, err := SanitizeTags(v); err != nil {
			form := slices.Clone(addTagsForm)
			form[0].Error = err.Error()
			param.FormFields = form
			return nil
		}

		param.FormFields = nil
		return nil
	default:
		return fmt.Errorf("unsupported modify tags operation: %s", a0.Modification)
	}
}

// SanitizeTags cleans up comma-separated tags and ensures they are valid.
func SanitizeTags(tags string) (string, error) {
	parts := strings.Split(tags, ",")
	var clean []string

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		// Use strings.ContainsFunc instead of a manual byte loop.
		// It returns true if any rune in the string matches the condition.
		if strings.ContainsFunc(p, func(r rune) bool {
			// Space, colon, quote, or any non-printable character (like control chars)
			return r == ' ' || r == ':' || r == '"' || !unicode.IsPrint(r)
		}) {
			return "", fmt.Errorf("illegal tag %q: cannot contain spaces, quotes, colons, or control characters", p)
		}

		clean = append(clean, p)
	}

	return strings.Join(clean, ","), nil
}

// ModifyTags applies the given struct tag modifications to the specified struct.
func ModifyTags(ctx context.Context, snapshot *cache.Snapshot, fh file.Handle, args command.ModifyTagsArgs, m *modifytags.Modification) ([]protocol.DocumentChange, error) {
	pgf, err := snapshot.ParseGo(ctx, fh, parsego.Full)
	if err != nil {
		return nil, fmt.Errorf("error fetching package file: %v", err)
	}
	start, end, err := pgf.RangePos(args.Range)
	if err != nil {
		return nil, fmt.Errorf("error getting position information: %v", err)
	}
	// If the cursor is at a point and not a selection, we should use the entire enclosing struct.
	if start == end {
		cur, ok := pgf.Cursor().FindByPos(start, end)
		if !ok {
			return nil, fmt.Errorf("error finding start and end positions: %v", err)
		}
		structnode, _ := cursorutil.FirstEnclosing[*ast.StructType](cur)
		if structnode == nil {
			return nil, fmt.Errorf("no enclosing struct type")
		}
		start, end = structnode.Pos(), structnode.End()
	}

	// Create a copy of the file node in order to avoid race conditions when we modify the node in Apply.
	cloned := internalastutil.CloneNode(pgf.File)
	fset := tokeninternal.FileSetFor(pgf.Tok)

	if err = m.Apply(fset, cloned, start, end); err != nil {
		return nil, fmt.Errorf("could not modify tags: %v", err)
	}

	// Construct a list of DocumentChanges based on the diff between the formatted node and the
	// original file content.
	var after bytes.Buffer
	if err := format.Node(&after, fset, cloned); err != nil {
		return nil, err
	}
	edits := diff.Bytes(pgf.Src, after.Bytes())
	if len(edits) == 0 {
		return nil, nil
	}
	textedits, err := protocol.EditsFromDiffEdits(pgf.Mapper, edits)
	if err != nil {
		return nil, fmt.Errorf("error computing edits for %s: %v", args.URI, err)
	}
	return []protocol.DocumentChange{
		protocol.DocumentChangeEdit(fh, textedits),
	}, nil
}
