// Package graph defines the code graph model: nodes (files, symbols) and
// edges (imports, calls, implements) extracted from a repository, plus the
// deterministic queries used to answer structural questions without an LLM.
package graph

// NodeKind identifies the kind of entity a graph node represents.
type NodeKind string

const (
	NodeFile      NodeKind = "file"
	NodeFunction  NodeKind = "function"
	NodeMethod    NodeKind = "method"
	NodeType      NodeKind = "type"
	NodeClass     NodeKind = "class"
	NodeInterface NodeKind = "interface"
	NodeEndpoint  NodeKind = "endpoint"
	// NodeGuard identifies a synthetic node representing one distinct auth
	// guard protecting an endpoint — e.g. "Authorize(Roles=admin)" or
	// "ApiKeyAuthorize(N8N)". It's never produced by a LanguageAnalyzer or
	// persisted by Store: internal/security.Rule implementations build
	// these in memory, straight off Node.Attrs/EdgeImplements, every time
	// a query runs — see EdgeGuardedBy.
	NodeGuard NodeKind = "guard"
)

// EdgeKind identifies the kind of relationship a graph edge represents.
type EdgeKind string

const (
	EdgeImports      EdgeKind = "imports"
	EdgeCalls        EdgeKind = "calls"
	EdgeInstantiates EdgeKind = "instantiates" // `new Foo()` — distinct from calls so diagrams don't conflate constructing a value with invoking a method on one
	EdgeImplements   EdgeKind = "implements"
	EdgeDefines      EdgeKind = "defines" // file defines symbol
	EdgeExtends      EdgeKind = "extends"
	EdgeReferences   EdgeKind = "references"
	// EdgeGuardedBy connects an endpoint to a NodeGuard that protects it —
	// "endpoint --guardedBy--> guard". Like NodeGuard, this is never added
	// to a Graph by the Builder or persisted by Store — the Builder only
	// records raw source structure (Node.Attrs); deciding which attribute
	// counts as a guard, and materializing that decision as this edge kind,
	// is entirely internal/security's job, computed fresh from the loaded
	// graph on every query. See internal/security.Rule.
	EdgeGuardedBy EdgeKind = "guarded_by"
)

// Attr is one attribute/decorator/annotation attached to a node exactly as
// found in source — a C# `[Authorize(Roles = "admin")]`, a Python
// `@login_required`, a future TS `@UseGuards(...)`, etc. It mirrors
// parser.AttrRef (kept as its own type here, the same way NodeKind mirrors
// parser.SymbolKind, to avoid a graph->parser type coupling); the Builder
// copies it over verbatim, with zero opinion about what any of it means.
//
// This is the one deliberate seam between "what the parser saw" and "what
// counts as an auth guard": a LanguageAnalyzer's only job is to report Attrs
// truthfully; deciding whether a given Attr is a guard is entirely up to
// internal/security.Rule implementations, which read Attrs back off the
// built graph. Adding a new kind of guard (a new framework, a new team
// convention) never means touching Node, the Builder, or an existing Rule.
type Attr struct {
	Name string            `json:"name"`           // e.g. "Authorize", "AllowAnonymous", "login_required"
	Args map[string]string `json:"args,omitempty"` // named args (Roles=, Policy=, ...); positional args under key ""
	Line int               `json:"line"`
}

// Node is a single entity in the code graph.
type Node struct {
	ID   string   `json:"id"` // stable id: "<file>::<qualified name>" or "<file>"
	Kind NodeKind `json:"kind"`
	Name string   `json:"name"` // simple name, e.g. "GetAllAsync" — ambiguous across classes/services/repositories, so diagrams should prefer Qualified
	// Qualified is the class/type-scoped name, e.g. "AppService.GetAllAsync"
	// — for a method/function that's Name prefixed with its enclosing
	// type; for a type/class/interface/endpoint it equals Name. Diagram
	// rendering uses this instead of Name so e.g. a service and the
	// repository it calls, which often share a bare method name, don't
	// render as if the same node called itself.
	Qualified  string `json:"qualified"`
	File       string `json:"file"` // repo-relative path
	Language   string `json:"language"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Signature  string `json:"signature,omitempty"`
	DocComment string `json:"doc_comment,omitempty"`
	// Attrs are this node's raw attributes/decorators, as reported by the
	// LanguageAnalyzer. See the Attr doc comment for why this exists.
	Attrs []Attr `json:"attrs,omitempty"`
	// Implements is the raw list of interface/base names this node declared
	// itself as implementing/extending, exactly as written in source —
	// copied verbatim from parser.Symbol.Implements, same as Attrs. This is
	// deliberately *not* the same thing as EdgeImplements: EdgeImplements
	// only exists once the Builder has resolved a name to another node in
	// this repo, so a base/interface that lives outside it (a framework
	// type like ASP.NET's IAuthorizationFilter, never itself a node) has no
	// edge — but a security.Rule may still need to know the name was
	// declared, which this field preserves regardless of whether it
	// resolved to anything.
	Implements []string `json:"implements,omitempty"`
	// WrapsType is set only for a class-like node that wraps another type
	// the way C#'s `TypeFilterAttribute` subclasses do (`class
	// ApiKeyAuthorizeAttribute : TypeFilterAttribute` constructed with
	// `base(typeof(ApiKeyAuthorizeFilter))`) — the wrapped type's simple
	// name, i.e. "ApiKeyAuthorizeFilter". This is what lets an
	// auth-detection Rule recognize a *custom* guard attribute generically,
	// by checking whether the type it wraps implements IAuthorizationFilter
	// (via the ordinary EdgeImplements edges), instead of hardcoding the
	// attribute's name.
	WrapsType string `json:"wraps_type,omitempty"`
}

// Edge is a directed relationship between two nodes.
type Edge struct {
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
}

// Graph is an in-memory representation of the code graph, with adjacency
// indexes for fast traversal. It is the unit built by parser/graph builder
// and persisted by Store.
type Graph struct {
	Nodes map[string]*Node
	Edges []*Edge

	out map[string][]*Edge // outgoing edges indexed by node id
	in  map[string][]*Edge // incoming edges indexed by node id
}

// New returns an empty Graph ready for AddNode/AddEdge.
func New() *Graph {
	return &Graph{
		Nodes: make(map[string]*Node),
		out:   make(map[string][]*Edge),
		in:    make(map[string][]*Edge),
	}
}

// AddNode inserts or overwrites a node by ID.
func (g *Graph) AddNode(n *Node) {
	if n == nil || n.ID == "" {
		return
	}
	g.Nodes[n.ID] = n
}

// AddEdge inserts a directed edge. Both endpoints do not need to already
// exist as nodes (dangling references are tolerated and simply won't
// resolve during traversal).
func (g *Graph) AddEdge(from, to string, kind EdgeKind) {
	if from == "" || to == "" {
		return
	}
	e := &Edge{From: from, To: to, Kind: kind}
	g.Edges = append(g.Edges, e)
	g.out[from] = append(g.out[from], e)
	g.in[to] = append(g.in[to], e)
}

// Out returns outgoing edges from a node, optionally filtered by kind.
func (g *Graph) Out(id string, kinds ...EdgeKind) []*Edge {
	return filterEdges(g.out[id], kinds)
}

// In returns incoming edges to a node, optionally filtered by kind.
func (g *Graph) In(id string, kinds ...EdgeKind) []*Edge {
	return filterEdges(g.in[id], kinds)
}

func filterEdges(edges []*Edge, kinds []EdgeKind) []*Edge {
	if len(kinds) == 0 {
		return edges
	}
	allowed := make(map[EdgeKind]bool, len(kinds))
	for _, k := range kinds {
		allowed[k] = true
	}
	var out []*Edge
	for _, e := range edges {
		if allowed[e.Kind] {
			out = append(out, e)
		}
	}
	return out
}

// Reindex rebuilds the adjacency indexes from Edges. Call this after
// loading nodes/edges directly (e.g. from the store) without using AddEdge.
func (g *Graph) Reindex() {
	g.out = make(map[string][]*Edge)
	g.in = make(map[string][]*Edge)
	for _, e := range g.Edges {
		g.out[e.From] = append(g.out[e.From], e)
		g.in[e.To] = append(g.in[e.To], e)
	}
}
