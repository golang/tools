// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The stacks command finds all gopls stack traces reported by
// telemetry in the past 7 days, and reports their associated GitHub
// issue, creating new issues as needed.
package main

// TODO(adonovan): create a proper package with tests. Much of this
// machinery might find wider use in other x/telemetry clients.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/telemetry"
	"golang.org/x/tools/gopls/internal/util/browser"
	"golang.org/x/tools/gopls/internal/util/moremaps"
)

// flags
var (
	daysFlag = flag.Int("days", 7, "number of previous days of telemetry data to read")

	token string // optional GitHub authentication token, to relax the rate limit
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("stacks: ")
	flag.Parse()

	// Read GitHub authentication token from $HOME/.stacks.token.
	//
	// You can create one using the flow at: GitHub > You > Settings >
	// Developer Settings > Personal Access Tokens > Fine-grained tokens >
	// Generate New Token.  Generate the token on behalf of yourself
	// (not "golang" or "google"), with no special permissions.
	// The token is typically of the form "github_pat_XXX", with 82 hex digits.
	// Save it in the file, with mode 0400.
	//
	// For security, secret tokens should be read from files, not
	// command-line flags or environment variables.
	{
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}
		tokenFile := filepath.Join(home, ".stacks.token")
		content, err := os.ReadFile(tokenFile)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Fatalf("cannot read GitHub authentication token: %v", err)
			}
			log.Printf("no file %s containing GitHub authentication token; continuing without authentication, which is subject to stricter rate limits (https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api).", tokenFile)
		}
		token = string(bytes.TrimSpace(content))
	}

	// Maps stack text to Info to count.
	stacks := make(map[string]map[Info]int64)
	var distinctStacks int

	// Maps stack to a telemetry URL.
	stackToURL := make(map[string]string)

	// Read all recent telemetry reports.
	t := time.Now()
	for i := 0; i < *daysFlag; i++ {
		date := t.Add(-time.Duration(i+1) * 24 * time.Hour).Format(time.DateOnly)

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
					// Include applicable client names (e.g. vscode, eglot).
					var clients []string
					var clientSuffix string
					for key := range prog.Counters {
						client := strings.TrimPrefix(key, "gopls/client:")
						if client != key {
							clients = append(clients, client)
						}
					}
					sort.Strings(clients)
					if len(clients) > 0 {
						clientSuffix = strings.Join(clients, ",")
					}

					// Ignore @devel versions as they correspond to
					// ephemeral (and often numerous) variations of
					// the program as we work on a fix to a bug.
					if prog.Version == "devel" {
						continue
					}

					distinctStacks++

					info := Info{
						Program:   prog.Program,
						Version:   prog.Version,
						GoVersion: prog.GoVersion,
						GOOS:      prog.GOOS,
						GOARCH:    prog.GOARCH,
						Client:    clientSuffix,
					}
					for stack, count := range prog.Stacks {
						counts := stacks[stack]
						if counts == nil {
							counts = make(map[Info]int64)
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
	// (Note: there may be multiple Issue records
	// for the same logical issue, i.e. Issue.Number.)
	issuesByStackID := make(map[string]*Issue)
	for len(stackIDs) > 0 {
		// For some reason GitHub returns 422 UnprocessableEntity
		// if we attempt to read more than 6 at once.
		batch := stackIDs[:min(6, len(stackIDs))]
		stackIDs = stackIDs[len(batch):]

		query := "is:issue label:gopls/telemetry-wins in:body " + strings.Join(batch, " OR ")
		res, err := searchIssues(query)
		if err != nil {
			log.Fatalf("GitHub issues query %q failed: %v", query, err)
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

	// For each stack, show existing issue or create a new one.
	// Aggregate stack IDs by issue summary.
	var (
		// Both vars map the summary line to the stack count.
		existingIssues = make(map[string]int64)
		newIssues      = make(map[string]int64)
	)
	for stack, counts := range stacks {
		id := stackID(stack)

		var info0 Info // an arbitrary Info for this stack
		var total int64
		for info, count := range counts {
			info0 = info
			total += count
		}

		if issue, ok := issuesByStackID[id]; ok {
			// existing issue
			// TODO(adonovan): use ANSI tty color codes for Issue.State.
			summary := fmt.Sprintf("#%d: %s [%s]",
				issue.Number, issue.Title, issue.State)
			existingIssues[summary] += total
		} else {
			// new issue
			title := newIssue(stack, id, info0, stackToURL[stack], counts)
			summary := fmt.Sprintf("%s: %s [%s]", id, title, "new")
			newIssues[summary] += total
		}
	}

	fmt.Printf("Found %d distinct stacks in last %v days:\n", distinctStacks, *daysFlag)
	print := func(caption string, issues map[string]int64) {
		// Print items in descending frequency.
		keys := moremaps.KeySlice(issues)
		sort.Slice(keys, func(i, j int) bool {
			return issues[keys[i]] > issues[keys[j]]
		})
		fmt.Printf("%s issues:\n", caption)
		for _, summary := range keys {
			count := issues[summary]
			// TODO(adonovan): use ANSI tty codes to show high n values in bold.
			fmt.Printf("%s (n=%d)\n", summary, count)
		}
	}
	print("Existing", existingIssues)
	print("New", newIssues)
}

type Info struct {
	Program            string // "golang.org/x/tools/gopls"
	Version, GoVersion string // e.g. "gopls/v0.16.1", "go1.23"
	GOOS, GOARCH       string
	Client             string // e.g. "vscode"
}

func (info Info) String() string {
	return fmt.Sprintf("%s@%s %s %s/%s %s",
		info.Program, info.Version,
		info.GoVersion, info.GOOS, info.GOARCH,
		info.Client)
}

// stackID returns a 32-bit identifier for a stack
// suitable for use in GitHub issue titles.
func stackID(stack string) string {
	// Encode it using base64 (6 bytes) for brevity,
	// as a single issue's body might contain multiple IDs
	// if separate issues with same cause were manually de-duped,
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

// newIssue creates a browser tab with a populated GitHub "New issue"
// form for the specified stack. (The triage person is expected to
// manually de-dup the issue before deciding whether to submit the form.)
//
// It returns the title.
func newIssue(stack, id string, info Info, jsonURL string, counts map[Info]int64) string {
	// Use a heuristic to find a suitable symbol to blame
	// in the title: the first public function or method
	// of a public type, in gopls, to appear in the stack
	// trace. We can always refine it later.
	//
	// TODO(adonovan): include in the issue a source snippet ±5
	// lines around the PC in this symbol.
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
	title := fmt.Sprintf("x/tools/gopls: bug in %s", symbol)
	body := new(bytes.Buffer)
	fmt.Fprintf(body, "This stack `%s` was [reported by telemetry](%s):\n\n",
		id, jsonURL)

	// Read the mapping from symbols to file/line.
	pclntab, err := readPCLineTable(info)
	if err != nil {
		log.Fatal(err)
	}

	// Parse the stack and get the symbol names out.
	for _, line := range strings.Split(stack, "\n") {
		// e.g. "golang.org/x/tools/gopls/foo.(*Type).Method.inlined.func3:+5"
		symbol, offset, ok := strings.Cut(line, ":")
		if !ok {
			// Not a symbol (perhaps stack counter title: "gopls/bug"?)
			fmt.Fprintf(body, "`%s`\n", line)
			continue
		}
		fileline, ok := pclntab[symbol]
		if !ok {
			// objdump reports ELF symbol names, which in
			// rare cases may be the Go symbols of
			// runtime.CallersFrames mangled by (e.g.) the
			// addition of .abi0 suffix; see
			// https://github.com/golang/go/issues/69390#issuecomment-2343795920
			// So this should not be a hard error.
			log.Printf("no pclntab info for symbol: %s", symbol)
			fmt.Fprintf(body, "`%s`\n", line)
			continue
		}
		if offset == "" {
			log.Fatalf("missing line offset: %s", line)
		}
		offsetNum, err := strconv.Atoi(offset[1:])
		if err != nil {
			log.Fatalf("invalid line offset: %s", line)
		}
		linenum := fileline.line
		switch offset[0] {
		case '-':
			linenum -= offsetNum
		case '+':
			linenum += offsetNum
		case '=':
			linenum = offsetNum
		}

		// Construct CodeSearch URL.
		var url string
		if firstSegment, _, _ := strings.Cut(fileline.file, "/"); !strings.Contains(firstSegment, ".") {
			// std
			// (First segment is a dir beneath GOROOT/src, not a module domain name.)
			url = fmt.Sprintf("https://cs.opensource.google/go/go/+/%s:src/%s;l=%d",
				info.GoVersion, fileline.file, linenum)

		} else if rest, ok := strings.CutPrefix(fileline.file, "golang.org/x/tools"); ok {
			// in x/tools repo (tools or gopls module)
			if rest[0] == '/' {
				// "golang.org/x/tools/gopls" -> "gopls"
				rest = rest[1:]
			} else if rest[0] == '@' {
				// "golang.org/x/tools@version/dir/file.go" -> "dir/file.go"
				rest = rest[strings.Index(rest, "/")+1:]
			}

			url = fmt.Sprintf("https://cs.opensource.google/go/x/tools/+/%s:%s;l=%d",
				"gopls/"+info.Version, rest, linenum)

		} else {
			// TODO(adonovan): support other module dependencies of gopls.
			log.Printf("no CodeSearch URL for %q (%s:%d)",
				symbol, fileline.file, linenum)
		}

		// Emit stack frame.
		if url != "" {
			fmt.Fprintf(body, "- [`%s`](%s)\n", line, url)
		} else {
			fmt.Fprintf(body, "- `%s`\n", line)
		}
	}

	// Add counts, gopls version, and platform info.
	// This isn't very precise but should provide clues.
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

	return title
}

// -- GitHub search --

// searchIssues queries the GitHub issue tracker.
func searchIssues(query string) (*IssuesSearchResult, error) {
	q := url.QueryEscape(query)

	req, err := http.NewRequest("GET", IssuesURL+"?q="+q, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Add("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("search query failed: %s (body: %s)", resp.Status, body)
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

// -- pclntab --

type FileLine struct {
	file string // "module@version/dir/file.go" or path relative to $GOROOT/src
	line int
}

// readPCLineTable builds the gopls executable specified by info,
// reads its PC-to-line-number table, and returns the file/line of
// each TEXT symbol.
func readPCLineTable(info Info) (map[string]FileLine, error) {
	// The stacks dir will be a semi-durable temp directory
	// (i.e. lasts for at least hours) holding source trees
	// and executables we have build recently.
	//
	// Each subdir will hold a specific revision.
	stacksDir := "/tmp/gopls-stacks"
	if err := os.MkdirAll(stacksDir, 0777); err != nil {
		return nil, fmt.Errorf("can't create stacks dir: %v", err)
	}

	// Fetch the source for the tools repo,
	// shallow-cloning just the desired revision.
	// (Skip if it's already cloned.)
	revDir := filepath.Join(stacksDir, info.Version)
	if !fileExists(revDir) {
		log.Printf("cloning tools@gopls/%s", info.Version)
		if err := shallowClone(revDir, "https://go.googlesource.com/tools", "gopls/"+info.Version); err != nil {
			return nil, fmt.Errorf("clone: %v", err)
		}
	}

	// Build the executable with the correct GOTOOLCHAIN, GOOS, GOARCH.
	// Use -trimpath for normalized file names.
	// (Skip if it's already built.)
	exe := fmt.Sprintf("exe-%s.%s-%s", info.GoVersion, info.GOOS, info.GOARCH)
	cmd := exec.Command("go", "build", "-trimpath", "-o", "../"+exe)
	cmd.Dir = filepath.Join(revDir, "gopls")
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN="+info.GoVersion,
		"GOOS="+info.GOOS,
		"GOARCH="+info.GOARCH,
	)
	if !fileExists(filepath.Join(revDir, exe)) {
		log.Printf("building %s@%s with %s on %s/%s",
			info.Program, info.Version, info.GoVersion, info.GOOS, info.GOARCH)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("building: %v", err)
		}
	}

	// Read pclntab of executable.
	cmd = exec.Command("go", "tool", "objdump", exe)
	cmd.Stdout = new(strings.Builder)
	cmd.Stderr = os.Stderr
	cmd.Dir = revDir
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN="+info.GoVersion,
		"GOOS="+info.GOOS,
		"GOARCH="+info.GOARCH,
	)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("reading pclntab %v", err)
	}
	pclntab := make(map[string]FileLine)
	lines := strings.Split(fmt.Sprint(cmd.Stdout), "\n")
	for i, line := range lines {
		// Each function is of this form:
		//
		// TEXT symbol(SB) filename
		//    basename.go:line instruction
		//    ...
		if !strings.HasPrefix(line, "TEXT ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue // symbol without file (e.g. go:buildid)
		}

		symbol := strings.TrimSuffix(fields[1], "(SB)")

		filename := fields[2]

		_, line, ok := strings.Cut(strings.Fields(lines[i+1])[0], ":")
		if !ok {
			return nil, fmt.Errorf("can't parse 'basename.go:line' from first instruction of %s:\n%s",
				symbol, line)
		}
		linenum, err := strconv.Atoi(line)
		if err != nil {
			return nil, fmt.Errorf("can't parse line number of %s: %s", symbol, line)
		}
		pclntab[symbol] = FileLine{filename, linenum}
	}

	return pclntab, nil
}

// shallowClone performs a shallow clone of repo into dir at the given
// 'commitish' ref (any commit reference understood by git).
//
// The directory dir must not already exist.
func shallowClone(dir, repo, commitish string) error {
	if err := os.Mkdir(dir, 0750); err != nil {
		return fmt.Errorf("creating dir for %s: %v", repo, err)
	}

	// Set a timeout for git fetch. If this proves flaky, it can be removed.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	// Use a shallow fetch to download just the relevant commit.
	shInit := fmt.Sprintf("git init && git fetch --depth=1 %q %q && git checkout FETCH_HEAD", repo, commitish)
	initCmd := exec.CommandContext(ctx, "/bin/sh", "-c", shInit)
	initCmd.Dir = dir
	if output, err := initCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("checking out %s: %v\n%s", repo, err, output)
	}
	return nil
}

func fileExists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}
