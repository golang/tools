package protocol

type PackageLocator struct {
	Version string `json:",omitempty"`
	Name    string
	RepoURI string `json:"uri"`
}

// SymbolLocator is the response type for the `textDocument/edefinition` extension.
type SymbolLocator struct {
	// The fully qualified name of the symbol.
	Qname string

	Kind SymbolKind

	// The file path relative to the repo root URI of the specified symbol.
	Path string

	// A concrete location at which the definition is located.
	Loc Location `json:"location,omitempty"`

	Package PackageLocator
}
