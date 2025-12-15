// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package integration

import (
	"bytes"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/server"
	"golang.org/x/tools/gopls/internal/util/constraints"
)

var (
	// InitialWorkspaceLoad is an expectation that the workspace initial load has
	// completed. It is verified via workdone reporting.
	InitialWorkspaceLoad = CompletedWork(server.DiagnosticWorkTitle(server.FromInitialWorkspaceLoad), 1, false)
)

// A Verdict is the result of checking an expectation against the current
// editor state.
type Verdict int

// Order matters for the following constants: verdicts are sorted in order of
// decisiveness.
const (
	// Met indicates that an expectation is satisfied by the current state.
	Met Verdict = iota
	// Unmet indicates that an expectation is not currently met, but could be met
	// in the future.
	Unmet
	// Unmeetable indicates that an expectation cannot be satisfied in the
	// future.
	Unmeetable
)

func (v Verdict) String() string {
	switch v {
	case Met:
		return "Met"
	case Unmet:
		return "Unmet"
	case Unmeetable:
		return "Unmeetable"
	}
	return fmt.Sprintf("unrecognized verdict %d", v)
}

// An Expectation is an expected property of the state of the LSP client.
// The Check function reports whether the property is met.
//
// Expectations are combinators. By composing them, tests may express
// complex expectations in terms of simpler ones.
type Expectation struct {
	// Check returns the verdict of this expectation for the given state.
	// If the vertict is not [Met], the second result should return a reason
	// that the verdict is not (yet) met.
	Check func(State) (Verdict, string)

	// Description holds a noun-phrase identifying what the expectation checks.
	//
	// TODO(rfindley): revisit existing descriptions to ensure they compose nicely.
	Description string
}

// OnceMet returns an Expectation that, once the precondition is met, asserts
// that mustMeet is met.
func OnceMet(pre, post Expectation) Expectation {
	check := func(s State) (Verdict, string) {
		switch v, why := pre.Check(s); v {
		case Unmeetable, Unmet:
			return v, fmt.Sprintf("precondition is %s: %s", v, why)
		case Met:
			v, why := post.Check(s)
			if v != Met {
				return Unmeetable, fmt.Sprintf("postcondition is not met:\n%s", indent(why))
			}
			return Met, ""
		default:
			panic(fmt.Sprintf("unknown precondition verdict %s", v))
		}
	}
	desc := fmt.Sprintf("once the following is met:\n%s\nmust have:\n%s",
		indent(pre.Description), indent(post.Description))
	return Expectation{
		Check:       check,
		Description: desc,
	}
}

// Not inverts the sense of an expectation: a met expectation is unmet, and an
// unmet expectation is met.
func Not(e Expectation) Expectation {
	check := func(s State) (Verdict, string) {
		switch v, _ := e.Check(s); v {
		case Met:
			return Unmet, "condition unexpectedly satisfied"
		case Unmet, Unmeetable:
			return Met, ""
		default:
			panic(fmt.Sprintf("unexpected verdict %v", v))
		}
	}
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("not: %s", e.Description),
	}
}

// AnyOf returns an expectation that is satisfied when any of the given
// expectations is met.
func AnyOf(anyOf ...Expectation) Expectation {
	if len(anyOf) == 1 {
		return anyOf[0] // avoid unnecessary boilerplate
	}
	check := func(s State) (Verdict, string) {
		for _, e := range anyOf {
			verdict, _ := e.Check(s)
			if verdict == Met {
				return Met, ""
			}
		}
		return Unmet, "none of the expectations were met"
	}
	description := describeExpectations(anyOf...)
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("any of:\n%s", description),
	}
}

// AllOf expects that all given expectations are met.
func AllOf(allOf ...Expectation) Expectation {
	if len(allOf) == 1 {
		return allOf[0] // avoid unnecessary boilerplate
	}
	check := func(s State) (Verdict, string) {
		var (
			verdict = Met
			reason  string
		)
		for _, e := range allOf {
			v, why := e.Check(s)
			if v > verdict {
				verdict = v
				reason = why
			}
		}
		return verdict, reason
	}
	desc := describeExpectations(allOf...)
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("all of:\n%s", indent(desc)),
	}
}

func describeExpectations(expectations ...Expectation) string {
	var descriptions []string
	for _, e := range expectations {
		descriptions = append(descriptions, e.Description)
	}
	return strings.Join(descriptions, "\n")
}

// ReadDiagnostics is an Expectation that stores the current diagnostics for
// fileName in into, whenever it is evaluated.
//
// It can be used in combination with OnceMet or AfterChange to capture the
// state of diagnostics when other expectations are satisfied.
func ReadDiagnostics(fileName string, into *protocol.PublishDiagnosticsParams) Expectation {
	check := func(s State) (Verdict, string) {
		diags, ok := s.diagnostics[fileName]
		if !ok {
			return Unmeetable, fmt.Sprintf("no diagnostics for %q", fileName)
		}
		*into = *diags
		return Met, ""
	}
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("read diagnostics for %q", fileName),
	}
}

// ReadAllDiagnostics is an expectation that stores all published diagnostics
// into the provided map, whenever it is evaluated.
//
// It can be used in combination with OnceMet or AfterChange to capture the
// state of diagnostics when other expectations are satisfied.
func ReadAllDiagnostics(into *map[string]*protocol.PublishDiagnosticsParams) Expectation {
	check := func(s State) (Verdict, string) {
		allDiags := maps.Clone(s.diagnostics)
		*into = allDiags
		return Met, ""
	}
	return Expectation{
		Check:       check,
		Description: "read all diagnostics",
	}
}

// ShownDocument asserts that the client has received a
// ShowDocumentRequest for the given URI.
func ShownDocument(uri protocol.URI) Expectation {
	check := func(s State) (Verdict, string) {
		for _, params := range s.showDocument {
			if params.URI == uri {
				return Met, ""
			}
		}
		return Unmet, fmt.Sprintf("no ShowDocumentRequest received for %s", uri)
	}
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("received window/showDocument for URI %s", uri),
	}
}

// ShownDocuments is an expectation that appends each showDocument
// request into the provided slice, whenever it is evaluated.
//
// It can be used in combination with OnceMet or AfterChange to
// capture the set of showDocument requests when other expectations
// are satisfied.
func ShownDocuments(into *[]*protocol.ShowDocumentParams) Expectation {
	check := func(s State) (Verdict, string) {
		*into = append(*into, s.showDocument...)
		return Met, ""
	}
	return Expectation{
		Check:       check,
		Description: "read shown documents",
	}
}

// NoShownMessage asserts that the editor has not received a ShowMessage.
func NoShownMessage(containing string) Expectation {
	check := func(s State) (Verdict, string) {
		for _, m := range s.showMessage {
			if strings.Contains(m.Message, containing) {
				// Format the message (which may contain newlines) as a block quote.
				msg := fmt.Sprintf("\"\"\"\n%s\n\"\"\"", strings.TrimSpace(m.Message))
				return Unmeetable, fmt.Sprintf("observed the following message:\n%s", indent(msg))
			}
		}
		return Met, ""
	}
	var desc string
	if containing != "" {
		desc = fmt.Sprintf("received no ShowMessage containing %q", containing)
	} else {
		desc = "received no ShowMessage requests"
	}
	return Expectation{
		Check:       check,
		Description: desc,
	}
}

// ShownMessage asserts that the editor has received a ShowMessageRequest
// containing the given substring.
func ShownMessage(containing string) Expectation {
	check := func(s State) (Verdict, string) {
		for _, m := range s.showMessage {
			if strings.Contains(m.Message, containing) {
				return Met, ""
			}
		}
		return Unmet, fmt.Sprintf("no ShowMessage containing %q", containing)
	}
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("received window/showMessage containing %q", containing),
	}
}

// ShownMessageRequest asserts that the editor has received a
// ShowMessageRequest with message matching the given regular expression.
func ShownMessageRequest(matchingRegexp string) Expectation {
	msgRE := regexp.MustCompile(matchingRegexp)
	check := func(s State) (Verdict, string) {
		if len(s.showMessageRequest) == 0 {
			return Unmet, "no ShowMessageRequest have been received"
		}
		for _, m := range s.showMessageRequest {
			if msgRE.MatchString(m.Message) {
				return Met, ""
			}
		}
		return Unmet, fmt.Sprintf("no ShowMessageRequest (out of %d) match %q", len(s.showMessageRequest), matchingRegexp)
	}
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("ShowMessageRequest matching %q", matchingRegexp),
	}
}

// DoneDiagnosingChanges expects that diagnostics are complete from common
// change notifications: didOpen, didChange, didSave, didChangeWatchedFiles,
// and didClose.
//
// This can be used when multiple notifications may have been sent, such as
// when a didChange is immediately followed by a didSave. It is insufficient to
// simply await NoOutstandingWork, because the LSP client has no control over
// when the server starts processing a notification. Therefore, we must keep
// track of
func (e *Env) DoneDiagnosingChanges() Expectation {
	stats := e.Editor.Stats()
	statsBySource := map[server.ModificationSource]uint64{
		server.FromDidOpen:                stats.DidOpen,
		server.FromDidChange:              stats.DidChange,
		server.FromDidSave:                stats.DidSave,
		server.FromDidChangeWatchedFiles:  stats.DidChangeWatchedFiles,
		server.FromDidClose:               stats.DidClose,
		server.FromDidChangeConfiguration: stats.DidChangeConfiguration,
	}

	var expected []server.ModificationSource
	for k, v := range statsBySource {
		if v > 0 {
			expected = append(expected, k)
		}
	}

	// Sort for stability.
	slices.Sort(expected)

	var all []Expectation
	for _, source := range expected {
		all = append(all, CompletedWork(server.DiagnosticWorkTitle(source), statsBySource[source], true))
	}

	return AllOf(all...)
}

// AfterChange expects that the given expectations will be met after all
// state-changing notifications have been processed by the server.
// Specifically, it awaits the awaits completion of the process of diagnosis
// after the following notifications, before checking the given expectations:
//   - textDocument/didOpen
//   - textDocument/didChange
//   - textDocument/didSave
//   - textDocument/didClose
//   - workspace/didChangeWatchedFiles
//   - workspace/didChangeConfiguration
func (e *Env) AfterChange(expectations ...Expectation) {
	e.TB.Helper()
	e.OnceMet(
		e.DoneDiagnosingChanges(),
		expectations...,
	)
}

// DoneWithOpen expects all didOpen notifications currently sent by the editor
// to be completely processed.
func (e *Env) DoneWithOpen() Expectation {
	opens := e.Editor.Stats().DidOpen
	return CompletedWork(server.DiagnosticWorkTitle(server.FromDidOpen), opens, true)
}

// StartedChange expects that the server has at least started processing all
// didChange notifications sent from the client.
func (e *Env) StartedChange() Expectation {
	changes := e.Editor.Stats().DidChange
	return StartedWork(server.DiagnosticWorkTitle(server.FromDidChange), changes)
}

// DoneWithChange expects all didChange notifications currently sent by the
// editor to be completely processed.
func (e *Env) DoneWithChange() Expectation {
	changes := e.Editor.Stats().DidChange
	return CompletedWork(server.DiagnosticWorkTitle(server.FromDidChange), changes, true)
}

// DoneWithSave expects all didSave notifications currently sent by the editor
// to be completely processed.
func (e *Env) DoneWithSave() Expectation {
	saves := e.Editor.Stats().DidSave
	return CompletedWork(server.DiagnosticWorkTitle(server.FromDidSave), saves, true)
}

// StartedChangeWatchedFiles expects that the server has at least started
// processing all didChangeWatchedFiles notifications sent from the client.
func (e *Env) StartedChangeWatchedFiles() Expectation {
	changes := e.Editor.Stats().DidChangeWatchedFiles
	return StartedWork(server.DiagnosticWorkTitle(server.FromDidChangeWatchedFiles), changes)
}

// DoneWithChangeWatchedFiles expects all didChangeWatchedFiles notifications
// currently sent by the editor to be completely processed.
func (e *Env) DoneWithChangeWatchedFiles() Expectation {
	changes := e.Editor.Stats().DidChangeWatchedFiles
	return CompletedWork(server.DiagnosticWorkTitle(server.FromDidChangeWatchedFiles), changes, true)
}

// DoneWithClose expects all didClose notifications currently sent by the
// editor to be completely processed.
func (e *Env) DoneWithClose() Expectation {
	changes := e.Editor.Stats().DidClose
	return CompletedWork(server.DiagnosticWorkTitle(server.FromDidClose), changes, true)
}

// StartedWork expect a work item to have been started >= atLeast times.
//
// See CompletedWork.
func StartedWork(title string, atLeast uint64) Expectation {
	check := func(s State) (Verdict, string) {
		started := s.startedWork[title]
		if started >= atLeast {
			return Met, ""
		}
		return Unmet, fmt.Sprintf("started work %d %s", started, pluralize("time", started))
	}
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("started work %q at least %d %s", title, atLeast, pluralize("time", atLeast)),
	}
}

// CompletedWork expects a work item to have been completed >= atLeast times.
//
// Since the Progress API doesn't include any hidden metadata, we must use the
// progress notification title to identify the work we expect to be completed.
func CompletedWork(title string, count uint64, atLeast bool) Expectation {
	check := func(s State) (Verdict, string) {
		completed := s.completedWork[title]
		if completed == count || atLeast && completed > count {
			return Met, ""
		}
		return Unmet, fmt.Sprintf("completed %d %s", completed, pluralize("time", completed))
	}
	desc := fmt.Sprintf("completed work %q %v %s", title, count, pluralize("time", count))
	if atLeast {
		desc = fmt.Sprintf("completed work %q at least %d %s", title, count, pluralize("time", count))
	}
	return Expectation{
		Check:       check,
		Description: desc,
	}
}

// pluralize adds an 's' suffix to name if n > 1.
func pluralize[T constraints.Integer](name string, n T) string {
	if n > 1 {
		return name + "s"
	}
	return name
}

type WorkStatus struct {
	// Last seen message from either `begin` or `report` progress.
	Msg string
	// Message sent with `end` progress message.
	EndMsg string
}

// CompletedProgressToken expects that workDone progress is complete for the given
// progress token. When non-nil WorkStatus is provided, it will be filled
// when the expectation is met.
//
// If the token is not a progress token that the client has seen, this
// expectation is Unmeetable.
func CompletedProgressToken(token protocol.ProgressToken, into *WorkStatus) Expectation {
	check := func(s State) (Verdict, string) {
		work, ok := s.work[token]
		if !ok {
			return Unmeetable, "no matching work items"
		}
		if work.complete {
			if into != nil {
				into.Msg = work.msg
				into.EndMsg = work.endMsg
			}
			return Met, ""
		}
		return Unmet, fmt.Sprintf("work is not complete; last message: %q", work.msg)
	}
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("completed work for token %v", token),
	}
}

// CompletedProgress expects that there is exactly one workDone progress with
// the given title, and is satisfied when that progress completes. If it is
// met, the corresponding status is written to the into argument.
//
// TODO(rfindley): refactor to eliminate the redundancy with CompletedWork.
// This expectation is a vestige of older workarounds for asynchronous command
// execution.
func CompletedProgress(title string, into *WorkStatus) Expectation {
	check := func(s State) (Verdict, string) {
		var work *workProgress
		for _, w := range s.work {
			if w.title == title {
				if work != nil {
					return Unmeetable, "multiple matching work items"
				}
				work = w
			}
		}
		if work == nil {
			return Unmeetable, "no matching work items"
		}
		if work.complete {
			if into != nil {
				into.Msg = work.msg
				into.EndMsg = work.endMsg
			}
			return Met, ""
		}
		return Unmet, fmt.Sprintf("work is not complete; last message: %q", work.msg)
	}
	desc := fmt.Sprintf("exactly 1 completed workDoneProgress with title %v", title)
	return Expectation{
		Check:       check,
		Description: desc,
	}
}

// OutstandingWork expects a work item to be outstanding. The given title must
// be an exact match, whereas the given msg must only be contained in the work
// item's message.
func OutstandingWork(title, msg string) Expectation {
	check := func(s State) (Verdict, string) {
		for _, work := range s.work {
			if work.complete {
				continue
			}
			if work.title == title && strings.Contains(work.msg, msg) {
				return Met, ""
			}
		}
		return Unmet, "no matching work"
	}
	return Expectation{
		Check:       check,
		Description: fmt.Sprintf("outstanding work: %q containing %q", title, msg),
	}
}

// NoOutstandingWork asserts that there is no work initiated using the LSP
// $/progress API that has not completed.
//
// If non-nil, the ignore func is used to ignore certain work items for the
// purpose of this check.
//
// TODO(rfindley): consider refactoring to treat outstanding work the same way
// we treat diagnostics: with an algebra of filters.
func NoOutstandingWork(ignore func(title, msg string) bool) Expectation {
	check := func(s State) (Verdict, string) {
		for _, w := range s.work {
			if w.complete {
				continue
			}
			if w.title == "" {
				// A token that has been created but not yet used.
				//
				// TODO(rfindley): this should be separated in the data model: until
				// the "begin" notification, work should not be in progress.
				continue
			}
			if ignore != nil && ignore(w.title, w.msg) {
				continue
			}
			return Unmet, fmt.Sprintf("found outstanding work %q: %q", w.title, w.msg)
		}
		return Met, ""
	}
	return Expectation{
		Check:       check,
		Description: "no outstanding work",
	}
}

// IgnoreTelemetryPromptWork may be used in conjunction with NoOutStandingWork
// to ignore the telemetry prompt.
func IgnoreTelemetryPromptWork(title, msg string) bool {
	return title == server.TelemetryPromptWorkTitle
}

// NoErrorLogs asserts that the client has not received any log messages of
// error severity.
func NoErrorLogs() Expectation {
	return NoLogMatching(protocol.Error, "")
}

// LogMatching asserts that the client has received a log message
// of type typ matching the regexp re a certain number of times.
//
// The count argument specifies the expected number of matching logs. If
// atLeast is set, this is a lower bound, otherwise there must be exactly count
// matching logs.
//
// Logs are asynchronous to other LSP messages, so this expectation should not
// be used with combinators such as OnceMet or AfterChange that assert on
// ordering with respect to other operations.
func LogMatching(typ protocol.MessageType, re string, count int, atLeast bool) Expectation {
	rec, err := regexp.Compile(re)
	if err != nil {
		panic(err)
	}
	check := func(state State) (Verdict, string) {
		var found int
		for _, msg := range state.logs {
			if msg.Type == typ && rec.Match([]byte(msg.Message)) {
				found++
			}
		}
		// Check for an exact or "at least" match.
		if found == count || (found >= count && atLeast) {
			return Met, ""
		}
		// If we require an exact count, and have received more than expected, the
		// expectation can never be met.
		verdict := Unmet
		if found > count && !atLeast {
			verdict = Unmeetable
		}
		return verdict, fmt.Sprintf("found %d matching logs", found)
	}
	desc := fmt.Sprintf("log message matching %q expected %v times", re, count)
	if atLeast {
		desc = fmt.Sprintf("log message matching %q expected at least %v times", re, count)
	}
	return Expectation{
		Check:       check,
		Description: desc,
	}
}

// NoLogMatching asserts that the client has not received a log message
// of type typ matching the regexp re. If re is an empty string, any log
// message is considered a match.
func NoLogMatching(typ protocol.MessageType, re string) Expectation {
	var r *regexp.Regexp
	if re != "" {
		var err error
		r, err = regexp.Compile(re)
		if err != nil {
			panic(err)
		}
	}
	check := func(state State) (Verdict, string) {
		for _, msg := range state.logs {
			if msg.Type != typ {
				continue
			}
			if r == nil || r.Match([]byte(msg.Message)) {
				return Unmeetable, fmt.Sprintf("found matching log %q", msg.Message)
			}
		}
		return Met, ""
	}
	desc := fmt.Sprintf("no %s log messages", typ)
	if re != "" {
		desc += fmt.Sprintf(" matching %q", re)
	}
	return Expectation{
		Check:       check,
		Description: desc,
	}
}

// FileWatchMatching expects that a file registration matches re.
func FileWatchMatching(re string) Expectation {
	return Expectation{
		Check:       checkFileWatch(re, Met, Unmet),
		Description: fmt.Sprintf("file watch matching %q", re),
	}
}

// NoFileWatchMatching expects that no file registration matches re.
func NoFileWatchMatching(re string) Expectation {
	return Expectation{
		Check:       checkFileWatch(re, Unmet, Met),
		Description: fmt.Sprintf("no file watch matching %q", re),
	}
}

func checkFileWatch(re string, onMatch, onNoMatch Verdict) func(State) (Verdict, string) {
	rec := regexp.MustCompile(re)
	return func(s State) (Verdict, string) {
		r := s.registeredCapabilities["workspace/didChangeWatchedFiles"]
		watchers := jsonProperty(r.RegisterOptions, "watchers").([]any)
		for _, watcher := range watchers {
			pattern := jsonProperty(watcher, "globPattern").(string)
			if rec.MatchString(pattern) {
				return onMatch, fmt.Sprintf("matches watcher pattern %q", pattern)
			}
		}
		return onNoMatch, "no matching watchers"
	}
}

// jsonProperty extracts a value from a path of JSON property names, assuming
// the default encoding/json unmarshaling to the empty interface (i.e.: that
// JSON objects are unmarshalled as map[string]interface{})
//
// For example, if obj is unmarshalled from the following json:
//
//	{
//		"foo": { "bar": 3 }
//	}
//
// Then jsonProperty(obj, "foo", "bar") will be 3.
func jsonProperty(obj any, path ...string) any {
	if len(path) == 0 || obj == nil {
		return obj
	}
	m := obj.(map[string]any)
	return jsonProperty(m[path[0]], path[1:]...)
}

func formatDiagnostic(d protocol.Diagnostic) string {
	return fmt.Sprintf("%d:%d [%s]: %s\n", d.Range.Start.Line, d.Range.Start.Character, d.Source, d.Message)
}

// Diagnostics asserts that there is at least one diagnostic matching the given
// filters.
func Diagnostics(filters ...DiagnosticFilter) Expectation {
	check := func(s State) (Verdict, string) {
		diags := flattenDiagnostics(s)
		for _, filter := range filters {
			var filtered []flatDiagnostic
			for _, d := range diags {
				if filter.check(d.name, d.diag) {
					filtered = append(filtered, d)
				}
			}
			if len(filtered) == 0 {
				// Reprinting the description of the filters is too verbose.
				//
				// We can probably do better here, but for now just format the
				// diagnostics.
				var b bytes.Buffer
				for name, params := range s.diagnostics {
					fmt.Fprintf(&b, "\t%s (version %d):\n", name, params.Version)
					for _, d := range params.Diagnostics {
						fmt.Fprintf(&b, "\t\t%s", formatDiagnostic(d))
					}
				}
				return Unmet, fmt.Sprintf("diagnostics:\n%s", b.String())
			}
			diags = filtered
		}
		return Met, ""
	}
	var descs []string
	for _, filter := range filters {
		descs = append(descs, filter.desc)
	}
	return Expectation{
		Check:       check,
		Description: "any diagnostics " + strings.Join(descs, ", "),
	}
}

// NoDiagnostics asserts that there are no diagnostics matching the given
// filters. Notably, if no filters are supplied this assertion checks that
// there are no diagnostics at all, for any file.
func NoDiagnostics(filters ...DiagnosticFilter) Expectation {
	check := func(s State) (Verdict, string) {
		diags := flattenDiagnostics(s)
		for _, filter := range filters {
			var filtered []flatDiagnostic
			for _, d := range diags {
				if filter.check(d.name, d.diag) {
					filtered = append(filtered, d)
				}
			}
			diags = filtered
		}
		if len(diags) > 0 {
			d := diags[0]
			why := fmt.Sprintf("have diagnostic: %s: %v", d.name, formatDiagnostic(d.diag))
			return Unmet, why
		}
		return Met, ""
	}
	var descs []string
	for _, filter := range filters {
		descs = append(descs, filter.desc)
	}
	return Expectation{
		Check:       check,
		Description: "no diagnostics " + strings.Join(descs, ", "),
	}
}

type flatDiagnostic struct {
	name string
	diag protocol.Diagnostic
}

func flattenDiagnostics(state State) []flatDiagnostic {
	var result []flatDiagnostic
	for name, diags := range state.diagnostics {
		for _, diag := range diags.Diagnostics {
			result = append(result, flatDiagnostic{name, diag})
		}
	}
	return result
}

// -- Diagnostic filters --

// A DiagnosticFilter filters the set of diagnostics, for assertion with
// Diagnostics or NoDiagnostics.
type DiagnosticFilter struct {
	desc  string
	check func(name string, _ protocol.Diagnostic) bool
}

// ForFile filters to diagnostics matching the sandbox-relative file name.
func ForFile(name string) DiagnosticFilter {
	return DiagnosticFilter{
		desc: fmt.Sprintf("for file %q", name),
		check: func(diagName string, _ protocol.Diagnostic) bool {
			return diagName == name
		},
	}
}

// FromSource filters to diagnostics matching the given diagnostics source.
func FromSource(source string) DiagnosticFilter {
	return DiagnosticFilter{
		desc: fmt.Sprintf("with source %q", source),
		check: func(_ string, d protocol.Diagnostic) bool {
			return d.Source == source
		},
	}
}

// AtRegexp filters to diagnostics in the file with sandbox-relative path name,
// at the first position matching the given regexp pattern.
//
// TODO(rfindley): pass in the editor to expectations, so that they may depend
// on editor state and AtRegexp can be a function rather than a method.
func (e *Env) AtRegexp(name, pattern string) DiagnosticFilter {
	loc := e.RegexpSearch(name, pattern)
	return DiagnosticFilter{
		desc: fmt.Sprintf("at the first position (%v) matching %#q in %q", loc.Range.Start, pattern, name),
		check: func(diagName string, d protocol.Diagnostic) bool {
			return diagName == name && d.Range.Start == loc.Range.Start
		},
	}
}

// AtPosition filters to diagnostics at location name:line:character, for a
// sandbox-relative path name.
//
// Line and character are 0-based, and character measures UTF-16 codes.
//
// Note: prefer the more readable AtRegexp.
func AtPosition(name string, line, character uint32) DiagnosticFilter {
	pos := protocol.Position{Line: line, Character: character}
	return DiagnosticFilter{
		desc: fmt.Sprintf("at %s:%d:%d", name, line, character),
		check: func(diagName string, d protocol.Diagnostic) bool {
			return diagName == name && d.Range.Start == pos
		},
	}
}

// WithMessage filters to diagnostics whose message contains the given
// substring.
func WithMessage(substring string) DiagnosticFilter {
	return DiagnosticFilter{
		desc: fmt.Sprintf("with message containing %q", substring),
		check: func(_ string, d protocol.Diagnostic) bool {
			return strings.Contains(d.Message, substring)
		},
	}
}

// WithSeverityTags filters to diagnostics whose severity and tags match
// the given expectation.
func WithSeverityTags(diagName string, severity protocol.DiagnosticSeverity, tags []protocol.DiagnosticTag) DiagnosticFilter {
	return DiagnosticFilter{
		desc: fmt.Sprintf("with diagnostic %q with severity %q and tag %#q", diagName, severity, tags),
		check: func(_ string, d protocol.Diagnostic) bool {
			return d.Source == diagName && d.Severity == severity && cmp.Equal(d.Tags, tags)
		},
	}
}
