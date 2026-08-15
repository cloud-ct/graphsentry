package graph

import "sort"

// ImpactResult is the outcome of an impact analysis for a symbol: everything
// that transitively depends on it, i.e. everything that could break if the
// symbol changes.
type ImpactResult struct {
	Root     string          `json:"root"`
	Impacted []*ImpactedNode `json:"impacted"` // sorted by distance, then id
}

// ImpactedNode is a node reachable from the root via incoming edges
// (dependents), annotated with its distance from the root and the edge kind
// that connects it to its predecessor in the BFS.
type ImpactedNode struct {
	Node     *Node    `json:"node"`
	Distance int      `json:"distance"`
	Via      EdgeKind `json:"via"`
}

// Impact performs a breadth-first traversal of incoming edges (calls,
// references, implements, imports) starting from id, up to maxDepth hops
// (0 = unlimited). It answers: "if I change this symbol, what depends on
// it and could break?" "Defines" edges (a file defining the symbols in it)
// are deliberately excluded from this traversal: every symbol has exactly
// one, from its own file, so including it would report "the file this
// lives in" as an impacted dependent of literally every query — true but
// meaningless noise, since changing a method doesn't "break" the file that
// contains it the way changing it can break an actual caller.
func (g *Graph) Impact(id string, maxDepth int) *ImpactResult {
	res := &ImpactResult{Root: id}
	res.Impacted = bfsReachable(g, id, maxDepth, g.In)
	return res
}

// Dependencies performs the mirror-image traversal of Impact: breadth-first
// over outgoing edges, up to maxDepth hops (0 = unlimited). It answers
// "what does this symbol itself rely on?" — the transitive expansion of the
// same fan-out CodeLens.title shows as a single number. Kept as a separate
// entry point (rather than an "incoming bool" parameter on Impact) since
// callers and the CLI/JSON shape read more clearly as two named things:
// what depends on this vs. what this depends on.
func (g *Graph) Dependencies(id string, maxDepth int) *ImpactResult {
	res := &ImpactResult{Root: id}
	res.Impacted = bfsReachable(g, id, maxDepth, g.Out)
	return res
}

// bfsReachable walks edges via next (g.In for dependents, g.Out for
// dependencies) up to maxDepth hops, returning reachable nodes sorted by
// distance then id. "Defines" edges are excluded (see Impact's doc comment)
// regardless of direction, since a file "depending on" the symbols it
// defines is exactly as meaningless as the reverse.
func bfsReachable(g *Graph, id string, maxDepth int, next func(id string, kinds ...EdgeKind) []*Edge) []*ImpactedNode {
	visited := map[string]int{id: 0}
	via := map[string]EdgeKind{}
	queue := []string{id}
	var order []string

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		d := visited[cur]
		if maxDepth > 0 && d >= maxDepth {
			continue
		}
		for _, e := range excludeDefines(next(cur)) {
			neighbor := edgeNeighbor(e, cur)
			if _, seen := visited[neighbor]; seen {
				continue
			}
			visited[neighbor] = d + 1
			via[neighbor] = e.Kind
			order = append(order, neighbor)
			queue = append(queue, neighbor)
		}
	}

	var out []*ImpactedNode
	for _, nid := range order {
		n, ok := g.Nodes[nid]
		if !ok {
			n = &Node{ID: nid, Name: nid} // dangling reference, still report it
		}
		out = append(out, &ImpactedNode{
			Node:     n,
			Distance: visited[nid],
			Via:      via[nid],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Distance != out[j].Distance {
			return out[i].Distance < out[j].Distance
		}
		return out[i].Node.ID < out[j].Node.ID
	})
	return out
}

// edgeNeighbor returns whichever end of e isn't cur — e.From when walking
// incoming edges (next == g.In, so e.To == cur), e.To when walking outgoing
// ones (next == g.Out, so e.From == cur).
func edgeNeighbor(e *Edge, cur string) string {
	if e.To == cur {
		return e.From
	}
	return e.To
}

// CouplingScore summarizes how coupled a node is to the rest of the graph.
type CouplingScore struct {
	Node   *Node `json:"node"`
	FanIn  int   `json:"fan_in"`  // number of distinct nodes depending on this one
	FanOut int   `json:"fan_out"` // number of distinct nodes this one depends on
	Total  int   `json:"total"`
}

// FanIn returns the number of distinct nodes with a real dependency edge
// pointing to id (calls, instantiates, implements, imports — not
// "defines": see excludeDefines).
func (g *Graph) FanIn(id string) int {
	return len(distinctEndpoints(excludeDefines(g.In(id)), true))
}

// FanOut returns the number of distinct nodes id has a real dependency
// edge pointing to (see FanIn).
func (g *Graph) FanOut(id string) int {
	return len(distinctEndpoints(excludeDefines(g.Out(id)), false))
}

// excludeDefines drops "defines" edges (a file defining the symbols
// declared in it) from a result set. Every symbol has exactly one —
// from its own file — so counting it as coupling or impact would inflate
// every single node's numbers by the same meaningless +1: "the file I'm
// declared in" isn't a dependent or a dependency in any sense a user
// asking "what's coupled to this" or "what breaks if I change this" cares
// about.
func excludeDefines(edges []*Edge) []*Edge {
	var out []*Edge
	for _, e := range edges {
		if e.Kind != EdgeDefines {
			out = append(out, e)
		}
	}
	return out
}

func distinctEndpoints(edges []*Edge, incoming bool) map[string]bool {
	set := make(map[string]bool)
	for _, e := range edges {
		if incoming {
			set[e.From] = true
		} else {
			set[e.To] = true
		}
	}
	return set
}

// TopCoupled ranks nodes by total coupling (fan-in + fan-out), descending.
// Only nodes present in g.Nodes are considered (dangling edge endpoints are
// excluded). limit <= 0 means "all".
func (g *Graph) TopCoupled(limit int) []*CouplingScore {
	scores := make([]*CouplingScore, 0, len(g.Nodes))
	for id, n := range g.Nodes {
		fi := g.FanIn(id)
		fo := g.FanOut(id)
		if fi == 0 && fo == 0 {
			continue
		}
		scores = append(scores, &CouplingScore{Node: n, FanIn: fi, FanOut: fo, Total: fi + fo})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Total != scores[j].Total {
			return scores[i].Total > scores[j].Total
		}
		return scores[i].Node.ID < scores[j].Node.ID
	})
	if limit > 0 && limit < len(scores) {
		scores = scores[:limit]
	}
	return scores
}

// PathStep is one hop in a call/dependency path.
type PathStep struct {
	Node *Node    `json:"node"`
	Via  EdgeKind `json:"via"` // edge kind used to reach this node from the previous step
}

// FlowPaths performs a bounded-depth DFS over outgoing edges from id,
// collecting all simple paths (no repeated nodes) up to maxDepth hops. Used
// to render call-flow diagrams for `graphsentry flow`.
func (g *Graph) FlowPaths(id string, maxDepth int) [][]*PathStep {
	if maxDepth <= 0 {
		maxDepth = 5
	}
	start := &PathStep{Node: g.nodeOrStub(id), Via: ""}
	var paths [][]*PathStep
	visited := map[string]bool{id: true}
	var dfs func(cur string, path []*PathStep, depth int)
	dfs = func(cur string, path []*PathStep, depth int) {
		outs := g.Out(cur)
		if len(outs) == 0 || depth >= maxDepth {
			cp := make([]*PathStep, len(path))
			copy(cp, path)
			paths = append(paths, cp)
			return
		}
		branched := false
		for _, e := range outs {
			if visited[e.To] {
				continue
			}
			branched = true
			visited[e.To] = true
			step := &PathStep{Node: g.nodeOrStub(e.To), Via: e.Kind}
			dfs(e.To, append(path, step), depth+1)
			delete(visited, e.To)
		}
		if !branched {
			cp := make([]*PathStep, len(path))
			copy(cp, path)
			paths = append(paths, cp)
		}
	}
	dfs(id, []*PathStep{start}, 0)
	return paths
}

func (g *Graph) nodeOrStub(id string) *Node {
	if n, ok := g.Nodes[id]; ok {
		return n
	}
	return &Node{ID: id, Name: id}
}

// EndpointsAccessingDB returns endpoint-kind nodes whose flow (within
// maxDepth hops) reaches a node whose name or file matches a DB-access
// heuristic (used by "show endpoints that access the DB directly").
// dbMatcher receives a node and reports whether it looks like DB access.
func (g *Graph) EndpointsAccessingDB(maxDepth int, dbMatcher func(*Node) bool) []*Node {
	var result []*Node
	for id, n := range g.Nodes {
		if n.Kind != NodeEndpoint {
			continue
		}
		if g.reaches(id, maxDepth, dbMatcher) {
			result = append(result, n)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (g *Graph) reaches(start string, maxDepth int, match func(*Node) bool) bool {
	visited := map[string]bool{start: true}
	queue := []struct {
		id    string
		depth int
	}{{start, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if n, ok := g.Nodes[cur.id]; ok && match(n) {
			return true
		}
		if maxDepth > 0 && cur.depth >= maxDepth {
			continue
		}
		for _, e := range g.Out(cur.id) {
			if visited[e.To] {
				continue
			}
			visited[e.To] = true
			queue = append(queue, struct {
				id    string
				depth int
			}{e.To, cur.depth + 1})
		}
	}
	return false
}
