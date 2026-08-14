// Package diagram renders code-graph subgraphs (call flows, impact trees)
// as ASCII art for the terminal and as Mermaid syntax for the `ask`
// command's richer output.
package diagram

import (
	"fmt"
	"strings"

	"github.com/cloud-ct/graphsentry/internal/graph"
)

// ASCIIFlow renders the flow paths from FlowPaths as an indented ASCII
// tree, e.g.:
//
//	POST /users
//	└─► UsersController
//	     └─► UserService
//	          ├─► UserRepository
//	          │    └─► PostgreSQL
//	          └─► IdentityService
func ASCIIFlow(paths [][]*graph.PathStep) string {
	if len(paths) == 0 {
		return "(no outgoing calls found)"
	}
	root := treeFromPaths(paths)
	var b strings.Builder
	b.WriteString(root.label + "\n")
	renderChildren(&b, root.children, "")
	return b.String()
}

// displayName prefers a node's class/type-scoped Qualified name over its
// bare Name: "AppService.GetAllAsync" instead of just "GetAllAsync",
// which otherwise reads as if two different layers calling a same-named
// method (e.g. a service delegating to a repository method of the same
// name) were the same node calling itself. Falls back to Name for nodes
// where Qualified wasn't populated (e.g. graphs persisted before this
// field existed, migrated without a re-analyze).
func displayName(n *graph.Node) string {
	if n.Qualified != "" {
		return n.Qualified
	}
	return n.Name
}

type treeNode struct {
	key      string // edge-kind + node name, used to dedupe branches during merge
	label    string
	children []*treeNode
}

// treeFromPaths merges the flat path list (which share a common root) into
// a tree so branches sharing a prefix are only rendered once. Nodes are
// keyed by node ID + edge-kind, not by display name — two different
// symbols that happen to share a bare method name (e.g. a service and the
// repository method it calls, both named GetAllAsync) must stay as
// separate branches, not collapse into one.
func treeFromPaths(paths [][]*graph.PathStep) *treeNode {
	root := &treeNode{label: displayName(paths[0][0].Node)}
	for _, path := range paths {
		cur := root
		for _, step := range path[1:] {
			key := string(step.Via) + "::" + step.Node.ID
			var found *treeNode
			for _, c := range cur.children {
				if c.key == key {
					found = c
					break
				}
			}
			if found == nil {
				found = &treeNode{key: key, label: edgeLabel(step)}
				cur.children = append(cur.children, found)
			}
			cur = found
		}
	}
	return root
}

// edgeLabel renders a tree node's label, prefixing the node name with its
// relationship to the parent when that relationship isn't the default
// "calls" — e.g. "[instantiates] MetricDelta" vs. plain "PercentageDelta"
// for a call — so the ASCII tree doesn't imply every edge is a method
// call the way an unlabeled arrow would.
func edgeLabel(step *graph.PathStep) string {
	name := displayName(step.Node)
	if step.Via != "" && step.Via != graph.EdgeCalls {
		return fmt.Sprintf("[%s] %s", step.Via, name)
	}
	return name
}

func renderChildren(b *strings.Builder, children []*treeNode, prefix string) {
	for i, c := range children {
		last := i == len(children)-1
		connector := "├─► "
		nextPrefix := prefix + "│    "
		if last {
			connector = "└─► "
			nextPrefix = prefix + "     "
		}
		fmt.Fprintf(b, "%s%s%s\n", prefix, connector, c.label)
		renderChildren(b, c.children, nextPrefix)
	}
}

// MermaidFlow renders the flow paths as a Mermaid flowchart definition.
func MermaidFlow(paths [][]*graph.PathStep) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	seen := map[string]bool{}
	seenEdge := map[string]bool{}
	nodeID := func(n *graph.Node) string {
		return sanitizeID(n.ID)
	}
	for _, path := range paths {
		for i, step := range path {
			id := nodeID(step.Node)
			if !seen[id] {
				seen[id] = true
				fmt.Fprintf(&b, "    %s[%q]\n", id, displayName(step.Node))
			}
			if i == 0 {
				continue
			}
			prevID := nodeID(path[i-1].Node)
			key := prevID + "->" + id
			if !seenEdge[key] {
				seenEdge[key] = true
				fmt.Fprintf(&b, "    %s -->|%s| %s\n", prevID, step.Via, id)
			}
		}
	}
	return b.String()
}

func sanitizeID(id string) string {
	r := strings.NewReplacer(":", "_", ".", "_", "/", "_", " ", "_", "-", "_")
	return "n_" + r.Replace(id)
}

// ASCIIImpact renders an impact analysis result as a flat, distance-grouped
// list.
func ASCIIImpact(res *graph.ImpactResult) string {
	if len(res.Impacted) == 0 {
		return "Nothing depends on this — safe to change in isolation (no known dependents in the graph)."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Impact analysis for %s:\n\n", res.Root)
	curDist := -1
	for _, imp := range res.Impacted {
		if imp.Distance != curDist {
			curDist = imp.Distance
			fmt.Fprintf(&b, "depth %d:\n", curDist)
		}
		fmt.Fprintf(&b, "  - %s (%s) via %s [%s]\n", displayName(imp.Node), imp.Node.Kind, imp.Via, imp.Node.File)
	}
	return b.String()
}
