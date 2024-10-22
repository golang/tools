// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux || darwin

// The stacks command finds all gopls stack traces reported by
// telemetry in the past 7 days, and reports their associated GitHub
// issue, creating new issues as needed.
//
// The association of stacks with GitHub issues (labelled
// gopls/telemetry-wins) is represented in two different ways by the
// body (first comment) of the issue:
//
//  1. Each distinct stack is identified by an ID, 6-digit base64
//     string such as "TwtkSg". If a stack's ID appears anywhere
//     within the issue body, the stack is associated with the issue.
//
//     Some problems are highly deterministic, resulting in many
//     field reports of the exact same stack. For such problems, a
//     single ID in the issue body suffices to record the
//     association. But most problems are exhibited in a variety of
//     ways, leading to multiple field reports of similar but
//     distinct stacks.
//
//  2. Each GitHub issue body may start with a code block of this form:
//
//     ```
//     #!stacks
//     "runtime.sigpanic" && "golang.hover:+170"
//     ```
//
//     The first line indicates the purpose of the block; the
//     remainder is a predicate that matches stacks.
//     It is an expression defined by this grammar:
//
//     >  expr = "string literal"
//     >       | ( expr )
//     >       | ! expr
//     >       | expr && expr
//     >       | expr || expr
//
//     Each string literal implies a substring match on the stack;
//     the other productions are boolean operations.
//
//     The stacks command gathers all such predicates out of the
//     labelled issues and evaluates each one against each new stack.
//     If the predicate for an issue matches, the issue is considered
//     to have "claimed" the stack: the stack command appends a
//     comment containing the new (variant) stack to the issue, and
//     appends the stack's ID to the last line of the issue body.
//
//     It is an error if two issues' predicates attempt to claim the
//     same stack.
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
	"go/ast"
	"go/parser"
	"go/token"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/sys/unix"
	"golang.org/x/telemetry"
	"golang.org/x/tools/gopls/internal/util/browser"
	"golang.org/x/tools/gopls/internal/util/moremaps"
)

// flags
var (
	daysFlag = flag.Int("days", 7, "number of previous days of telemetry data to read")

	authToken string // mandatory GitHub authentication token (for R/W issues access)
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("stacks: ")
	flag.Parse()

	// Read GitHub authentication token from $HOME/.stacks.token.
	//
	// You can create one using the flow at: GitHub > You > Settings >
	// Developer Settings > Personal Access Tokens > Fine-grained tokens >
	// Generate New Token.  Generate the token on behalf of golang/go
	// with R/W access to "Issues".
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
			log.Fatalf("no file %s containing GitHub authentication token.", tokenFile)
		}
		authToken = string(bytes.TrimSpace(content))
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

	// Query GitHub for all existing GitHub issues with label:gopls/telemetry-wins.
	//
	// TODO(adonovan): by default GitHub returns at most 30
	// issues; we have lifted this to 100 using per_page=%d, but
	// that won't work forever; use paging.
	const query = "is:issue label:gopls/telemetry-wins"
	res, err := searchIssues(query)
	if err != nil {
		log.Fatalf("GitHub issues query %q failed: %v", query, err)
	}

	// Extract and validate predicate expressions in ```#!stacks...``` code blocks.
	// See the package doc comment for the grammar.
	for _, issue := range res.Items {
		block := findPredicateBlock(issue.Body)
		if block != "" {
			expr, err := parser.ParseExpr(block)
			if err != nil {
				log.Printf("invalid predicate in issue #%d: %v\n<<%s>>",
					issue.Number, err, block)
				continue
			}
			var validate func(ast.Expr) error
			validate = func(e ast.Expr) error {
				switch e := e.(type) {
				case *ast.UnaryExpr:
					if e.Op != token.NOT {
						return fmt.Errorf("invalid op: %s", e.Op)
					}
					return validate(e.X)

				case *ast.BinaryExpr:
					if e.Op != token.LAND && e.Op != token.LOR {
						return fmt.Errorf("invalid op: %s", e.Op)
					}
					if err := validate(e.X); err != nil {
						return err
					}
					return validate(e.Y)

				case *ast.ParenExpr:
					return validate(e.X)

				case *ast.BasicLit:
					if e.Kind != token.STRING {
						return fmt.Errorf("invalid literal (%s)", e.Kind)
					}
					if _, err := strconv.Unquote(e.Value); err != nil {
						return err
					}

				default:
					return fmt.Errorf("syntax error (%T)", e)
				}
				return nil
			}
			if err := validate(expr); err != nil {
				log.Printf("invalid predicate in issue #%d: %v\n<<%s>>",
					issue.Number, err, block)
				continue
			}
			issue.predicateText = block
			issue.predicate = func(stack string) bool {
				var eval func(ast.Expr) bool
				eval = func(e ast.Expr) bool {
					switch e := e.(type) {
					case *ast.UnaryExpr:
						return !eval(e.X)

					case *ast.BinaryExpr:
						if e.Op == token.LAND {
							return eval(e.X) && eval(e.Y)
						} else {
							return eval(e.X) || eval(e.Y)
						}

					case *ast.ParenExpr:
						return eval(e.X)

					case *ast.BasicLit:
						substr, _ := strconv.Unquote(e.Value)
						return strings.Contains(stack, substr)
					}
					panic("unreachable")
				}
				return eval(expr)
			}
		}
	}

	// Map each stack ID to its issue.
	//
	// An issue can claim a stack two ways:
	//
	// 1. if the issue body contains the ID of the stack. Matching
	//    is a little loose but base64 will rarely produce words
	//    that appear in the body by chance.
	//
	// 2. if the issue body contains a ```#!stacks``` predicate
	//    that matches the stack.
	//
	// We report an error if two different issues attempt to claim
	// the same stack.
	//
	// This is O(new stacks x existing issues).
	claimedBy := make(map[string]*Issue)
	for stack := range stacks {
		id := stackID(stack)
		for _, issue := range res.Items {
			byPredicate := false
			if strings.Contains(issue.Body, id) {
				// nop
			} else if issue.predicate != nil && issue.predicate(stack) {
				byPredicate = true
			} else {
				continue
			}

			if prev := claimedBy[id]; prev != nil && prev != issue {
				log.Printf("stack %s is claimed by issues #%d and #%d:%s",
					id, prev.Number, issue.Number, strings.ReplaceAll("\n"+stack, "\n", "\n- "))
				continue
			}
			if false {
				log.Printf("stack %s claimed by issue #%d",
					id, issue.Number)
			}
			claimedBy[id] = issue
			if byPredicate {
				// The stack ID matched the predicate but was not
				// found in the issue body, so this is a new stack.
				issue.newStacks = append(issue.newStacks, stack)
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

		var total int64
		for _, count := range counts {
			total += count
		}

		if issue, ok := claimedBy[id]; ok {
			// existing issue
			summary := fmt.Sprintf("#%d: %s [%s]",
				issue.Number, issue.Title, issue.State)
			existingIssues[summary] += total
		} else {
			// new issue
			title := newIssue(stack, id, stackToURL[stack], counts)
			summary := fmt.Sprintf("%s: %s [%s]", id, title, "new")
			newIssues[summary] += total
		}
	}

	// Update existing issues that claimed new stacks by predicate.
	for _, issue := range res.Items {
		if len(issue.newStacks) == 0 {
			continue
		}

		// Add a comment to the existing issue listing all its new stacks.
		// (Save the ID of each stack for the second step.)
		comment := new(bytes.Buffer)
		var newStackIDs []string
		for _, stack := range issue.newStacks {
			id := stackID(stack)
			newStackIDs = append(newStackIDs, id)
			writeStackComment(comment, stack, id, stackToURL[stack], stacks[stack])
		}
		if err := addIssueComment(issue.Number, comment.String()); err != nil {
			log.Println(err)
			continue
		}

		// Append to the "Dups: ID ..." list on last line of issue body.
		body := strings.TrimSpace(issue.Body)
		lastLineStart := strings.LastIndexByte(body, '\n') + 1
		lastLine := body[lastLineStart:]
		if !strings.HasPrefix(lastLine, "Dups:") {
			body += "\nDups:"
		}
		body += " " + strings.Join(newStackIDs, " ")
		if err := updateIssueBody(issue.Number, body); err != nil {
			log.Printf("added comment to issue #%d but failed to update body: %v",
				issue.Number, err)
			continue
		}

		log.Printf("added stacks %s to issue #%d", newStackIDs, issue.Number)
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
			// Show closed issues in "white".
			if isTerminal(os.Stdout) && strings.Contains(summary, "[closed]") {
				// ESC + "[" + n + "m" => change color to n
				// (37 = white, 0 = default)
				summary = "\x1B[37m" + summary + "\x1B[0m"
			}
			fmt.Printf("%s (n=%d)\n", summary, count)
		}
	}
	print("Existing", existingIssues)
	print("New", newIssues)
}

// Info is used as a key for de-duping and aggregating.
// Do not add detail about particular records (e.g. data, telemetry URL).
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
func newIssue(stack, id string, jsonURL string, counts map[Info]int64) string {
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

	// Add a placeholder ```#!stacks``` block since this is a new issue.
	body.WriteString("```" + `
#!stacks
"<insert predicate here>"
` + "```\n")
	fmt.Fprintf(body, "Issue created by [stacks](https://pkg.go.dev/golang.org/x/tools/gopls/internal/telemetry/cmd/stacks).\n\n")

	writeStackComment(body, stack, id, jsonURL, counts)

	const labels = "gopls,Tools,gopls/telemetry-wins,NeedsInvestigation"

	// Report it. The user will interactively finish the task,
	// since they will typically de-dup it without even creating a new issue
	// by expanding the #!stacks predicate of an existing issue.
	if !browser.Open("https://github.com/golang/go/issues/new?labels=" + labels + "&title=" + url.QueryEscape(title) + "&body=" + url.QueryEscape(body.String())) {
		log.Print("Please file a new issue at golang.org/issue/new using this template:\n\n")
		log.Printf("Title: %s\n", title)
		log.Printf("Labels: %s\n", labels)
		log.Printf("Body: %s\n", body)
	}

	return title
}

// writeStackComment writes a stack in Markdown form, for a new GitHub
// issue or new comment on an existing one.
func writeStackComment(body *bytes.Buffer, stack, id string, jsonURL string, counts map[Info]int64) {
	if len(counts) == 0 {
		panic("no counts")
	}
	var info Info // pick an arbitrary key
	for info = range counts {
		break
	}

	fmt.Fprintf(body, "This stack `%s` was [reported by telemetry](%s):\n\n",
		id, jsonURL)

	// Read the mapping from symbols to file/line.
	pclntab, err := readPCLineTable(info)
	if err != nil {
		log.Fatal(err)
	}

	// Parse the stack and get the symbol names out.
	for _, frame := range strings.Split(stack, "\n") {
		if url := frameURL(pclntab, info, frame); url != "" {
			fmt.Fprintf(body, "- [`%s`](%s)\n", frame, url)
		} else {
			fmt.Fprintf(body, "- `%s`\n", frame)
		}
	}

	// Add counts, gopls version, and platform info.
	// This isn't very precise but should provide clues.
	fmt.Fprintf(body, "```\n")
	for info, count := range counts {
		fmt.Fprintf(body, "%s (%d)\n", info, count)
	}
	fmt.Fprintf(body, "```\n\n")
}

// frameURL returns the CodeSearch URL for the stack frame, if known.
func frameURL(pclntab map[string]FileLine, info Info, frame string) string {
	// e.g. "golang.org/x/tools/gopls/foo.(*Type).Method.inlined.func3:+5"
	symbol, offset, ok := strings.Cut(frame, ":")
	if !ok {
		// Not a symbol (perhaps stack counter title: "gopls/bug"?)
		return ""
	}

	fileline, ok := pclntab[symbol]
	if !ok {
		// objdump reports ELF symbol names, which in
		// rare cases may be the Go symbols of
		// runtime.CallersFrames mangled by (e.g.) the
		// addition of .abi0 suffix; see
		// https://github.com/golang/go/issues/69390#issuecomment-2343795920
		// So this should not be a hard error.
		if symbol != "runtime.goexit" {
			log.Printf("no pclntab info for symbol: %s", symbol)
		}
		return ""
	}

	if offset == "" {
		log.Fatalf("missing line offset: %s", frame)
	}
	if unicode.IsDigit(rune(offset[0])) {
		// Fix gopls/v0.14.2 legacy syntax ":%d" -> ":+%d".
		offset = "+" + offset
	}
	offsetNum, err := strconv.Atoi(offset[1:])
	if err != nil {
		log.Fatalf("invalid line offset: %s", frame)
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

	// std module?
	firstSegment, _, _ := strings.Cut(fileline.file, "/")
	if !strings.Contains(firstSegment, ".") {
		// (First segment is a dir beneath GOROOT/src, not a module domain name.)
		return fmt.Sprintf("https://cs.opensource.google/go/go/+/%s:src/%s;l=%d",
			info.GoVersion, fileline.file, linenum)
	}

	// x/tools repo (tools or gopls module)?
	if rest, ok := strings.CutPrefix(fileline.file, "golang.org/x/tools"); ok {
		if rest[0] == '/' {
			// "golang.org/x/tools/gopls" -> "gopls"
			rest = rest[1:]
		} else if rest[0] == '@' {
			// "golang.org/x/tools@version/dir/file.go" -> "dir/file.go"
			rest = rest[strings.Index(rest, "/")+1:]
		}

		return fmt.Sprintf("https://cs.opensource.google/go/x/tools/+/%s:%s;l=%d",
			"gopls/"+info.Version, rest, linenum)
	}

	// other x/ module dependency?
	// e.g. golang.org/x/sync@v0.8.0/errgroup/errgroup.go
	if rest, ok := strings.CutPrefix(fileline.file, "golang.org/x/"); ok {
		if modVer, filename, ok := strings.Cut(rest, "/"); ok {
			if mod, version, ok := strings.Cut(modVer, "@"); ok {
				return fmt.Sprintf("https://cs.opensource.google/go/x/%s/+/%s:%s;l=%d",
					mod, version, filename, linenum)
			}
		}
	}

	log.Printf("no CodeSearch URL for %q (%s:%d)",
		symbol, fileline.file, linenum)
	return ""
}

// -- GitHub search --

// searchIssues queries the GitHub issue tracker.
func searchIssues(query string) (*IssuesSearchResult, error) {
	q := url.QueryEscape(query)

	req, err := http.NewRequest("GET", "https://api.github.com/search/issues?q="+q+"&per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", "Bearer "+authToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search query failed: %s (body: %s)", resp.Status, body)
	}
	var result IssuesSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

// updateIssueBody updates the body of the numbered issue.
func updateIssueBody(number int, body string) error {
	// https://docs.github.com/en/rest/issues/comments#update-an-issue
	var payload struct {
		Body string `json:"body"`
	}
	payload.Body = body
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.github.com/repos/golang/go/issues/%d", number)
	req, err := http.NewRequest("PATCH", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+authToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("issue update failed: %s (body: %s)", resp.Status, body)
	}
	return nil
}

// addIssueComment adds a markdown comment to the numbered issue.
func addIssueComment(number int, comment string) error {
	// https://docs.github.com/en/rest/issues/comments#create-an-issue-comment
	var payload struct {
		Body string `json:"body"`
	}
	payload.Body = comment
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.github.com/repos/golang/go/issues/%d/comments", number)
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Bearer "+authToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create issue comment: %s (body: %s)", resp.Status, body)
	}
	return nil
}

// See https://developer.github.com/v3/search/#search-issues.

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

	predicateText string            // text of ```#!stacks...``` predicate block
	predicate     func(string) bool // matching predicate over stack text
	newStacks     []string          // new stacks to add to existing issue (comments and IDs)
}

type User struct {
	Login   string
	HTMLURL string `json:"html_url"`
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
	// and executables we have built recently.
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
	if !fileExists(filepath.Join(revDir, "go.mod")) {
		// We check for presence of the go.mod file,
		// not just the directory itself, as the /tmp reaper
		// often removes stale files before removing their directories.
		// Remove those stale directories now.
		_ = os.RemoveAll(revDir) // ignore errors

		log.Printf("cloning tools@gopls/%s", info.Version)
		if err := shallowClone(revDir, "https://go.googlesource.com/tools", "gopls/"+info.Version); err != nil {
			_ = os.RemoveAll(revDir) // ignore errors
			return nil, fmt.Errorf("clone: %v", err)
		}
	}

	// Build the executable with the correct GOTOOLCHAIN, GOOS, GOARCH.
	// Use -trimpath for normalized file names.
	// (Skip if it's already built.)
	exe := fmt.Sprintf("exe-%s.%s-%s", info.GoVersion, info.GOOS, info.GOARCH)
	cmd := exec.Command("go", "build", "-trimpath", "-o", "../"+exe)
	cmd.Stderr = os.Stderr
	cmd.Dir = filepath.Join(revDir, "gopls")
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN="+info.GoVersion,
		"GOOS="+info.GOOS,
		"GOARCH="+info.GOARCH,
	)
	if !fileExists(filepath.Join(revDir, exe)) {
		log.Printf("building %s@%s with %s for %s/%s",
			info.Program, info.Version, info.GoVersion, info.GOOS, info.GOARCH)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("building: %v (rm -fr /tmp/gopls-stacks?)", err)
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

// findPredicateBlock returns the content (sans "#!stacks") of the
// code block at the start of the issue body.
// Logic plundered from x/build/cmd/watchflakes/github.go.
func findPredicateBlock(body string) string {
	// Extract ```-fenced or indented code block at start of issue description (body).
	body = strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.SplitAfter(body, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	text := ""
	// A code quotation is bracketed by sequence of 3+ backticks.
	// (More than 3 are permitted so that one can quote 3 backticks.)
	if len(lines) > 0 && strings.HasPrefix(lines[0], "```") {
		marker := lines[0]
		n := 0
		for n < len(marker) && marker[n] == '`' {
			n++
		}
		marker = marker[:n]
		i := 1
		for i := 1; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], marker) && strings.TrimSpace(strings.TrimLeft(lines[i], "`")) == "" {
				text = strings.Join(lines[1:i], "")
				break
			}
		}
		if i < len(lines) {
		}
	} else if strings.HasPrefix(lines[0], "\t") || strings.HasPrefix(lines[0], "    ") {
		i := 1
		for i < len(lines) && (strings.HasPrefix(lines[i], "\t") || strings.HasPrefix(lines[i], "    ")) {
			i++
		}
		text = strings.Join(lines[:i], "")
	}

	// Must start with #!stacks so we're sure it is for us.
	hdr, rest, _ := strings.Cut(text, "\n")
	hdr = strings.TrimSpace(hdr)
	if hdr != "#!stacks" {
		return ""
	}
	return rest
}

// isTerminal reports whether file is a terminal,
// avoiding a dependency on golang.org/x/term.
func isTerminal(file *os.File) bool {
	// Hardwire the constants to avoid the need for build tags.
	// The values here are good for our dev machines.
	switch runtime.GOOS {
	case "darwin":
		const TIOCGETA = 0x40487413 // from unix.TIOCGETA
		_, err := unix.IoctlGetTermios(int(file.Fd()), TIOCGETA)
		return err == nil
	case "linux":
		const TCGETS = 0x5401 // from unix.TCGETS
		_, err := unix.IoctlGetTermios(int(file.Fd()), TCGETS)
		return err == nil
	}
	panic("unreachable")
}
