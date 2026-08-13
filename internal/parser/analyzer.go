// Package parser defines the common LanguageAnalyzer interface used to
// extract symbols and relationships from source files. Adding support for a
// new language means implementing this interface, not modifying the graph
// builder or any other core package.
package parser

// SymbolKind identifies the kind of entity a Symbol represents. It mirrors
// graph.NodeKind but lives here to avoid an import cycle (graph imports
// parser to build the graph from analyzer output); the graph builder maps
// SymbolKind -> graph.NodeKind when constructing nodes.
type SymbolKind string

const (
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindType      SymbolKind = "type"
	KindClass     SymbolKind = "class"
	KindInterface SymbolKind = "interface"
	KindEndpoint  SymbolKind = "endpoint"
)

// Symbol is a code entity extracted from a single file: a function, method,
// type, class, interface, or an HTTP endpoint recognized by convention
// (e.g. an Express route handler or an ASP.NET controller action).
type Symbol struct {
	Kind       SymbolKind
	Name       string // simple name, e.g. "CreateUser"
	Qualified  string // qualified name used to build the node ID, e.g. "UserService.CreateUser"
	StartLine  int
	EndLine    int
	Signature  string
	DocComment string
	Calls      []string // qualified/simple names of symbols this symbol calls (resolved later by the builder)
	Implements []string // interface/base names this type implements or extends
}

// Import is a dependency declared by a file on another file/module.
type Import struct {
	Path string // as written in source, e.g. "./user.service" or "github.com/foo/bar"
	Line int
}

// FileAnalysis is everything extracted from a single source file.
type FileAnalysis struct {
	Path     string // repo-relative path
	Language string
	Symbols  []Symbol
	Imports  []Import
}

// LanguageAnalyzer extracts symbols and imports from source files of one
// language. Implementations must be safe to reuse across files (no
// per-file mutable state kept between calls).
type LanguageAnalyzer interface {
	// Language returns the identifier used for this analyzer, e.g. "go",
	// "typescript".
	Language() string

	// Extensions returns the file extensions (including the leading dot)
	// this analyzer handles, e.g. []string{".go"}.
	Extensions() []string

	// Analyze parses a single file's contents and returns the symbols and
	// imports found in it. path is the repo-relative path, used to build
	// stable node IDs.
	Analyze(path string, content []byte) (*FileAnalysis, error)
}

// Registry maps file extensions to the analyzer responsible for them.
type Registry struct {
	byExt map[string]LanguageAnalyzer
}

// NewRegistry builds a Registry from a list of analyzers.
func NewRegistry(analyzers ...LanguageAnalyzer) *Registry {
	r := &Registry{byExt: make(map[string]LanguageAnalyzer)}
	for _, a := range analyzers {
		for _, ext := range a.Extensions() {
			r.byExt[ext] = a
		}
	}
	return r
}

// For returns the analyzer registered for a file extension, if any.
func (r *Registry) For(ext string) (LanguageAnalyzer, bool) {
	a, ok := r.byExt[ext]
	return a, ok
}
