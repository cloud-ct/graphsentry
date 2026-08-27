package rules

import (
	"fmt"
	"strings"

	"github.com/cloud-ct/graphsentry/internal/graph"
	"github.com/cloud-ct/graphsentry/internal/security"
)

// aspNetAuthFilterInterfaces are the ASP.NET interfaces that mark a class as
// an authorization filter — these are framework types, never nodes in the
// analyzed repo themselves (see graph.Node.Implements), so recognizing them
// is a name check against what a class *declared*, not a graph traversal
// through a resolved edge.
var aspNetAuthFilterInterfaces = map[string]bool{
	"IAuthorizationFilter":      true,
	"IAsyncAuthorizationFilter": true,
}

// AspNetCustomFilter recognizes a *custom* ASP.NET auth attribute — the
// `class XAttribute : TypeFilterAttribute` + `base(typeof(YFilter))`
// pattern (see graph.Node.WrapsType), where YFilter implements
// IAuthorizationFilter/IAsyncAuthorizationFilter. Unlike AspNetAuthorize,
// this never hardcodes an attribute's name: it works for any team's custom
// filter-backed attribute (bankme-ai-main's `[ApiKeyAuthorize(...)]` is the
// motivating example), purely by following the wraps-type -> implements
// chain the parser already extracted.
type AspNetCustomFilter struct{}

func (AspNetCustomFilter) Name() string { return "aspnet-custom-filter" }

func (AspNetCustomFilter) Applies(language string) bool { return language == "csharp" }

func (r AspNetCustomFilter) Evaluate(g *graph.Graph) []security.GuardMatch {
	guardAttrNames := guardAttributeNames(g)
	if len(guardAttrNames) == 0 {
		return nil
	}

	var matches []security.GuardMatch
	for _, n := range g.Nodes {
		if n.Kind != graph.NodeEndpoint || n.Language != "csharp" {
			continue
		}
		for _, at := range n.Attrs {
			if !guardAttrNames[at.Name] {
				continue
			}
			matches = append(matches, security.GuardMatch{
				EndpointID: n.ID,
				GuardName:  formatCustomFilter(at),
				File:       n.File,
				Line:       at.Line,
			})
		}
	}
	return matches
}

// guardAttributeNames finds every attribute usage name (the bracket form,
// e.g. "ApiKeyAuthorize") that resolves to a custom filter-backed guard:
// a class that wraps another class implementing one of
// aspNetAuthFilterInterfaces.
func guardAttributeNames(g *graph.Graph) map[string]bool {
	filterTypes := make(map[string]bool) // filter class names that implement an auth-filter interface
	for _, n := range g.Nodes {
		if n.Kind != graph.NodeClass || n.Language != "csharp" {
			continue
		}
		for _, impl := range n.Implements {
			if aspNetAuthFilterInterfaces[impl] {
				filterTypes[n.Name] = true
				break
			}
		}
	}
	if len(filterTypes) == 0 {
		return nil
	}

	names := make(map[string]bool)
	for _, n := range g.Nodes {
		if n.Kind != graph.NodeClass || n.Language != "csharp" || n.WrapsType == "" {
			continue
		}
		if filterTypes[n.WrapsType] {
			names[attributeUsageName(n.Name)] = true
		}
	}
	return names
}

// attributeUsageName strips the C# "Attribute" suffix a class declares
// (`ApiKeyAuthorizeAttribute`) to the name actually written at a use site
// (`[ApiKeyAuthorize(...)]`) — the two are the same convention the compiler
// itself applies, not a guess.
func attributeUsageName(className string) string {
	return strings.TrimSuffix(className, "Attribute")
}

func formatCustomFilter(at graph.Attr) string {
	if v, ok := at.Args[""]; ok {
		return fmt.Sprintf("%s(%s)", at.Name, v)
	}
	return at.Name
}
