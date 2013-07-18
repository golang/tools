// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package godoc

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type SearchResult struct {
	Query string
	Alert string // error or warning message

	// identifier matches
	Pak HitList       // packages matching Query
	Hit *LookupResult // identifier matches of Query
	Alt *AltWords     // alternative identifiers to look for

	// textual matches
	Found    int         // number of textual occurrences found
	Textual  []FileLines // textual matches of Query
	Complete bool        // true if all textual occurrences of Query are reported
}

func (c *Corpus) Lookup(query string) SearchResult {
	var result SearchResult
	result.Query = query

	index, timestamp := c.CurrentIndex()
	if index != nil {
		// identifier search
		var err error
		result.Pak, result.Hit, result.Alt, err = index.Lookup(query)
		if err != nil && c.MaxResults <= 0 {
			// ignore the error if full text search is enabled
			// since the query may be a valid regular expression
			result.Alert = "Error in query string: " + err.Error()
			return result
		}

		// full text search
		if c.MaxResults > 0 && query != "" {
			rx, err := regexp.Compile(query)
			if err != nil {
				result.Alert = "Error in query regular expression: " + err.Error()
				return result
			}
			// If we get maxResults+1 results we know that there are more than
			// maxResults results and thus the result may be incomplete (to be
			// precise, we should remove one result from the result set, but
			// nobody is going to count the results on the result page).
			result.Found, result.Textual = index.LookupRegexp(rx, c.MaxResults+1)
			result.Complete = result.Found <= c.MaxResults
			if !result.Complete {
				result.Found-- // since we looked for maxResults+1
			}
		}
	}

	// is the result accurate?
	if c.IndexEnabled {
		if ts := c.FSModifiedTime(); timestamp.Before(ts) {
			// The index is older than the latest file system change under godoc's observation.
			result.Alert = "Indexing in progress: result may be inaccurate"
		}
	} else {
		result.Alert = "Search index disabled: no results available"
	}

	return result
}

func (p *Presentation) HandleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.FormValue("q"))
	result := p.Corpus.Lookup(query)

	if GetPageInfoMode(r)&NoHTML != 0 {
		p.ServeText(w, applyTemplate(p.SearchText, "searchText", result))
		return
	}

	var title string
	if result.Hit != nil || len(result.Textual) > 0 {
		title = fmt.Sprintf(`Results for query %q`, query)
	} else {
		title = fmt.Sprintf(`No results found for query %q`, query)
	}

	p.ServePage(w, Page{
		Title:    title,
		Tabtitle: query,
		Query:    query,
		Body:     applyTemplate(p.SearchHTML, "searchHTML", result),
	})
}
