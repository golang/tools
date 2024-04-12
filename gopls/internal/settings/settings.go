// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package settings

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/tools/gopls/internal/analysis/unusedvariable"
	"golang.org/x/tools/gopls/internal/file"
	"golang.org/x/tools/gopls/internal/protocol"
	"golang.org/x/tools/gopls/internal/protocol/command"
)

type Annotation string

const (
	// Nil controls nil checks.
	Nil Annotation = "nil"

	// Escape controls diagnostics about escape choices.
	Escape Annotation = "escape"

	// Inline controls diagnostics about inlining choices.
	Inline Annotation = "inline"

	// Bounds controls bounds checking diagnostics.
	Bounds Annotation = "bounds"
)

// Options holds various configuration that affects Gopls execution, organized
// by the nature or origin of the settings.
//
// Options must be comparable with reflect.DeepEqual.
type Options struct {
	ClientOptions
	ServerOptions
	UserOptions
	InternalOptions
}

// ClientOptions holds LSP-specific configuration that is provided by the
// client.
//
// ClientOptions must be comparable with reflect.DeepEqual.
type ClientOptions struct {
	ClientInfo                                 *protocol.ClientInfo
	InsertTextFormat                           protocol.InsertTextFormat
	ConfigurationSupported                     bool
	DynamicConfigurationSupported              bool
	DynamicRegistrationSemanticTokensSupported bool
	DynamicWatchedFilesSupported               bool
	RelativePatternsSupported                  bool
	PreferredContentFormat                     protocol.MarkupKind
	LineFoldingOnly                            bool
	HierarchicalDocumentSymbolSupport          bool
	SemanticTypes                              []string
	SemanticMods                               []string
	RelatedInformationSupported                bool
	CompletionTags                             bool
	CompletionDeprecated                       bool
	SupportedResourceOperations                []protocol.ResourceOperationKind
	CodeActionResolveOptions                   []string
}

// ServerOptions holds LSP-specific configuration that is provided by the
// server.
//
// ServerOptions must be comparable with reflect.DeepEqual.
type ServerOptions struct {
	SupportedCodeActions map[file.Kind]map[protocol.CodeActionKind]bool
	SupportedCommands    []string
}

// Note: BuildOptions must be comparable with reflect.DeepEqual.
type BuildOptions struct {
	// BuildFlags is the set of flags passed on to the build system when invoked.
	// It is applied to queries like `go list`, which is used when discovering files.
	// The most common use is to set `-tags`.
	BuildFlags []string

	// Env adds environment variables to external commands run by `gopls`, most notably `go list`.
	Env map[string]string

	// DirectoryFilters can be used to exclude unwanted directories from the
	// workspace. By default, all directories are included. Filters are an
	// operator, `+` to include and `-` to exclude, followed by a path prefix
	// relative to the workspace folder. They are evaluated in order, and
	// the last filter that applies to a path controls whether it is included.
	// The path prefix can be empty, so an initial `-` excludes everything.
	//
	// DirectoryFilters also supports the `**` operator to match 0 or more directories.
	//
	// Examples:
	//
	// Exclude node_modules at current depth: `-node_modules`
	//
	// Exclude node_modules at any depth: `-**/node_modules`
	//
	// Include only project_a: `-` (exclude everything), `+project_a`
	//
	// Include only project_a, but not node_modules inside it: `-`, `+project_a`, `-project_a/node_modules`
	DirectoryFilters []string

	// TemplateExtensions gives the extensions of file names that are treateed
	// as template files. (The extension
	// is the part of the file name after the final dot.)
	TemplateExtensions []string

	// obsolete, no effect
	MemoryMode string `status:"experimental"`

	// ExpandWorkspaceToModule determines which packages are considered
	// "workspace packages" when the workspace is using modules.
	//
	// Workspace packages affect the scope of workspace-wide operations. Notably,
	// gopls diagnoses all packages considered to be part of the workspace after
	// every keystroke, so by setting "ExpandWorkspaceToModule" to false, and
	// opening a nested workspace directory, you can reduce the amount of work
	// gopls has to do to keep your workspace up to date.
	ExpandWorkspaceToModule bool `status:"experimental"`

	// AllowModfileModifications disables -mod=readonly, allowing imports from
	// out-of-scope modules. This option will eventually be removed.
	AllowModfileModifications bool `status:"experimental"`

	// AllowImplicitNetworkAccess disables GOPROXY=off, allowing implicit module
	// downloads rather than requiring user action. This option will eventually
	// be removed.
	AllowImplicitNetworkAccess bool `status:"experimental"`

	// StandaloneTags specifies a set of build constraints that identify
	// individual Go source files that make up the entire main package of an
	// executable.
	//
	// A common example of standalone main files is the convention of using the
	// directive `//go:build ignore` to denote files that are not intended to be
	// included in any package, for example because they are invoked directly by
	// the developer using `go run`.
	//
	// Gopls considers a file to be a standalone main file if and only if it has
	// package name "main" and has a build directive of the exact form
	// "//go:build tag" or "// +build tag", where tag is among the list of tags
	// configured by this setting. Notably, if the build constraint is more
	// complicated than a simple tag (such as the composite constraint
	// `//go:build tag && go1.18`), the file is not considered to be a standalone
	// main file.
	//
	// This setting is only supported when gopls is built with Go 1.16 or later.
	StandaloneTags []string
}

// Note: UIOptions must be comparable with reflect.DeepEqual.
type UIOptions struct {
	DocumentationOptions
	CompletionOptions
	NavigationOptions
	DiagnosticOptions
	InlayHintOptions

	// Codelenses overrides the enabled/disabled state of code lenses. See the
	// "Code Lenses" section of the
	// [Settings page](https://github.com/golang/tools/blob/master/gopls/doc/settings.md#code-lenses)
	// for the list of supported lenses.
	//
	// Example Usage:
	//
	// ```json5
	// "gopls": {
	// ...
	//   "codelenses": {
	//     "generate": false,  // Don't show the `go generate` lens.
	//     "gc_details": true  // Show a code lens toggling the display of gc's choices.
	//   }
	// ...
	// }
	// ```
	Codelenses map[string]bool

	// SemanticTokens controls whether the LSP server will send
	// semantic tokens to the client.
	SemanticTokens bool `status:"experimental"`

	// NoSemanticString turns off the sending of the semantic token 'string'
	NoSemanticString bool `status:"experimental"`

	// NoSemanticNumber  turns off the sending of the semantic token 'number'
	NoSemanticNumber bool `status:"experimental"`
}

// Note: CompletionOptions must be comparable with reflect.DeepEqual.
type CompletionOptions struct {
	// Placeholders enables placeholders for function parameters or struct
	// fields in completion responses.
	UsePlaceholders bool

	// CompletionBudget is the soft latency goal for completion requests. Most
	// requests finish in a couple milliseconds, but in some cases deep
	// completions can take much longer. As we use up our budget we
	// dynamically reduce the search scope to ensure we return timely
	// results. Zero means unlimited.
	CompletionBudget time.Duration `status:"debug"`

	// Matcher sets the algorithm that is used when calculating completion
	// candidates.
	Matcher Matcher `status:"advanced"`

	// ExperimentalPostfixCompletions enables artificial method snippets
	// such as "someSlice.sort!".
	ExperimentalPostfixCompletions bool `status:"experimental"`

	// CompleteFunctionCalls enables function call completion.
	//
	// When completing a statement, or when a function return type matches the
	// expected of the expression being completed, completion may suggest call
	// expressions (i.e. may include parentheses).
	CompleteFunctionCalls bool
}

// Note: DocumentationOptions must be comparable with reflect.DeepEqual.
type DocumentationOptions struct {
	// HoverKind controls the information that appears in the hover text.
	// SingleLine and Structured are intended for use only by authors of editor plugins.
	HoverKind HoverKind

	// LinkTarget controls where documentation links go.
	// It might be one of:
	//
	// * `"godoc.org"`
	// * `"pkg.go.dev"`
	//
	// If company chooses to use its own `godoc.org`, its address can be used as well.
	//
	// Modules matching the GOPRIVATE environment variable will not have
	// documentation links in hover.
	LinkTarget string

	// LinksInHover toggles the presence of links to documentation in hover.
	LinksInHover bool
}

// Note: FormattingOptions must be comparable with reflect.DeepEqual.
type FormattingOptions struct {
	// Local is the equivalent of the `goimports -local` flag, which puts
	// imports beginning with this string after third-party packages. It should
	// be the prefix of the import path whose imports should be grouped
	// separately.
	Local string

	// Gofumpt indicates if we should run gofumpt formatting.
	Gofumpt bool
}

// Note: DiagnosticOptions must be comparable with reflect.DeepEqual.
type DiagnosticOptions struct {
	// Analyses specify analyses that the user would like to enable or disable.
	// A map of the names of analysis passes that should be enabled/disabled.
	// A full list of analyzers that gopls uses can be found in
	// [analyzers.md](https://github.com/golang/tools/blob/master/gopls/doc/analyzers.md).
	//
	// Example Usage:
	//
	// ```json5
	// ...
	// "analyses": {
	//   "unreachable": false, // Disable the unreachable analyzer.
	//   "unusedvariable": true  // Enable the unusedvariable analyzer.
	// }
	// ...
	// ```
	Analyses map[string]bool

	// Staticcheck enables additional analyses from staticcheck.io.
	// These analyses are documented on
	// [Staticcheck's website](https://staticcheck.io/docs/checks/).
	Staticcheck bool `status:"experimental"`

	// Annotations specifies the various kinds of optimization diagnostics
	// that should be reported by the gc_details command.
	Annotations map[Annotation]bool `status:"experimental"`

	// Vulncheck enables vulnerability scanning.
	Vulncheck VulncheckMode `status:"experimental"`

	// DiagnosticsDelay controls the amount of time that gopls waits
	// after the most recent file modification before computing deep diagnostics.
	// Simple diagnostics (parsing and type-checking) are always run immediately
	// on recently modified packages.
	//
	// This option must be set to a valid duration string, for example `"250ms"`.
	DiagnosticsDelay time.Duration `status:"advanced"`

	// DiagnosticsTrigger controls when to run diagnostics.
	DiagnosticsTrigger DiagnosticsTrigger `status:"experimental"`

	// AnalysisProgressReporting controls whether gopls sends progress
	// notifications when construction of its index of analysis facts is taking a
	// long time. Cancelling these notifications will cancel the indexing task,
	// though it will restart after the next change in the workspace.
	//
	// When a package is opened for the first time and heavyweight analyses such as
	// staticcheck are enabled, it can take a while to construct the index of
	// analysis facts for all its dependencies. The index is cached in the
	// filesystem, so subsequent analysis should be faster.
	AnalysisProgressReporting bool
}

type InlayHintOptions struct {
	// Hints specify inlay hints that users want to see. A full list of hints
	// that gopls uses can be found in
	// [inlayHints.md](https://github.com/golang/tools/blob/master/gopls/doc/inlayHints.md).
	Hints map[string]bool `status:"experimental"`
}

type NavigationOptions struct {
	// ImportShortcut specifies whether import statements should link to
	// documentation or go to definitions.
	ImportShortcut ImportShortcut

	// SymbolMatcher sets the algorithm that is used when finding workspace symbols.
	SymbolMatcher SymbolMatcher `status:"advanced"`

	// SymbolStyle controls how symbols are qualified in symbol responses.
	//
	// Example Usage:
	//
	// ```json5
	// "gopls": {
	// ...
	//   "symbolStyle": "Dynamic",
	// ...
	// }
	// ```
	SymbolStyle SymbolStyle `status:"advanced"`

	// SymbolScope controls which packages are searched for workspace/symbol
	// requests. The default value, "workspace", searches only workspace
	// packages. The legacy behavior, "all", causes all loaded packages to be
	// searched, including dependencies; this is more expensive and may return
	// unwanted results.
	SymbolScope SymbolScope
}

// UserOptions holds custom Gopls configuration (not part of the LSP) that is
// modified by the client.
//
// UserOptions must be comparable with reflect.DeepEqual.
type UserOptions struct {
	BuildOptions
	UIOptions
	FormattingOptions

	// VerboseOutput enables additional debug logging.
	VerboseOutput bool `status:"debug"`
}

// EnvSlice returns Env as a slice of k=v strings.
func (u *UserOptions) EnvSlice() []string {
	var result []string
	for k, v := range u.Env {
		result = append(result, fmt.Sprintf("%v=%v", k, v))
	}
	return result
}

// SetEnvSlice sets Env from a slice of k=v strings.
func (u *UserOptions) SetEnvSlice(env []string) {
	u.Env = map[string]string{}
	for _, kv := range env {
		split := strings.SplitN(kv, "=", 2)
		if len(split) != 2 {
			continue
		}
		u.Env[split[0]] = split[1]
	}
}

// InternalOptions contains settings that are not intended for use by the
// average user. These may be settings used by tests or outdated settings that
// will soon be deprecated. Some of these settings may not even be configurable
// by the user.
//
// TODO(rfindley): even though these settings are not intended for
// modification, some of them should be surfaced in our documentation.
type InternalOptions struct {
	// VerboseWorkDoneProgress controls whether the LSP server should send
	// progress reports for all work done outside the scope of an RPC.
	// Used by the regression tests.
	VerboseWorkDoneProgress bool

	// The following options were previously available to users, but they
	// really shouldn't be configured by anyone other than "power users".

	// CompletionDocumentation enables documentation with completion results.
	CompletionDocumentation bool

	// CompleteUnimported enables completion for packages that you do not
	// currently import.
	CompleteUnimported bool

	// DeepCompletion enables the ability to return completions from deep
	// inside relevant entities, rather than just the locally accessible ones.
	//
	// Consider this example:
	//
	// ```go
	// package main
	//
	// import "fmt"
	//
	// type wrapString struct {
	//     str string
	// }
	//
	// func main() {
	//     x := wrapString{"hello world"}
	//     fmt.Printf(<>)
	// }
	// ```
	//
	// At the location of the `<>` in this program, deep completion would suggest
	// the result `x.str`.
	DeepCompletion bool

	// ShowBugReports causes a message to be shown when the first bug is reported
	// on the server.
	// This option applies only during initialization.
	ShowBugReports bool

	// SubdirWatchPatterns configures the file watching glob patterns registered
	// by gopls.
	//
	// Some clients (namely VS Code) do not send workspace/didChangeWatchedFile
	// notifications for files contained in a directory when that directory is
	// deleted:
	// https://github.com/microsoft/vscode/issues/109754
	//
	// In this case, gopls would miss important notifications about deleted
	// packages. To work around this, gopls registers a watch pattern for each
	// directory containing Go files.
	//
	// Unfortunately, other clients experience performance problems with this
	// many watch patterns, so there is no single behavior that works well for
	// all clients.
	//
	// The "subdirWatchPatterns" setting allows configuring this behavior. Its
	// default value of "auto" attempts to guess the correct behavior based on
	// the client name. We'd love to avoid this specialization, but as described
	// above there is no single value that works for all clients.
	//
	// If any LSP client does not behave well with the default value (for
	// example, if like VS Code it drops file notifications), please file an
	// issue.
	SubdirWatchPatterns SubdirWatchPatterns

	// ReportAnalysisProgressAfter sets the duration for gopls to wait before starting
	// progress reporting for ongoing go/analysis passes.
	//
	// It is intended to be used for testing only.
	ReportAnalysisProgressAfter time.Duration

	// TelemetryPrompt controls whether gopls prompts about enabling Go telemetry.
	//
	// Once the prompt is answered, gopls doesn't ask again, but TelemetryPrompt
	// can prevent the question from ever being asked in the first place.
	TelemetryPrompt bool

	// LinkifyShowMessage controls whether the client wants gopls
	// to linkify links in showMessage. e.g. [go.dev](https://go.dev).
	LinkifyShowMessage bool

	// IncludeReplaceInWorkspace controls whether locally replaced modules in a
	// go.mod file are treated like workspace modules.
	// Or in other words, if a go.mod file with local replaces behaves like a
	// go.work file.
	IncludeReplaceInWorkspace bool

	// ZeroConfig enables the zero-config algorithm for workspace layout,
	// dynamically creating build configurations for different modules,
	// directories, and GOOS/GOARCH combinations to cover open files.
	ZeroConfig bool
}

type SubdirWatchPatterns string

const (
	SubdirWatchPatternsOn   SubdirWatchPatterns = "on"
	SubdirWatchPatternsOff  SubdirWatchPatterns = "off"
	SubdirWatchPatternsAuto SubdirWatchPatterns = "auto"
)

type ImportShortcut string

const (
	BothShortcuts      ImportShortcut = "Both"
	LinkShortcut       ImportShortcut = "Link"
	DefinitionShortcut ImportShortcut = "Definition"
)

func (s ImportShortcut) ShowLinks() bool {
	return s == BothShortcuts || s == LinkShortcut
}

func (s ImportShortcut) ShowDefinition() bool {
	return s == BothShortcuts || s == DefinitionShortcut
}

type Matcher string

const (
	Fuzzy           Matcher = "Fuzzy"
	CaseInsensitive Matcher = "CaseInsensitive"
	CaseSensitive   Matcher = "CaseSensitive"
)

// A SymbolMatcher controls the matching of symbols for workspace/symbol
// requests.
type SymbolMatcher string

const (
	SymbolFuzzy           SymbolMatcher = "Fuzzy"
	SymbolFastFuzzy       SymbolMatcher = "FastFuzzy"
	SymbolCaseInsensitive SymbolMatcher = "CaseInsensitive"
	SymbolCaseSensitive   SymbolMatcher = "CaseSensitive"
)

// A SymbolStyle controls the formatting of symbols in workspace/symbol results.
type SymbolStyle string

const (
	// PackageQualifiedSymbols is package qualified symbols i.e.
	// "pkg.Foo.Field".
	PackageQualifiedSymbols SymbolStyle = "Package"
	// FullyQualifiedSymbols is fully qualified symbols, i.e.
	// "path/to/pkg.Foo.Field".
	FullyQualifiedSymbols SymbolStyle = "Full"
	// DynamicSymbols uses whichever qualifier results in the highest scoring
	// match for the given symbol query. Here a "qualifier" is any "/" or "."
	// delimited suffix of the fully qualified symbol. i.e. "to/pkg.Foo.Field" or
	// just "Foo.Field".
	DynamicSymbols SymbolStyle = "Dynamic"
)

// A SymbolScope controls the search scope for workspace/symbol requests.
type SymbolScope string

const (
	// WorkspaceSymbolScope matches symbols in workspace packages only.
	WorkspaceSymbolScope SymbolScope = "workspace"
	// AllSymbolScope matches symbols in any loaded package, including
	// dependencies.
	AllSymbolScope SymbolScope = "all"
)

type HoverKind string

const (
	SingleLine            HoverKind = "SingleLine"
	NoDocumentation       HoverKind = "NoDocumentation"
	SynopsisDocumentation HoverKind = "SynopsisDocumentation"
	FullDocumentation     HoverKind = "FullDocumentation"

	// Structured is an experimental setting that returns a structured hover format.
	// This format separates the signature from the documentation, so that the client
	// can do more manipulation of these fields.
	//
	// This should only be used by clients that support this behavior.
	Structured HoverKind = "Structured"
)

type VulncheckMode string

const (
	// Disable vulnerability analysis.
	ModeVulncheckOff VulncheckMode = "Off"
	// In Imports mode, `gopls` will report vulnerabilities that affect packages
	// directly and indirectly used by the analyzed main module.
	ModeVulncheckImports VulncheckMode = "Imports"

	// TODO: VulncheckRequire, VulncheckCallgraph
)

type DiagnosticsTrigger string

const (
	// Trigger diagnostics on file edit and save. (default)
	DiagnosticsOnEdit DiagnosticsTrigger = "Edit"
	// Trigger diagnostics only on file save. Events like initial workspace load
	// or configuration change will still trigger diagnostics.
	DiagnosticsOnSave DiagnosticsTrigger = "Save"
	// TODO: support "Manual"?
)

type OptionResults []OptionResult

type OptionResult struct {
	Name  string
	Value any
	Error error
}

func SetOptions(options *Options, opts any) OptionResults {
	var results OptionResults
	switch opts := opts.(type) {
	case nil:
	case map[string]any:
		// If the user's settings contains "allExperiments", set that first,
		// and then let them override individual settings independently.
		var enableExperiments bool
		for name, value := range opts {
			if b, ok := value.(bool); name == "allExperiments" && ok && b {
				enableExperiments = true
				options.EnableAllExperiments()
			}
		}
		seen := map[string]struct{}{}
		for name, value := range opts {
			results = append(results, options.set(name, value, seen))
		}
		// Finally, enable any experimental features that are specified in
		// maps, which allows users to individually toggle them on or off.
		if enableExperiments {
			options.enableAllExperimentMaps()
		}
	default:
		results = append(results, OptionResult{
			Value: opts,
			Error: fmt.Errorf("Invalid options type %T", opts),
		})
	}
	return results
}

func (o *Options) ForClientCapabilities(clientName *protocol.ClientInfo, caps protocol.ClientCapabilities) {
	o.ClientInfo = clientName
	// Check if the client supports snippets in completion items.
	if caps.Workspace.WorkspaceEdit != nil {
		o.SupportedResourceOperations = caps.Workspace.WorkspaceEdit.ResourceOperations
	}
	if c := caps.TextDocument.Completion; c.CompletionItem.SnippetSupport {
		o.InsertTextFormat = protocol.SnippetTextFormat
	}
	// Check if the client supports configuration messages.
	o.ConfigurationSupported = caps.Workspace.Configuration
	o.DynamicConfigurationSupported = caps.Workspace.DidChangeConfiguration.DynamicRegistration
	o.DynamicRegistrationSemanticTokensSupported = caps.TextDocument.SemanticTokens.DynamicRegistration
	o.DynamicWatchedFilesSupported = caps.Workspace.DidChangeWatchedFiles.DynamicRegistration
	o.RelativePatternsSupported = caps.Workspace.DidChangeWatchedFiles.RelativePatternSupport

	// Check which types of content format are supported by this client.
	if hover := caps.TextDocument.Hover; hover != nil && len(hover.ContentFormat) > 0 {
		o.PreferredContentFormat = hover.ContentFormat[0]
	}
	// Check if the client supports only line folding.

	if fr := caps.TextDocument.FoldingRange; fr != nil {
		o.LineFoldingOnly = fr.LineFoldingOnly
	}
	// Check if the client supports hierarchical document symbols.
	o.HierarchicalDocumentSymbolSupport = caps.TextDocument.DocumentSymbol.HierarchicalDocumentSymbolSupport

	// Client's semantic tokens
	o.SemanticTypes = caps.TextDocument.SemanticTokens.TokenTypes
	o.SemanticMods = caps.TextDocument.SemanticTokens.TokenModifiers
	// we don't need Requests, as we support full functionality
	// we don't need Formats, as there is only one, for now

	// Check if the client supports diagnostic related information.
	o.RelatedInformationSupported = caps.TextDocument.PublishDiagnostics.RelatedInformation
	// Check if the client completion support includes tags (preferred) or deprecation
	if caps.TextDocument.Completion.CompletionItem.TagSupport != nil &&
		caps.TextDocument.Completion.CompletionItem.TagSupport.ValueSet != nil {
		o.CompletionTags = true
	} else if caps.TextDocument.Completion.CompletionItem.DeprecatedSupport {
		o.CompletionDeprecated = true
	}

	// Check if the client supports code actions resolving.
	if caps.TextDocument.CodeAction.DataSupport && caps.TextDocument.CodeAction.ResolveSupport != nil {
		o.CodeActionResolveOptions = caps.TextDocument.CodeAction.ResolveSupport.Properties
	}
}

func (o *Options) Clone() *Options {
	// TODO(rfindley): has this function gone stale? It appears that there are
	// settings that are incorrectly cloned here (such as TemplateExtensions).
	result := &Options{
		ClientOptions:   o.ClientOptions,
		InternalOptions: o.InternalOptions,
		ServerOptions:   o.ServerOptions,
		UserOptions:     o.UserOptions,
	}
	// Fully clone any slice or map fields. Only Hooks, ExperimentalOptions,
	// and UserOptions can be modified.
	copyStringMap := func(src map[string]bool) map[string]bool {
		dst := make(map[string]bool)
		for k, v := range src {
			dst[k] = v
		}
		return dst
	}
	result.Analyses = copyStringMap(o.Analyses)
	result.Codelenses = copyStringMap(o.Codelenses)

	copySlice := func(src []string) []string {
		dst := make([]string, len(src))
		copy(dst, src)
		return dst
	}
	result.SetEnvSlice(o.EnvSlice())
	result.BuildFlags = copySlice(o.BuildFlags)
	result.DirectoryFilters = copySlice(o.DirectoryFilters)
	result.StandaloneTags = copySlice(o.StandaloneTags)

	return result
}

// EnableAllExperiments turns on all of the experimental "off-by-default"
// features offered by gopls. Any experimental features specified in maps
// should be enabled in enableAllExperimentMaps.
func (o *Options) EnableAllExperiments() {
	o.SemanticTokens = true
}

func (o *Options) enableAllExperimentMaps() {
	if _, ok := o.Codelenses[string(command.GCDetails)]; !ok {
		o.Codelenses[string(command.GCDetails)] = true
	}
	if _, ok := o.Codelenses[string(command.RunGovulncheck)]; !ok {
		o.Codelenses[string(command.RunGovulncheck)] = true
	}
	if _, ok := o.Analyses[unusedvariable.Analyzer.Name]; !ok {
		o.Analyses[unusedvariable.Analyzer.Name] = true
	}
}

// validateDirectoryFilter validates if the filter string
// - is not empty
// - start with either + or -
// - doesn't contain currently unsupported glob operators: *, ?
func validateDirectoryFilter(ifilter string) (string, error) {
	filter := fmt.Sprint(ifilter)
	if filter == "" || (filter[0] != '+' && filter[0] != '-') {
		return "", fmt.Errorf("invalid filter %v, must start with + or -", filter)
	}
	segs := strings.Split(filter[1:], "/")
	unsupportedOps := [...]string{"?", "*"}
	for _, seg := range segs {
		if seg != "**" {
			for _, op := range unsupportedOps {
				if strings.Contains(seg, op) {
					return "", fmt.Errorf("invalid filter %v, operator %v not supported. If you want to have this operator supported, consider filing an issue.", filter, op)
				}
			}
		}
	}

	return strings.TrimRight(filepath.FromSlash(filter), "/"), nil
}

func (o *Options) set(name string, value interface{}, seen map[string]struct{}) OptionResult {
	// Flatten the name in case we get options with a hierarchy.
	split := strings.Split(name, ".")
	name = split[len(split)-1]

	result := OptionResult{Name: name, Value: value}
	if _, ok := seen[name]; ok {
		result.parseErrorf("duplicate configuration for %s", name)
	}
	seen[name] = struct{}{}

	switch name {
	case "env":
		menv, ok := value.(map[string]interface{})
		if !ok {
			result.parseErrorf("invalid type %T, expect map", value)
			break
		}
		if o.Env == nil {
			o.Env = make(map[string]string)
		}
		for k, v := range menv {
			o.Env[k] = fmt.Sprint(v)
		}

	case "buildFlags":
		// TODO(rfindley): use asStringSlice.
		iflags, ok := value.([]interface{})
		if !ok {
			result.parseErrorf("invalid type %T, expect list", value)
			break
		}
		flags := make([]string, 0, len(iflags))
		for _, flag := range iflags {
			flags = append(flags, fmt.Sprintf("%s", flag))
		}
		o.BuildFlags = flags

	case "directoryFilters":
		// TODO(rfindley): use asStringSlice.
		ifilters, ok := value.([]interface{})
		if !ok {
			result.parseErrorf("invalid type %T, expect list", value)
			break
		}
		var filters []string
		for _, ifilter := range ifilters {
			filter, err := validateDirectoryFilter(fmt.Sprintf("%v", ifilter))
			if err != nil {
				result.parseErrorf("%v", err)
				return result
			}
			filters = append(filters, strings.TrimRight(filepath.FromSlash(filter), "/"))
		}
		o.DirectoryFilters = filters

	case "memoryMode":
		result.deprecated("")
	case "completionDocumentation":
		result.setBool(&o.CompletionDocumentation)
	case "usePlaceholders":
		result.setBool(&o.UsePlaceholders)
	case "deepCompletion":
		result.setBool(&o.DeepCompletion)
	case "completeUnimported":
		result.setBool(&o.CompleteUnimported)
	case "completionBudget":
		result.setDuration(&o.CompletionBudget)
	case "matcher":
		if s, ok := result.asOneOf(
			string(Fuzzy),
			string(CaseSensitive),
			string(CaseInsensitive),
		); ok {
			o.Matcher = Matcher(s)
		}

	case "symbolMatcher":
		if s, ok := result.asOneOf(
			string(SymbolFuzzy),
			string(SymbolFastFuzzy),
			string(SymbolCaseInsensitive),
			string(SymbolCaseSensitive),
		); ok {
			o.SymbolMatcher = SymbolMatcher(s)
		}

	case "symbolStyle":
		if s, ok := result.asOneOf(
			string(FullyQualifiedSymbols),
			string(PackageQualifiedSymbols),
			string(DynamicSymbols),
		); ok {
			o.SymbolStyle = SymbolStyle(s)
		}

	case "symbolScope":
		if s, ok := result.asOneOf(
			string(WorkspaceSymbolScope),
			string(AllSymbolScope),
		); ok {
			o.SymbolScope = SymbolScope(s)
		}

	case "hoverKind":
		if s, ok := result.asOneOf(
			string(NoDocumentation),
			string(SingleLine),
			string(SynopsisDocumentation),
			string(FullDocumentation),
			string(Structured),
		); ok {
			o.HoverKind = HoverKind(s)
		}

	case "linkTarget":
		result.setString(&o.LinkTarget)

	case "linksInHover":
		result.setBool(&o.LinksInHover)

	case "importShortcut":
		if s, ok := result.asOneOf(string(BothShortcuts), string(LinkShortcut), string(DefinitionShortcut)); ok {
			o.ImportShortcut = ImportShortcut(s)
		}

	case "analyses":
		result.setBoolMap(&o.Analyses)

	case "hints":
		result.setBoolMap(&o.Hints)

	case "annotations":
		result.setAnnotationMap(&o.Annotations)

	case "vulncheck":
		if s, ok := result.asOneOf(
			string(ModeVulncheckOff),
			string(ModeVulncheckImports),
		); ok {
			o.Vulncheck = VulncheckMode(s)
		}

	case "codelenses", "codelens":
		var lensOverrides map[string]bool
		result.setBoolMap(&lensOverrides)
		if result.Error == nil {
			if o.Codelenses == nil {
				o.Codelenses = make(map[string]bool)
			}
			for lens, enabled := range lensOverrides {
				o.Codelenses[lens] = enabled
			}
		}

		// codelens is deprecated, but still works for now.
		// TODO(rstambler): Remove this for the gopls/v0.7.0 release.
		if name == "codelens" {
			result.deprecated("codelenses")
		}

	case "staticcheck":
		if v, ok := result.asBool(); ok {
			o.Staticcheck = v
			if v && !StaticcheckSupported {
				result.Error = fmt.Errorf("applying setting %q: staticcheck is not supported at %s;"+
					" rebuild gopls with a more recent version of Go", result.Name, runtime.Version())
			}
		}

	case "local":
		result.setString(&o.Local)

	case "verboseOutput":
		result.setBool(&o.VerboseOutput)

	case "verboseWorkDoneProgress":
		result.setBool(&o.VerboseWorkDoneProgress)

	case "tempModFile":
		result.deprecated("")

	case "showBugReports":
		result.setBool(&o.ShowBugReports)

	case "gofumpt":
		if v, ok := result.asBool(); ok {
			o.Gofumpt = v
			if v && !GofumptSupported {
				result.Error = fmt.Errorf("applying setting %q: gofumpt is not supported at %s;"+
					" rebuild gopls with a more recent version of Go", result.Name, runtime.Version())
			}
		}
	case "completeFunctionCalls":
		result.setBool(&o.CompleteFunctionCalls)

	case "semanticTokens":
		result.setBool(&o.SemanticTokens)

	case "noSemanticString":
		result.setBool(&o.NoSemanticString)

	case "noSemanticNumber":
		result.setBool(&o.NoSemanticNumber)

	case "expandWorkspaceToModule":
		// See golang/go#63536: we can consider deprecating
		// expandWorkspaceToModule, but probably need to change the default
		// behavior in that case to *not* expand to the module.
		result.setBool(&o.ExpandWorkspaceToModule)

	case "experimentalPostfixCompletions":
		result.setBool(&o.ExperimentalPostfixCompletions)

	case "experimentalWorkspaceModule":
		result.deprecated("")

	case "experimentalTemplateSupport": // TODO(pjw): remove after June 2022
		result.deprecated("")

	case "templateExtensions":
		if iexts, ok := value.([]interface{}); ok {
			ans := []string{}
			for _, x := range iexts {
				ans = append(ans, fmt.Sprint(x))
			}
			o.TemplateExtensions = ans
			break
		}
		if value == nil {
			o.TemplateExtensions = nil
			break
		}
		result.parseErrorf("unexpected type %T not []string", value)

	case "experimentalDiagnosticsDelay":
		result.deprecated("diagnosticsDelay")

	case "diagnosticsDelay":
		result.setDuration(&o.DiagnosticsDelay)

	case "diagnosticsTrigger":
		if s, ok := result.asOneOf(
			string(DiagnosticsOnEdit),
			string(DiagnosticsOnSave),
		); ok {
			o.DiagnosticsTrigger = DiagnosticsTrigger(s)
		}

	case "analysisProgressReporting":
		result.setBool(&o.AnalysisProgressReporting)

	case "experimentalWatchedFileDelay":
		result.deprecated("")

	case "experimentalPackageCacheKey":
		result.deprecated("")

	case "allowModfileModifications":
		result.softErrorf("gopls setting \"allowModfileModifications\" is deprecated.\nPlease comment on https://go.dev/issue/65546 if this impacts your workflow.")
		result.setBool(&o.AllowModfileModifications)

	case "allowImplicitNetworkAccess":
		result.setBool(&o.AllowImplicitNetworkAccess)

	case "experimentalUseInvalidMetadata":
		result.deprecated("")

	case "standaloneTags":
		result.setStringSlice(&o.StandaloneTags)

	case "allExperiments":
		// This setting should be handled before all of the other options are
		// processed, so do nothing here.

	case "newDiff":
		result.deprecated("")

	case "subdirWatchPatterns":
		if s, ok := result.asOneOf(
			string(SubdirWatchPatternsOn),
			string(SubdirWatchPatternsOff),
			string(SubdirWatchPatternsAuto),
		); ok {
			o.SubdirWatchPatterns = SubdirWatchPatterns(s)
		}

	case "reportAnalysisProgressAfter":
		result.setDuration(&o.ReportAnalysisProgressAfter)

	case "telemetryPrompt":
		result.setBool(&o.TelemetryPrompt)

	case "linkifyShowMessage":
		result.setBool(&o.LinkifyShowMessage)

	case "includeReplaceInWorkspace":
		result.setBool(&o.IncludeReplaceInWorkspace)

	case "zeroConfig":
		result.setBool(&o.ZeroConfig)

	// Replaced settings.
	case "experimentalDisabledAnalyses":
		result.deprecated("analyses")

	case "disableDeepCompletion":
		result.deprecated("deepCompletion")

	case "disableFuzzyMatching":
		result.deprecated("fuzzyMatching")

	case "wantCompletionDocumentation":
		result.deprecated("completionDocumentation")

	case "wantUnimportedCompletions":
		result.deprecated("completeUnimported")

	case "fuzzyMatching":
		result.deprecated("matcher")

	case "caseSensitiveCompletion":
		result.deprecated("matcher")

	// Deprecated settings.
	case "wantSuggestedFixes":
		result.deprecated("")

	case "noIncrementalSync":
		result.deprecated("")

	case "watchFileChanges":
		result.deprecated("")

	case "go-diff":
		result.deprecated("")

	default:
		result.unexpected()
	}
	return result
}

// parseErrorf reports an error parsing the current configuration value.
func (r *OptionResult) parseErrorf(msg string, values ...interface{}) {
	if false {
		_ = fmt.Sprintf(msg, values...) // this causes vet to check this like printf
	}
	prefix := fmt.Sprintf("parsing setting %q: ", r.Name)
	r.Error = fmt.Errorf(prefix+msg, values...)
}

// A SoftError is an error that does not affect the functionality of gopls.
type SoftError struct {
	msg string
}

func (e *SoftError) Error() string {
	return e.msg
}

// deprecated reports the current setting as deprecated. If 'replacement' is
// non-nil, it is suggested to the user.
func (r *OptionResult) deprecated(replacement string) {
	msg := fmt.Sprintf("gopls setting %q is deprecated", r.Name)
	if replacement != "" {
		msg = fmt.Sprintf("%s, use %q instead", msg, replacement)
	}
	r.Error = &SoftError{msg}
}

// softErrorf reports a soft error related to the current option.
func (r *OptionResult) softErrorf(format string, args ...any) {
	r.Error = &SoftError{fmt.Sprintf(format, args...)}
}

// unexpected reports that the current setting is not known to gopls.
func (r *OptionResult) unexpected() {
	r.Error = fmt.Errorf("unexpected gopls setting %q", r.Name)
}

func (r *OptionResult) asBool() (bool, bool) {
	b, ok := r.Value.(bool)
	if !ok {
		r.parseErrorf("invalid type %T, expect bool", r.Value)
		return false, false
	}
	return b, true
}

func (r *OptionResult) setBool(b *bool) {
	if v, ok := r.asBool(); ok {
		*b = v
	}
}

func (r *OptionResult) setDuration(d *time.Duration) {
	if v, ok := r.asString(); ok {
		parsed, err := time.ParseDuration(v)
		if err != nil {
			r.parseErrorf("failed to parse duration %q: %v", v, err)
			return
		}
		*d = parsed
	}
}

func (r *OptionResult) setBoolMap(bm *map[string]bool) {
	m := r.asBoolMap()
	*bm = m
}

func (r *OptionResult) setAnnotationMap(bm *map[Annotation]bool) {
	all := r.asBoolMap()
	if all == nil {
		return
	}
	// Default to everything enabled by default.
	m := make(map[Annotation]bool)
	for k, enabled := range all {
		a, err := asOneOf(
			k,
			string(Nil),
			string(Escape),
			string(Inline),
			string(Bounds),
		)
		if err != nil {
			// In case of an error, process any legacy values.
			switch k {
			case "noEscape":
				m[Escape] = false
				r.parseErrorf(`"noEscape" is deprecated, set "Escape: false" instead`)
			case "noNilcheck":
				m[Nil] = false
				r.parseErrorf(`"noNilcheck" is deprecated, set "Nil: false" instead`)
			case "noInline":
				m[Inline] = false
				r.parseErrorf(`"noInline" is deprecated, set "Inline: false" instead`)
			case "noBounds":
				m[Bounds] = false
				r.parseErrorf(`"noBounds" is deprecated, set "Bounds: false" instead`)
			default:
				r.parseErrorf("%v", err)
			}
			continue
		}
		m[Annotation(a)] = enabled
	}
	*bm = m
}

func (r *OptionResult) asBoolMap() map[string]bool {
	all, ok := r.Value.(map[string]interface{})
	if !ok {
		r.parseErrorf("invalid type %T for map[string]bool option", r.Value)
		return nil
	}
	m := make(map[string]bool)
	for a, enabled := range all {
		if e, ok := enabled.(bool); ok {
			m[a] = e
		} else {
			r.parseErrorf("invalid type %T for map key %q", enabled, a)
			return m
		}
	}
	return m
}

func (r *OptionResult) asString() (string, bool) {
	b, ok := r.Value.(string)
	if !ok {
		r.parseErrorf("invalid type %T, expect string", r.Value)
		return "", false
	}
	return b, true
}

func (r *OptionResult) asStringSlice() ([]string, bool) {
	iList, ok := r.Value.([]interface{})
	if !ok {
		r.parseErrorf("invalid type %T, expect list", r.Value)
		return nil, false
	}
	var list []string
	for _, elem := range iList {
		s, ok := elem.(string)
		if !ok {
			r.parseErrorf("invalid element type %T, expect string", elem)
			return nil, false
		}
		list = append(list, s)
	}
	return list, true
}

func (r *OptionResult) asOneOf(options ...string) (string, bool) {
	s, ok := r.asString()
	if !ok {
		return "", false
	}
	s, err := asOneOf(s, options...)
	if err != nil {
		r.parseErrorf("%v", err)
	}
	return s, err == nil
}

func asOneOf(str string, options ...string) (string, error) {
	lower := strings.ToLower(str)
	for _, opt := range options {
		if strings.ToLower(opt) == lower {
			return opt, nil
		}
	}
	return "", fmt.Errorf("invalid option %q for enum", str)
}

func (r *OptionResult) setString(s *string) {
	if v, ok := r.asString(); ok {
		*s = v
	}
}

func (r *OptionResult) setStringSlice(s *[]string) {
	if v, ok := r.asStringSlice(); ok {
		*s = v
	}
}
