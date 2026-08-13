// Package diagram renders code-graph subgraphs (call flows, impact trees)
// as ASCII art for the terminal and as Mermaid syntax for the `ask`
// command's richer output.
package diagram

import (
	"fmt"
	"strings"

	"github.com/huandert/repolens/internal/graph"
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

type treeNode struct {
	label    string
	children []*treeNode
}

// treeFromPaths merges the flat path list (which share a common root) into
// a tree so branches sharing a prefix are only rendered once.
func treeFromPaths(paths [][]*graph.PathStep) *treeNode {
	root := &treeNode{label: paths[0][0].Node.Name}
	for _, path := range paths {
		cur := root
		for _, step := range path[1:] {
			label := edgeLabel(step)
			var found *treeNode
			for _, c := range cur.children {
				if c.label == label {
					found = c
					break
				}
			}
			if found == nil {
				found = &treeNode{label: label}
				cur.children = append(cur.children, found)
			}
			cur = found
		}
	}
	return root
}

func edgeLabel(step *graph.PathStep) string {
	return step.Node.Name
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
				fmt.Fprintf(&b, "    %s[%q]\n", id, step.Node.Name)
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
		fmt.Fprintf(&b, "  - %s (%s) via %s [%s]\n", imp.Node.Name, imp.Node.Kind, imp.Via, imp.Node.File)
	}
	return b.String()
}
