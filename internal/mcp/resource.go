// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	jsonrpc2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/mcp/internal/util"
)

// A ServerResource associates a Resource with its handler.
type ServerResource struct {
	Resource *Resource
	Handler  ResourceHandler
}

// A ServerResourceTemplate associates a ResourceTemplate with its handler.
type ServerResourceTemplate struct {
	ResourceTemplate *ResourceTemplate
	Handler          ResourceHandler
}

// A ResourceHandler is a function that reads a resource.
// It will be called when the client calls [ClientSession.ReadResource].
// If it cannot find the resource, it should return the result of calling [ResourceNotFoundError].
type ResourceHandler func(context.Context, *ServerSession, *ReadResourceParams) (*ReadResourceResult, error)

// ResourceNotFoundError returns an error indicating that a resource being read could
// not be found.
func ResourceNotFoundError(uri string) error {
	return &jsonrpc2.WireError{
		Code:    CodeResourceNotFound,
		Message: "Resource not found",
		Data:    json.RawMessage(fmt.Sprintf(`{"uri":%q}`, uri)),
	}
}

// readFileResource reads from the filesystem at a URI relative to dirFilepath, respecting
// the roots.
// dirFilepath and rootFilepaths are absolute filesystem paths.
func readFileResource(rawURI, dirFilepath string, rootFilepaths []string) ([]byte, error) {
	uriFilepath, err := computeURIFilepath(rawURI, dirFilepath, rootFilepaths)
	if err != nil {
		return nil, err
	}

	var data []byte
	err = withFile(dirFilepath, uriFilepath, func(f *os.File) error {
		var err error
		data, err = io.ReadAll(f)
		return err
	})
	if os.IsNotExist(err) {
		err = ResourceNotFoundError(rawURI)
	}
	return data, err
}

// computeURIFilepath returns a path relative to dirFilepath.
// The dirFilepath and rootFilepaths are absolute file paths.
func computeURIFilepath(rawURI, dirFilepath string, rootFilepaths []string) (string, error) {
	// We use "file path" to mean a filesystem path.
	uri, err := url.Parse(rawURI)
	if err != nil {
		return "", err
	}
	if uri.Scheme != "file" {
		return "", fmt.Errorf("URI is not a file: %s", uri)
	}
	if uri.Path == "" {
		// A more specific error than the one below, to catch the
		// common mistake "file://foo".
		return "", errors.New("empty path")
	}
	// The URI's path is interpreted relative to dirFilepath, and in the local filesystem.
	// It must not try to escape its directory.
	uriFilepathRel, err := filepath.Localize(strings.TrimPrefix(uri.Path, "/"))
	if err != nil {
		return "", fmt.Errorf("%q cannot be localized: %w", uriFilepathRel, err)
	}

	// Check roots, if there are any.
	if len(rootFilepaths) > 0 {
		// To check against the roots, we need an absolute file path, not relative to the directory.
		// uriFilepath is local, so the joined path is under dirFilepath.
		uriFilepathAbs := filepath.Join(dirFilepath, uriFilepathRel)
		rootOK := false
		// Check that the requested file path is under some root.
		// Since both paths are absolute, that's equivalent to filepath.Rel constructing
		// a local path.
		for _, rootFilepathAbs := range rootFilepaths {
			if rel, err := filepath.Rel(rootFilepathAbs, uriFilepathAbs); err == nil && filepath.IsLocal(rel) {
				rootOK = true
				break
			}
		}
		if !rootOK {
			return "", fmt.Errorf("URI path %q is not under any root", uriFilepathAbs)
		}
	}
	return uriFilepathRel, nil
}

// fileRoots transforms the Roots obtained from the client into absolute paths on
// the local filesystem.
// TODO(jba): expose this functionality to user ResourceHandlers,
// so they don't have to repeat it.
func fileRoots(rawRoots []*Root) ([]string, error) {
	var fileRoots []string
	for _, r := range rawRoots {
		fr, err := fileRoot(r)
		if err != nil {
			return nil, err
		}
		fileRoots = append(fileRoots, fr)
	}
	return fileRoots, nil
}

// fileRoot returns the absolute path for Root.
func fileRoot(root *Root) (_ string, err error) {
	defer util.Wrapf(&err, "root %q", root.URI)

	// Convert to absolute file path.
	rurl, err := url.Parse(root.URI)
	if err != nil {
		return "", err
	}
	if rurl.Scheme != "file" {
		return "", errors.New("not a file URI")
	}
	if rurl.Path == "" {
		// A more specific error than the one below, to catch the
		// common mistake "file://foo".
		return "", errors.New("empty path")
	}
	// We don't want Localize here: we want an absolute path, which is not local.
	fileRoot := filepath.Clean(filepath.FromSlash(rurl.Path))
	if !filepath.IsAbs(fileRoot) {
		return "", errors.New("not an absolute path")
	}
	return fileRoot, nil
}

// Matches reports whether the receiver's uri template matches the uri.
// TODO: use "github.com/yosida95/uritemplate/v3"
func (sr *ServerResourceTemplate) Matches(uri string) bool {
	re, err := uriTemplateToRegexp(sr.ResourceTemplate.URITemplate)
	if err != nil {
		return false
	}
	return re.MatchString(uri)
}

func uriTemplateToRegexp(uriTemplate string) (*regexp.Regexp, error) {
	pat := uriTemplate
	var b strings.Builder
	b.WriteByte('^')
	seen := map[string]bool{}
	for len(pat) > 0 {
		literal, rest, ok := strings.Cut(pat, "{")
		b.WriteString(regexp.QuoteMeta(literal))
		if !ok {
			break
		}
		expr, rest, ok := strings.Cut(rest, "}")
		if !ok {
			return nil, errors.New("missing '}'")
		}
		pat = rest
		if strings.ContainsRune(expr, ',') {
			return nil, errors.New("can't handle commas in expressions")
		}
		if strings.ContainsRune(expr, ':') {
			return nil, errors.New("can't handle prefix modifiers in expressions")
		}
		if len(expr) > 0 && expr[len(expr)-1] == '*' {
			return nil, errors.New("can't handle explode modifiers in expressions")
		}

		// These sets of valid characters aren't accurate.
		// See https://datatracker.ietf.org/doc/html/rfc6570.
		var re, name string
		first := byte(0)
		if len(expr) > 0 {
			first = expr[0]
		}
		switch first {
		default:
			// {var} doesn't match slashes. (It should also fail to match other characters,
			// but this simplified implementation doesn't handle that.)
			re = `[^/]*`
			name = expr
		case '+':
			// {+var} matches anything, even slashes
			re = `.*`
			name = expr[1:]
		case '#', '.', '/', ';', '?', '&':
			return nil, fmt.Errorf("prefix character %c unsupported", first)
		}
		if seen[name] {
			return nil, fmt.Errorf("can't handle duplicate name %q", name)
		}
		seen[name] = true
		b.WriteString(re)
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}
