// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The stacks command finds all gopls stack traces reported by
// telemetry in the past 7 days, and reports their associated GitHub
// issue, creating new issues as needed.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"io"

	"golang.org/x/telemetry"
	"golang.org/x/tools/gopls/internal/util/browser"
)

// flags
var (
	daysFlag = flag.Int("days", 7, "number of previous days of telemetry data to read")
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("stacks: ")
	flag.Parse()

	// Maps stack text to Version/GoVersion/GOOS/GOARCH string to counter.
	stacks := make(map[string]map[string]int64)
	var total int

	// Maps stack to a telemetry URL.
	stackToURL := make(map[string]string)

	// Read all recent telemetry reports.
	t := time.Now()
	for i := 0; i < *daysFlag; i++ {
		const DateOnly = "2006-01-02" // TODO(adonovan): use time.DateOnly in go1.20.
		date := t.Add(-time.Duration(i+1) * 24 * time.Hour).Format(DateOnly)

		url := fmt.Sprintf("https://storage.googleapis.com/prod-telemetry-merged/%s.json", date)
		resp, err := http.Get(url)
		if err != nil {
			log.Fatalf("can't GET %s: %v", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Fatalf("GET %s returned %d %s", url, resp.StatusCode, resp.Status)
		}

		dec := json.NewDecoder(resp.Body)
		for {
			var report telemetry.Report
			if err := dec.Decode(&report); err != nil {
				if err == io.EOF {
					break
				}
				log.Fatal(err)
			}
			for _, prog := range report.Programs {
				if prog.Program == "golang.org/x/tools/gopls" && len(prog.Stacks) > 0 {
					total++

					// Ignore @devel versions as they correspond to
					// ephemeral (and often numerous) variations of
					// the program as we work on a fix to a bug.
					if prog.Version == "devel" {
						continue
					}
					info := fmt.Sprintf("%s@%s %s %s/%s",
						prog.Program, prog.Version,
						prog.GoVersion, prog.GOOS, prog.GOARCH)
					for stack, count := range prog.Stacks {
						counts := stacks[stack]
						if counts == nil {
							counts = make(map[string]int64)
							stacks[stack] = counts
						}
						counts[info] += count
						stackToURL[stack] = url
					}
				}
			}
		}
	}

	// Compute IDs of all stacks.
	var stackIDs []string
	for stack := range stacks {
		stackIDs = append(stackIDs, stackID(stack))
	}

	// Query GitHub for existing GitHub issues.
	issuesByStackID := make(map[string]*Issue)
	for len(stackIDs) > 0 {
		// For some reason GitHub returns 422 UnprocessableEntity
		// if we attempt to read more than 6 at once.
		batch := stackIDs[:min(6, len(stackIDs))]
		stackIDs = stackIDs[len(batch):]

		query := "label:gopls/telemetry-wins in:body " + strings.Join(batch, " OR ")
		res, err := searchIssues(query)
		if err != nil {
			log.Fatalf("GitHub issues query failed: %v", err)
		}
		for _, issue := range res.Items {
			for _, id := range batch {
				// Matching is a little fuzzy here
				// but base64 will rarely produce
				// words that appear in the body
				// by chance.
				if strings.Contains(issue.Body, id) {
					issuesByStackID[id] = issue
				}
			}
		}
	}

	fmt.Printf("Found %d stacks in last %v days:\n", total, *daysFlag)

	// For each stack, show existing issue or create a new one.
	for stack, counts := range stacks {
		id := stackID(stack)

		// Existing issue?
		issue, ok := issuesByStackID[id]
		if ok {
			if issue != nil {
				fmt.Printf("#%d: %s [%s]\n",
					issue.Number, issue.Title, issue.State)
			} else {
				// We just created a "New issue" browser tab
				// for this stackID.
				issuesByStackID[id] = nil // suppress dups
			}
			continue
		}

		// Create new issue.
		issuesByStackID[id] = nil // suppress dups

		// Use a heuristic to find a suitable symbol to blame
		// in the title: the first public function or method
		// of a public type, in gopls, to appear in the stack
		// trace. We can always refine it later.
		var symbol string
		for _, line := range strings.Split(stack, "\n") {
			// Look for:
			//   gopls/.../pkg.Func
			//   gopls/.../pkg.Type.method
			//   gopls/.../pkg.(*Type).method
			if strings.Contains(line, "internal/util/bug.") {
				continue // not interesting
			}
			if _, rest, ok := strings.Cut(line, "golang.org/x/tools/gopls/"); ok {
				if i := strings.IndexByte(rest, '.'); i >= 0 {
					rest = rest[i+1:]
					rest = strings.TrimPrefix(rest, "(*")
					if rest != "" && 'A' <= rest[0] && rest[0] <= 'Z' {
						rest, _, _ = strings.Cut(rest, ":")
						symbol = " " + rest
						break
					}
				}
			}
		}

		// Populate the form (title, body, label)
		title := fmt.Sprintf("x/tools/gopls:%s bug reported by telemetry", symbol)
		body := new(bytes.Buffer)
		fmt.Fprintf(body, "This stack `%s` was [reported by telemetry](%s):\n\n",
			id, stackToURL[stack])
		fmt.Fprintf(body, "```\n%s\n```\n", stack)

		// Add counts, gopls version, and platform info.
		// This isn't very precise but should provide clues.
		//
		// TODO(adonovan): link each stack (ideally each frame) to source:
		// https://cs.opensource.google/go/x/tools/+/gopls/VERSION:gopls/FILE;l=LINE
		// (Requires parsing stack, shallow-cloning gopls module at that tag, and
		// computing correct line offsets. Would be labor-saving though.)
		fmt.Fprintf(body, "```\n")
		for info, count := range counts {
			fmt.Fprintf(body, "%s (%d)\n", info, count)
		}
		fmt.Fprintf(body, "```\n\n")

		fmt.Fprintf(body, "Issue created by golang.org/x/tools/gopls/internal/telemetry/cmd/stacks.\n")

		const labels = "gopls,Tools,gopls/telemetry-wins,NeedsInvestigation"

		// Report it.
		if !browser.Open("https://github.com/golang/go/issues/new?labels=" + labels + "&title=" + url.QueryEscape(title) + "&body=" + url.QueryEscape(body.String())) {
			log.Print("Please file a new issue at golang.org/issue/new using this template:\n\n")
			log.Printf("Title: %s\n", title)
			log.Printf("Labels: %s\n", labels)
			log.Printf("Body: %s\n", body)
		}
	}
}

// stackID returns a 32-bit identifier for a stack
// suitable for use in GitHub issue titles.
func stackID(stack string) string {
	// Encode it using base64 (6 bytes) for brevity,
	// as a single issue's body might contain multiple IDs
	// if separate issues with same cause wre manually de-duped,
	// e.g. "AAAAAA, BBBBBB"
	//
	// https://hbfs.wordpress.com/2012/03/30/finding-collisions:
	// the chance of a collision is 1 - exp(-n(n-1)/2d) where n
	// is the number of items and d is the number of distinct values.
	// So, even with n=10^4 telemetry-reported stacks each identified
	// by a uint32 (d=2^32), we have a 1% chance of a collision,
	// which is plenty good enough.
	h := fnv.New32()
	io.WriteString(h, stack)
	return base64.URLEncoding.EncodeToString(h.Sum(nil))[:6]
}

// -- GitHub search --

// searchIssues queries the GitHub issue tracker.
func searchIssues(query string) (*IssuesSearchResult, error) {
	q := url.QueryEscape(query)
	resp, err := http.Get(IssuesURL + "?q=" + q)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("search query failed: %s", resp.Status)
	}
	var result IssuesSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		resp.Body.Close()
		return nil, err
	}
	resp.Body.Close()
	return &result, nil
}

// See https://developer.github.com/v3/search/#search-issues.

const IssuesURL = "https://api.github.com/search/issues"

type IssuesSearchResult struct {
	TotalCount int `json:"total_count"`
	Items      []*Issue
}

type Issue struct {
	Number    int
	HTMLURL   string `json:"html_url"`
	Title     string
	State     string
	User      *User
	CreatedAt time.Time `json:"created_at"`
	Body      string    // in Markdown format
}

type User struct {
	Login   string
	HTMLURL string `json:"html_url"`
}

// -- helpers --

func min(x, y int) int {
	if x < y {
		return x
	} else {
		return y
	}
}
