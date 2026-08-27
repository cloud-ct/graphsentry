package security

import (
	"testing"

	"github.com/cloud-ct/graphsentry/internal/graph"
)

// fakeRule is a minimal Rule used only by this test — it never touches
// tree-sitter or a real repo, which is exactly the point of keeping Rule
// decoupled from parsing: testing Analyze's merge logic doesn't need either.
type fakeRule struct {
	name     string
	lang     string
	matches  []GuardMatch
}

func (r fakeRule) Name() string                      { return r.name }
func (r fakeRule) Applies(language string) bool       { return language == r.lang }
func (r fakeRule) Evaluate(g *graph.Graph) []GuardMatch { return r.matches }

func endpoint(id, language string) *graph.Node {
	return &graph.Node{ID: id, Kind: graph.NodeEndpoint, Name: id, Qualified: id, Language: language}
}

func TestAnalyze(t *testing.T) {
	g := graph.New()
	g.AddNode(endpoint("ep-protected", "csharp"))
	g.AddNode(endpoint("ep-public", "csharp"))
	g.AddNode(endpoint("ep-unprotected", "csharp"))
	g.AddNode(endpoint("ep-unknown", "python")) // no rule below applies to python

	rule := fakeRule{
		name: "fake-aspnet",
		lang: "csharp",
		matches: []GuardMatch{
			{EndpointID: "ep-protected", GuardName: "Authorize(Roles=admin)"},
			{EndpointID: "ep-public", Public: true},
		},
	}

	findings := Analyze(g, rule)
	if len(findings) != 4 {
		t.Fatalf("expected 4 findings, got %d", len(findings))
	}

	byID := make(map[string]EndpointFinding, len(findings))
	for _, f := range findings {
		byID[f.Endpoint.ID] = f
	}

	if got := byID["ep-protected"].Status; got != StatusProtected {
		t.Errorf("ep-protected: got status %q, want %q", got, StatusProtected)
	}
	if got := len(byID["ep-protected"].Guards); got != 1 {
		t.Errorf("ep-protected: got %d guards, want 1", got)
	}

	if got := byID["ep-public"].Status; got != StatusPublic {
		t.Errorf("ep-public: got status %q, want %q", got, StatusPublic)
	}
	if got := len(byID["ep-public"].Guards); got != 0 {
		t.Errorf("ep-public: got %d guards, want 0 (a public override isn't a guard)", got)
	}

	if got := byID["ep-unprotected"].Status; got != StatusUnprotected {
		t.Errorf("ep-unprotected: got status %q, want %q", got, StatusUnprotected)
	}

	if got := byID["ep-unknown"].Status; got != StatusUnknown {
		t.Errorf("ep-unknown: got status %q, want %q (no rule applies to python here)", got, StatusUnknown)
	}
}

// TestAnnotateGuards checks that guards actually land on the graph as
// ordinary nodes/edges — the whole point being that a plain graph
// traversal (like `graphsentry flow`'s FlowPaths) picks them up without
// knowing anything about internal/security.
func TestAnnotateGuards(t *testing.T) {
	g := graph.New()
	g.AddNode(endpoint("ep-protected", "csharp"))
	g.AddNode(endpoint("ep-unprotected", "csharp"))

	rule := fakeRule{
		name: "fake-aspnet",
		lang: "csharp",
		matches: []GuardMatch{
			{EndpointID: "ep-protected", GuardName: "Authorize(Roles=admin)", File: "Foo.cs", Line: 10},
		},
	}

	AnnotateGuards(g, rule)

	guardEdges := g.Out("ep-protected", graph.EdgeGuardedBy)
	if len(guardEdges) != 1 {
		t.Fatalf("expected 1 guarded_by edge from ep-protected, got %d", len(guardEdges))
	}
	guardNode := g.Nodes[guardEdges[0].To]
	if guardNode == nil || guardNode.Kind != graph.NodeGuard || guardNode.Name != "Authorize(Roles=admin)" {
		t.Errorf("expected a NodeGuard named %q, got %+v", "Authorize(Roles=admin)", guardNode)
	}

	if edges := g.Out("ep-unprotected", graph.EdgeGuardedBy); len(edges) != 0 {
		t.Errorf("expected no guarded_by edge from an unprotected endpoint, got %d", len(edges))
	}
}
