// Package rules holds one file per auth-detection convention, each
// implementing security.Rule. See the internal/security package doc for
// why this is split into independent, individually-registered strategies
// instead of one big switch statement.
package rules

import (
	"fmt"

	"github.com/cloud-ct/graphsentry/internal/graph"
	"github.com/cloud-ct/graphsentry/internal/security"
)

// AspNetAuthorize recognizes ASP.NET's built-in [Authorize]/[AllowAnonymous]
// attributes. These are framework types, not something declared anywhere in
// the analyzed repo, so — unlike AspNetCustomFilter — this rule matches by
// attribute name rather than by tracing an implements chain; there's no
// user-defined type to trace through.
type AspNetAuthorize struct{}

func (AspNetAuthorize) Name() string { return "aspnet-authorize" }

func (AspNetAuthorize) Applies(language string) bool { return language == "csharp" }

func (r AspNetAuthorize) Evaluate(g *graph.Graph) []security.GuardMatch {
	var matches []security.GuardMatch
	for _, n := range g.Nodes {
		if n.Kind != graph.NodeEndpoint || n.Language != "csharp" {
			continue
		}
		for _, at := range n.Attrs {
			switch at.Name {
			case "Authorize":
				matches = append(matches, security.GuardMatch{
					EndpointID: n.ID,
					GuardName:  formatAuthorize(at),
					File:       n.File,
					Line:       at.Line,
				})
			case "AllowAnonymous":
				matches = append(matches, security.GuardMatch{
					EndpointID: n.ID,
					GuardName:  "AllowAnonymous",
					File:       n.File,
					Line:       at.Line,
					Public:     true,
				})
			}
		}
	}
	return matches
}

// formatAuthorize renders a display name for an [Authorize] guard that
// includes its qualifier when it has one — "Authorize(Roles=admin)" reads a
// lot more useful in a report than a bare "Authorize" repeated for every
// endpoint in the repo, protected by wildly different roles/policies/
// schemes.
func formatAuthorize(at graph.Attr) string {
	for _, key := range []string{"Roles", "Policy", "AuthenticationSchemes"} {
		if v, ok := at.Args[key]; ok {
			return fmt.Sprintf("Authorize(%s=%s)", key, v)
		}
	}
	return "Authorize"
}
