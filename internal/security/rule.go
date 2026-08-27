// Package security answers one deterministic, no-LLM question about a
// repository's code graph: "which of these HTTP endpoints has no
// authentication/authorization guard?"
//
// It's built to be extended by people who don't touch the rest of
// GraphSentry: a Rule is a small, self-contained strategy for recognizing
// one particular way of guarding an endpoint (an ASP.NET [Authorize]
// attribute, a custom IAuthorizationFilter, an Express middleware, a Python
// decorator, ...). Analyze runs every Rule that applies and merges their
// results — it has no framework-specific knowledge itself, so a new Rule
// never means editing an existing one, Analyze, or any caller of Analyze.
//
// Rules read a *already-built* graph.Graph (Node.Attrs, EdgeImplements,
// EdgeCalls, ...) rather than source text, so they're cheap to test (build
// a small graph.Graph by hand, no tree-sitter involved) and can reason
// across files — e.g. "this attribute wraps a class that implements
// IAuthorizationFilter" needs the whole-repo Implements edges, which only
// exist once the graph is fully built.
package security

import (
	"fmt"

	"github.com/cloud-ct/graphsentry/internal/graph"
)

// GuardMatch is one guard a Rule found for one endpoint.
type GuardMatch struct {
	EndpointID string `json:"endpoint_id"`         // graph.Node.ID of the endpoint this guard applies to
	GuardName  string `json:"guard_name"`          // display name, e.g. "Authorize(Roles=admin)" or "ApiKeyAuthorize(N8N)"
	File       string `json:"file"`                // where the guard is declared/applied, for the report to point at
	Line       int    `json:"line"`
	// Public marks this as an explicit "this endpoint is intentionally
	// open" signal (C#'s [AllowAnonymous] and equivalents), not a guard
	// that protects the endpoint. It's reported through the same
	// GuardMatch shape as a real guard — Analyze is what tells the two
	// apart — so a Rule only ever has one method to implement regardless
	// of which kind of signal it recognizes.
	Public bool `json:"public"`
}

// Rule is one self-contained strategy for recognizing an auth guard (or an
// explicit "intentionally public" override) on an endpoint. Implementations
// live under internal/security/rules, one file per convention.
type Rule interface {
	// Name identifies the rule for diagnostics and reporting, e.g.
	// "aspnet-authorize" or "aspnet-custom-filter".
	Name() string

	// Applies reports whether this rule is relevant for a node's
	// Language, so Analyze can skip it cheaply for languages it doesn't
	// understand.
	Applies(language string) bool

	// Evaluate returns every guard/public-override this rule can justify
	// from g. Implementations must not mutate g.
	Evaluate(g *graph.Graph) []GuardMatch
}

// Status is the final auth posture Analyze assigns to one endpoint, after
// merging every applicable Rule's findings.
type Status string

const (
	// StatusProtected: at least one Rule found a guard, and nothing
	// overrode it with an explicit public marker.
	StatusProtected Status = "protected"
	// StatusPublic: a Rule found an explicit "intentionally open" marker
	// (e.g. [AllowAnonymous]) — a deliberate choice, not a finding to flag.
	StatusPublic Status = "public"
	// StatusUnprotected: no Rule applicable to this endpoint's language
	// found any guard or public marker at all. This is what the "endpoints
	// without auth" report surfaces.
	StatusUnprotected Status = "unprotected"
	// StatusUnknown: no Rule applies to this endpoint's language yet, so
	// nothing can be said either way — kept distinct from StatusUnprotected
	// so an unsupported language never gets reported as a finding.
	StatusUnknown Status = "unknown"
)

// EndpointFinding is one endpoint's merged auth posture.
type EndpointFinding struct {
	Endpoint *graph.Node  `json:"endpoint"`
	Status   Status       `json:"status"`
	Guards   []GuardMatch `json:"guards,omitempty"` // the guards behind StatusProtected; empty otherwise
}

// Analyze evaluates every endpoint node in g against every Rule that
// applies to its language, and returns one merged EndpointFinding per
// endpoint. A single Public match wins over any number of guard matches
// (an explicit override is deliberate, so it's reported as such rather than
// as "protected").
func Analyze(g *graph.Graph, rules ...Rule) []EndpointFinding {
	// Each rule runs exactly once over the whole graph (not once per
	// endpoint) — it decides for itself which endpoints it has anything to
	// say about, via the EndpointID on the GuardMatches it returns.
	byEndpoint := make(map[string][]GuardMatch)
	for _, r := range rules {
		for _, m := range r.Evaluate(g) {
			byEndpoint[m.EndpointID] = append(byEndpoint[m.EndpointID], m)
		}
	}

	var findings []EndpointFinding
	for _, n := range g.Nodes {
		if n.Kind != graph.NodeEndpoint {
			continue
		}
		findings = append(findings, buildFinding(n, byEndpoint[n.ID], anyRuleApplies(rules, n.Language)))
	}
	return findings
}

// AnnotateGuards runs Analyze and, for every guard it found, adds a
// graph.NodeGuard + graph.EdgeGuardedBy directly onto g — so ordinary graph
// traversals (`graphsentry flow`, in particular) can show what protects an
// endpoint the same way they already show what it calls, without every
// consumer needing to know Rule/GuardMatch exist at all. This is the one
// place internal/security is allowed to mutate a graph.Graph — see
// graph.EdgeGuardedBy's doc comment for why the Builder itself never does
// this, and why nothing added here is ever written back by Store.Save (g is
// the caller's in-memory copy; annotating it doesn't persist anything).
//
// Guard nodes are scoped one-per-match rather than shared/deduplicated
// across endpoints that happen to carry the identical guard (e.g. the same
// [Authorize(Roles=admin)] on ten controllers): each match's own File/Line
// stays accurate to that specific attribute usage instead of becoming
// arbitrary once several occurrences would otherwise collide on one shared
// node.
func AnnotateGuards(g *graph.Graph, rules ...Rule) []EndpointFinding {
	findings := Analyze(g, rules...)
	for _, f := range findings {
		for _, gm := range f.Guards {
			guardID := fmt.Sprintf("guard::%s::%s::%d", f.Endpoint.ID, gm.GuardName, gm.Line)
			g.AddNode(&graph.Node{
				ID: guardID, Kind: graph.NodeGuard, Name: gm.GuardName, Qualified: gm.GuardName,
				File: gm.File, StartLine: gm.Line, Language: f.Endpoint.Language,
			})
			g.AddEdge(f.Endpoint.ID, guardID, graph.EdgeGuardedBy)
		}
	}
	return findings
}

func anyRuleApplies(rules []Rule, language string) bool {
	for _, r := range rules {
		if r.Applies(language) {
			return true
		}
	}
	return false
}

func buildFinding(n *graph.Node, matches []GuardMatch, ruleApplies bool) EndpointFinding {
	f := EndpointFinding{Endpoint: n}

	var guards []GuardMatch
	public := false
	for _, m := range matches {
		if m.Public {
			public = true
			continue
		}
		guards = append(guards, m)
	}

	switch {
	case public:
		f.Status = StatusPublic
	case len(guards) > 0:
		f.Status = StatusProtected
		f.Guards = guards
	case ruleApplies:
		f.Status = StatusUnprotected
	default:
		f.Status = StatusUnknown
	}
	return f
}
