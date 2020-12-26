// Code generated by "golang.org/x/tools/gopls/doc/generate"; DO NOT EDIT.

package source

var GeneratedAPIJSON = &APIJSON{
	Options: map[string][]*OptionJSON{
		"Debugging": {
			{
				Name:       "verboseOutput",
				Type:       "bool",
				Doc:        "verboseOutput enables additional debug logging.\n",
				EnumValues: nil,
				Default:    "false",
			},
			{
				Name:       "completionBudget",
				Type:       "time.Duration",
				Doc:        "completionBudget is the soft latency goal for completion requests. Most\nrequests finish in a couple milliseconds, but in some cases deep\ncompletions can take much longer. As we use up our budget we\ndynamically reduce the search scope to ensure we return timely\nresults. Zero means unlimited.\n",
				EnumValues: nil,
				Default:    "\"100ms\"",
			},
		},
		"Experimental": {
			{
				Name: "annotations",
				Type: "map[string]bool",
				Doc:  "annotations specifies the various kinds of optimization diagnostics\nthat should be reported by the gc_details command.\n",
				EnumValues: []EnumValue{
					{
						Value: "\"bounds\"",
						Doc:   "`\"bounds\"` controls bounds checking diagnostics.\n",
					},
					{
						Value: "\"escape\"",
						Doc:   "`\"escape\"` controls diagnostics about escape choices.\n",
					},
					{
						Value: "\"inline\"",
						Doc:   "`\"inline\"` controls diagnostics about inlining choices.\n",
					},
					{
						Value: "\"nil\"",
						Doc:   "`\"nil\"` controls nil checks.\n",
					},
				},
				Default: "{\"bounds\":true,\"escape\":true,\"inline\":true,\"nil\":true}",
			},
			{
				Name:       "staticcheck",
				Type:       "bool",
				Doc:        "staticcheck enables additional analyses from staticcheck.io.\n",
				EnumValues: nil,
				Default:    "false",
			},
			{
				Name:       "semanticTokens",
				Type:       "bool",
				Doc:        "semanticTokens controls whether the LSP server will send\nsemantic tokens to the client.\n",
				EnumValues: nil,
				Default:    "false",
			},
			{
				Name:       "expandWorkspaceToModule",
				Type:       "bool",
				Doc:        "expandWorkspaceToModule instructs `gopls` to adjust the scope of the\nworkspace to find the best available module root. `gopls` first looks for\na go.mod file in any parent directory of the workspace folder, expanding\nthe scope to that directory if it exists. If no viable parent directory is\nfound, gopls will check if there is exactly one child directory containing\na go.mod file, narrowing the scope to that directory if it exists.\n",
				EnumValues: nil,
				Default:    "true",
			},
			{
				Name:       "experimentalWorkspaceModule",
				Type:       "bool",
				Doc:        "experimentalWorkspaceModule opts a user into the experimental support\nfor multi-module workspaces.\n",
				EnumValues: nil,
				Default:    "false",
			},
			{
				Name:       "experimentalDiagnosticsDelay",
				Type:       "time.Duration",
				Doc:        "experimentalDiagnosticsDelay controls the amount of time that gopls waits\nafter the most recent file modification before computing deep diagnostics.\nSimple diagnostics (parsing and type-checking) are always run immediately\non recently modified packages.\n\nThis option must be set to a valid duration string, for example `\"250ms\"`.\n",
				EnumValues: nil,
				Default:    "\"250ms\"",
			},
			{
				Name:       "experimentalPackageCacheKey",
				Type:       "bool",
				Doc:        "experimentalPackageCacheKey controls whether to use a coarser cache key\nfor package type information to increase cache hits. This setting removes\nthe user's environment, build flags, and working directory from the cache\nkey, which should be a safe change as all relevant inputs into the type\nchecking pass are already hashed into the key. This is temporarily guarded\nby an experiment because caching behavior is subtle and difficult to\ncomprehensively test.\n",
				EnumValues: nil,
				Default:    "true",
			},
			{
				Name:       "allowModfileModifications",
				Type:       "bool",
				Doc:        "allowModfileModifications disables -mod=readonly, allowing imports from\nout-of-scope modules. This option will eventually be removed.\n",
				EnumValues: nil,
				Default:    "false",
			},
			{
				Name:       "allowImplicitNetworkAccess",
				Type:       "bool",
				Doc:        "allowImplicitNetworkAccess disables GOPROXY=off, allowing implicit module\ndownloads rather than requiring user action. This option will eventually\nbe removed.\n",
				EnumValues: nil,
				Default:    "false",
			},
		},
		"User": {
			{
				Name:       "buildFlags",
				Type:       "[]string",
				Doc:        "buildFlags is the set of flags passed on to the build system when invoked.\nIt is applied to queries like `go list`, which is used when discovering files.\nThe most common use is to set `-tags`.\n",
				EnumValues: nil,
				Default:    "[]",
			},
			{
				Name:       "env",
				Type:       "map[string]string",
				Doc:        "env adds environment variables to external commands run by `gopls`, most notably `go list`.\n",
				EnumValues: nil,
				Default:    "{}",
			},
			{
				Name: "hoverKind",
				Type: "enum",
				Doc:  "hoverKind controls the information that appears in the hover text.\nSingleLine and Structured are intended for use only by authors of editor plugins.\n",
				EnumValues: []EnumValue{
					{
						Value: "\"FullDocumentation\"",
						Doc:   "",
					},
					{
						Value: "\"NoDocumentation\"",
						Doc:   "",
					},
					{
						Value: "\"SingleLine\"",
						Doc:   "",
					},
					{
						Value: "\"Structured\"",
						Doc:   "`\"Structured\"` is an experimental setting that returns a structured hover format.\nThis format separates the signature from the documentation, so that the client\ncan do more manipulation of these fields.\n\nThis should only be used by clients that support this behavior.\n",
					},
					{
						Value: "\"SynopsisDocumentation\"",
						Doc:   "",
					},
				},
				Default: "\"FullDocumentation\"",
			},
			{
				Name:       "usePlaceholders",
				Type:       "bool",
				Doc:        "placeholders enables placeholders for function parameters or struct fields in completion responses.\n",
				EnumValues: nil,
				Default:    "false",
			},
			{
				Name:       "linkTarget",
				Type:       "string",
				Doc:        "linkTarget controls where documentation links go.\nIt might be one of:\n\n* `\"godoc.org\"`\n* `\"pkg.go.dev\"`\n\nIf company chooses to use its own `godoc.org`, its address can be used as well.\n",
				EnumValues: nil,
				Default:    "\"pkg.go.dev\"",
			},
			{
				Name:       "local",
				Type:       "string",
				Doc:        "local is the equivalent of the `goimports -local` flag, which puts imports beginning with this string after 3rd-party packages.\nIt should be the prefix of the import path whose imports should be grouped separately.\n",
				EnumValues: nil,
				Default:    "\"\"",
			},
			{
				Name:       "gofumpt",
				Type:       "bool",
				Doc:        "gofumpt indicates if we should run gofumpt formatting.\n",
				EnumValues: nil,
				Default:    "false",
			},
			{
				Name:       "analyses",
				Type:       "map[string]bool",
				Doc:        "analyses specify analyses that the user would like to enable or disable.\nA map of the names of analysis passes that should be enabled/disabled.\nA full list of analyzers that gopls uses can be found [here](analyzers.md)\n\nExample Usage:\n```json5\n...\n\"analyses\": {\n  \"unreachable\": false, // Disable the unreachable analyzer.\n  \"unusedparams\": true  // Enable the unusedparams analyzer.\n}\n...\n```\n",
				EnumValues: nil,
				Default:    "{}",
			},
			{
				Name:       "codelenses",
				Type:       "map[string]bool",
				Doc:        "codelenses overrides the enabled/disabled state of code lenses. See the \"Code Lenses\"\nsection of settings.md for the list of supported lenses.\n\nExample Usage:\n```json5\n\"gopls\": {\n...\n  \"codelenses\": {\n    \"generate\": false,  // Don't show the `go generate` lens.\n    \"gc_details\": true  // Show a code lens toggling the display of gc's choices.\n  }\n...\n}\n```\n",
				EnumValues: nil,
				Default:    "{\"gc_details\":false,\"generate\":true,\"regenerate_cgo\":true,\"tidy\":true,\"upgrade_dependency\":true,\"vendor\":true}",
			},
			{
				Name:       "linksInHover",
				Type:       "bool",
				Doc:        "linksInHover toggles the presence of links to documentation in hover.\n",
				EnumValues: nil,
				Default:    "true",
			},
			{
				Name: "importShortcut",
				Type: "enum",
				Doc:  "importShortcut specifies whether import statements should link to\ndocumentation or go to definitions.\n",
				EnumValues: []EnumValue{
					{
						Value: "\"Both\"",
						Doc:   "",
					},
					{
						Value: "\"Definition\"",
						Doc:   "",
					},
					{
						Value: "\"Link\"",
						Doc:   "",
					},
				},
				Default: "\"Both\"",
			},
			{
				Name: "matcher",
				Type: "enum",
				Doc:  "matcher sets the algorithm that is used when calculating completion candidates.\n",
				EnumValues: []EnumValue{
					{
						Value: "\"CaseInsensitive\"",
						Doc:   "",
					},
					{
						Value: "\"CaseSensitive\"",
						Doc:   "",
					},
					{
						Value: "\"Fuzzy\"",
						Doc:   "",
					},
				},
				Default: "\"Fuzzy\"",
			},
			{
				Name: "symbolMatcher",
				Type: "enum",
				Doc:  "symbolMatcher sets the algorithm that is used when finding workspace symbols.\n",
				EnumValues: []EnumValue{
					{
						Value: "\"CaseInsensitive\"",
						Doc:   "",
					},
					{
						Value: "\"CaseSensitive\"",
						Doc:   "",
					},
					{
						Value: "\"Fuzzy\"",
						Doc:   "",
					},
				},
				Default: "\"Fuzzy\"",
			},
			{
				Name: "symbolStyle",
				Type: "enum",
				Doc:  "symbolStyle controls how symbols are qualified in symbol responses.\n\nExample Usage:\n```json5\n\"gopls\": {\n...\n  \"symbolStyle\": \"dynamic\",\n...\n}\n```\n",
				EnumValues: []EnumValue{
					{
						Value: "\"Dynamic\"",
						Doc:   "`\"Dynamic\"` uses whichever qualifier results in the highest scoring\nmatch for the given symbol query. Here a \"qualifier\" is any \"/\" or \".\"\ndelimited suffix of the fully qualified symbol. i.e. \"to/pkg.Foo.Field\" or\njust \"Foo.Field\".\n",
					},
					{
						Value: "\"Full\"",
						Doc:   "`\"Full\"` is fully qualified symbols, i.e.\n\"path/to/pkg.Foo.Field\".\n",
					},
					{
						Value: "\"Package\"",
						Doc:   "`\"Package\"` is package qualified symbols i.e.\n\"pkg.Foo.Field\".\n",
					},
				},
				Default: "\"Dynamic\"",
			},
			{
				Name:       "directoryFilters",
				Type:       "[]string",
				Doc:        "directoryFilters can be used to exclude unwanted directories from the\nworkspace. By default, all directories are included. Filters are an\noperator, `+` to include and `-` to exclude, followed by a path prefix\nrelative to the workspace folder. They are evaluated in order, and\nthe last filter that applies to a path controls whether it is included.\nThe path prefix can be empty, so an initial `-` excludes everything.\n\nExamples:\nExclude node_modules: `-node_modules`\nInclude only project_a: `-` (exclude everything), `+project_a`\nInclude only project_a, but not node_modules inside it: `-`, `+project_a`, `-project_a/node_modules`\n",
				EnumValues: nil,
				Default:    "[]",
			},
		},
	},
	Commands: []*CommandJSON{
		{
			Command: "gopls.generate",
			Title:   "Run go generate",
			Doc:     "generate runs `go generate` for a given directory.\n",
		},
		{
			Command: "gopls.fill_struct",
			Title:   "Fill struct",
			Doc:     "fill_struct is a gopls command to fill a struct with default\nvalues.\n",
		},
		{
			Command: "gopls.regenerate_cgo",
			Title:   "Regenerate cgo",
			Doc:     "regenerate_cgo regenerates cgo definitions.\n",
		},
		{
			Command: "gopls.test",
			Title:   "Run test(s)",
			Doc:     "test runs `go test` for a specific test function.\n",
		},
		{
			Command: "gopls.tidy",
			Title:   "Run go mod tidy",
			Doc:     "tidy runs `go mod tidy` for a module.\n",
		},
		{
			Command: "gopls.update_go_sum",
			Title:   "Update go.sum",
			Doc:     "update_go_sum updates the go.sum file for a module.\n",
		},
		{
			Command: "gopls.undeclared_name",
			Title:   "Undeclared name",
			Doc:     "undeclared_name adds a variable declaration for an undeclared\nname.\n",
		},
		{
			Command: "gopls.go_get_package",
			Title:   "go get package",
			Doc:     "go_get_package runs `go get` to fetch a package.\n",
		},
		{
			Command: "gopls.add_dependency",
			Title:   "Add dependency",
			Doc:     "add_dependency adds a dependency.\n",
		},
		{
			Command: "gopls.upgrade_dependency",
			Title:   "Upgrade dependency",
			Doc:     "upgrade_dependency upgrades a dependency.\n",
		},
		{
			Command: "gopls.remove_dependency",
			Title:   "Remove dependency",
			Doc:     "remove_dependency removes a dependency.\n",
		},
		{
			Command: "gopls.vendor",
			Title:   "Run go mod vendor",
			Doc:     "vendor runs `go mod vendor` for a module.\n",
		},
		{
			Command: "gopls.extract_variable",
			Title:   "Extract to variable",
			Doc:     "extract_variable extracts an expression to a variable.\n",
		},
		{
			Command: "gopls.extract_function",
			Title:   "Extract to function",
			Doc:     "extract_function extracts statements to a function.\n",
		},
		{
			Command: "gopls.gc_details",
			Title:   "Toggle gc_details",
			Doc:     "gc_details controls calculation of gc annotations.\n",
		},
		{
			Command: "gopls.generate_gopls_mod",
			Title:   "Generate gopls.mod",
			Doc:     "generate_gopls_mod (re)generates the gopls.mod file.\n",
		},
	},
	Lenses: []*LensJSON{
		{
			Lens:  "generate",
			Title: "Run go generate",
			Doc:   "generate runs `go generate` for a given directory.\n",
		},
		{
			Lens:  "regenerate_cgo",
			Title: "Regenerate cgo",
			Doc:   "regenerate_cgo regenerates cgo definitions.\n",
		},
		{
			Lens:  "test",
			Title: "Run test(s)",
			Doc:   "test runs `go test` for a specific test function.\n",
		},
		{
			Lens:  "tidy",
			Title: "Run go mod tidy",
			Doc:   "tidy runs `go mod tidy` for a module.\n",
		},
		{
			Lens:  "upgrade_dependency",
			Title: "Upgrade dependency",
			Doc:   "upgrade_dependency upgrades a dependency.\n",
		},
		{
			Lens:  "vendor",
			Title: "Run go mod vendor",
			Doc:   "vendor runs `go mod vendor` for a module.\n",
		},
		{
			Lens:  "gc_details",
			Title: "Toggle gc_details",
			Doc:   "gc_details controls calculation of gc annotations.\n",
		},
	},
}
