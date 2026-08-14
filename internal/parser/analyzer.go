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

// CallRef is one call target found inside a symbol's body: the simple name
// being invoked, plus — when the analyzer could work it out from a local
// variable/field declaration — the declared type of the receiver
// (`ws.SendAsync()` with `ws` declared as `ClientWebSocket` -> Name:
// "SendAsync", ReceiverType: "ClientWebSocket"). The graph builder uses
// ReceiverType two ways: to route interface-typed calls to the right
// implementing class (via the Implements edges already captured), and to
// refuse to resolve a call whose receiver type isn't a symbol in the repo
// at all (e.g. a BCL/stdlib/vendor type) even if the method name happens
// to collide with one of the user's own methods. ReceiverType is "" when
// the analyzer couldn't determine it — the builder then falls back to the
// old bare-name heuristic (same-file preference, drop if ambiguous).
type CallRef struct {
	Name         string
	ReceiverType string
	// ReceiverVar is the receiver's raw identifier name — set only when
	// ReceiverType couldn't be determined locally (ReceiverType == "")
	// but the receiver is at least a plain variable name the builder
	// might resolve another way: a module-level singleton constructed in
	// a *different* file (`chat_service = ChatService()` at module scope,
	// imported and called elsewhere as `chat_service.create_assistant()`)
	// isn't visible to a single-file analyzer pass, but the graph builder
	// sees every file's module-level vars at once (Builder.moduleVarTypes)
	// and can resolve it there — precisely, and only when that variable
	// name is unambiguous repo-wide, same ambiguity policy as everywhere
	// else in this package. Both ReceiverType and ReceiverVar empty means
	// a genuinely receiver-less call (bare `foo()`), which the builder
	// resolves with the plain bare-name heuristic.
	ReceiverVar string
	// Line is the 1-based source line the call appears on, used by the
	// graph builder to interleave Calls and Creates back into the order
	// they're actually written in — the two are collected into separate
	// slices during extraction (see CreateRef), so without a position to
	// re-sort by, edges would always come out "all calls, then all
	// creates" regardless of how they're interleaved in the source.
	Line int
}

// CreateRef is one `new Foo()` found inside a symbol's body: the
// instantiated type's name, plus its source line — see CallRef.Line for
// why the line matters.
type CreateRef struct {
	TypeName string
	Line     int
}

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
	Calls      []CallRef   // symbols this symbol invokes (resolved later by the builder)
	Creates    []CreateRef // types this symbol instantiates via `new` (kept separate from Calls: constructing a value is a different relationship than invoking a method on one)
	Implements []string    // interface/base names this type implements or extends
}

// Import is a dependency declared by a file on another file/module.
type Import struct {
	Path string // as written in source, e.g. "./user.service" or "github.com/foo/bar"
	Line int
}

// ModuleVar is a module/file-scope variable assignment whose type could be
// determined syntactically — `chat_service = ChatService()` at the top
// level of a Python module, for instance. See CallRef.ReceiverVar for how
// the graph builder uses these across files.
type ModuleVar struct {
	Name     string
	TypeName string
}

// FileAnalysis is everything extracted from a single source file.
type FileAnalysis struct {
	Path       string // repo-relative path
	Language   string
	Symbols    []Symbol
	Imports    []Import
	ModuleVars []ModuleVar // module-scope variable -> type, when inferable; nil for languages/files where this doesn't apply
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
