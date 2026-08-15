package graph

import "testing"

// buildSample constructs a tiny graph mimicking:
// POST /users -> UsersController -> UserService -> UserRepository -> DB
//
//	-> IdentityService
func buildSample() *Graph {
	g := New()
	g.AddNode(&Node{ID: "endpoint::POST /users", Kind: NodeEndpoint, Name: "POST /users"})
	g.AddNode(&Node{ID: "controller::UsersController", Kind: NodeClass, Name: "UsersController"})
	g.AddNode(&Node{ID: "service::UserService", Kind: NodeClass, Name: "UserService"})
	g.AddNode(&Node{ID: "repo::UserRepository", Kind: NodeClass, Name: "UserRepository"})
	g.AddNode(&Node{ID: "identity::IdentityService", Kind: NodeClass, Name: "IdentityService"})
	g.AddNode(&Node{ID: "db::PostgreSQL", Kind: NodeType, Name: "PostgreSQL"})

	g.AddEdge("endpoint::POST /users", "controller::UsersController", EdgeCalls)
	g.AddEdge("controller::UsersController", "service::UserService", EdgeCalls)
	g.AddEdge("service::UserService", "repo::UserRepository", EdgeCalls)
	g.AddEdge("service::UserService", "identity::IdentityService", EdgeCalls)
	g.AddEdge("repo::UserRepository", "db::PostgreSQL", EdgeCalls)
	return g
}

func TestImpact(t *testing.T) {
	g := buildSample()
	res := g.Impact("repo::UserRepository", 0)
	if len(res.Impacted) != 3 {
		t.Fatalf("expected 3 impacted nodes, got %d: %+v", len(res.Impacted), res.Impacted)
	}
	// Closest dependent should be UserService at distance 1.
	if res.Impacted[0].Node.ID != "service::UserService" || res.Impacted[0].Distance != 1 {
		t.Errorf("expected UserService at distance 1 first, got %+v", res.Impacted[0])
	}
	ids := map[string]bool{}
	for _, n := range res.Impacted {
		ids[n.Node.ID] = true
	}
	for _, want := range []string{"service::UserService", "controller::UsersController", "endpoint::POST /users"} {
		if !ids[want] {
			t.Errorf("expected %s to be impacted", want)
		}
	}
}

func TestImpactMaxDepth(t *testing.T) {
	g := buildSample()
	res := g.Impact("repo::UserRepository", 1)
	if len(res.Impacted) != 1 {
		t.Fatalf("expected 1 impacted node with maxDepth=1, got %d", len(res.Impacted))
	}
}

func TestDependencies(t *testing.T) {
	g := buildSample()
	res := g.Dependencies("service::UserService", 0)
	if len(res.Impacted) != 3 {
		t.Fatalf("expected 3 dependencies, got %d: %+v", len(res.Impacted), res.Impacted)
	}
	ids := map[string]bool{}
	for _, n := range res.Impacted {
		ids[n.Node.ID] = true
	}
	for _, want := range []string{"repo::UserRepository", "identity::IdentityService", "db::PostgreSQL"} {
		if !ids[want] {
			t.Errorf("expected %s to be a dependency", want)
		}
	}
}

func TestDependenciesMaxDepth(t *testing.T) {
	g := buildSample()
	res := g.Dependencies("service::UserService", 1)
	if len(res.Impacted) != 2 {
		t.Fatalf("expected 2 direct dependencies with maxDepth=1, got %d", len(res.Impacted))
	}
}

func TestFanInFanOut(t *testing.T) {
	g := buildSample()
	if fo := g.FanOut("service::UserService"); fo != 2 {
		t.Errorf("expected fan-out 2 for UserService, got %d", fo)
	}
	if fi := g.FanIn("service::UserService"); fi != 1 {
		t.Errorf("expected fan-in 1 for UserService, got %d", fi)
	}
}

func TestTopCoupled(t *testing.T) {
	g := buildSample()
	top := g.TopCoupled(1)
	if len(top) != 1 {
		t.Fatalf("expected 1 result, got %d", len(top))
	}
	if top[0].Node.ID != "service::UserService" {
		t.Errorf("expected UserService to be most coupled (fan-in 1 + fan-out 2 = 3), got %s (total=%d)", top[0].Node.ID, top[0].Total)
	}
}

func TestFlowPaths(t *testing.T) {
	g := buildSample()
	paths := g.FlowPaths("endpoint::POST /users", 5)
	found := false
	for _, p := range paths {
		if len(p) == 5 && p[len(p)-1].Node.ID == "db::PostgreSQL" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a path reaching PostgreSQL, got paths: %+v", paths)
	}
}

func TestEndpointsAccessingDB(t *testing.T) {
	g := buildSample()
	isDB := func(n *Node) bool { return n.Name == "PostgreSQL" }
	endpoints := g.EndpointsAccessingDB(0, isDB)
	if len(endpoints) != 1 || endpoints[0].Name != "POST /users" {
		t.Errorf("expected POST /users endpoint to reach DB, got %+v", endpoints)
	}
}

// TestDefinesEdgeExcludedFromCouplingAndImpact is a regression test for a
// confusion reported against bankme-ai-main: every symbol's fan-in/impact
// count was inflated by exactly 1, because the "file defines this symbol"
// bookkeeping edge every symbol has was being counted as a real
// dependent/dependency. A user asking "why does this method have N
// dependents" should never have to first subtract 1 for "its own file".
func TestDefinesEdgeExcludedFromCouplingAndImpact(t *testing.T) {
	g := New()
	g.AddNode(&Node{ID: "file::a.go", Kind: NodeFile, Name: "a.go"})
	g.AddNode(&Node{ID: "symbol::a.go::Foo", Kind: NodeFunction, Name: "Foo"})
	g.AddEdge("file::a.go", "symbol::a.go::Foo", EdgeDefines)

	if fi := g.FanIn("symbol::a.go::Foo"); fi != 0 {
		t.Errorf("expected FanIn 0 for a symbol with only a defines edge, got %d", fi)
	}
	if fo := g.FanOut("file::a.go"); fo != 0 {
		t.Errorf("expected FanOut 0 for a file with only a defines edge, got %d", fo)
	}

	res := g.Impact("symbol::a.go::Foo", 0)
	if len(res.Impacted) != 0 {
		t.Errorf("expected no impacted nodes from a defines-only edge, got %+v", res.Impacted)
	}
}
