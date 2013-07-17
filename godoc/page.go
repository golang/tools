// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package godoc

import (
	"log"
	"net/http"
	"runtime"
	"text/template"
)

// Page describes the contents of the top-level godoc webpage.
type Page struct {
	Title    string
	Tabtitle string
	Subtitle string
	Query    string
	Body     []byte

	// filled in by servePage
	SearchBox  bool
	Playground bool
	Version    string
}

var (
	DirlistHTML,
	ErrorHTML,
	ExampleHTML,
	GodocHTML,
	PackageHTML,
	PackageText,
	SearchHTML,
	SearchText,
	SearchDescXML *template.Template
)

func ServePage(w http.ResponseWriter, page Page) {
	if page.Tabtitle == "" {
		page.Tabtitle = page.Title
	}
	page.SearchBox = IndexEnabled
	page.Playground = ShowPlayground
	page.Version = runtime.Version()
	if err := GodocHTML.Execute(w, page); err != nil && err != http.ErrBodyNotAllowed {
		// Only log if there's an error that's not about writing on HEAD requests.
		// See Issues 5451 and 5454.
		log.Printf("GodocHTML.Execute: %s", err)
	}
}

func ServeError(w http.ResponseWriter, r *http.Request, relpath string, err error) {
	w.WriteHeader(http.StatusNotFound)
	ServePage(w, Page{
		Title:    "File " + relpath,
		Subtitle: relpath,
		Body:     applyTemplate(ErrorHTML, "errorHTML", err), // err may contain an absolute path!
	})
}
