package graph

import (
	"testing"

	"github.com/huandert/repolens/internal/parser"
	"github.com/huandert/repolens/internal/parser/golang"
)

func TestBuilderResolvesCallsWithinFile(t *testing.T) {
	const src = `package service

func CreateUser(name string) error {
	validateName(name)
	return nil
}

func validateName(name string) bool {
	return name != ""
}
`
	a := golang.New()
	fa, err := a.Analyze("service.go", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}

	g := NewBuilder().Build([]*parser.FileAnalysis{fa})

	fileID := "file::service.go"
	if _, ok := g.Nodes[fileID]; !ok {
		t.Fatal("expected file node to exist")
	}

	createID := "symbol::service.go::CreateUser"
	validateID := "symbol::service.go::validateName"
	if _, ok := g.Nodes[createID]; !ok {
		t.Fatal("expected CreateUser symbol node")
	}
	if _, ok := g.Nodes[validateID]; !ok {
		t.Fatal("expected validateName symbol node")
	}

	foundCallEdge := false
	foundDefinesEdge := 0
	for _, e := range g.Edges {
		if e.From == createID && e.To == validateID && e.Kind == EdgeCalls {
			foundCallEdge = true
		}
		if e.From == fileID && e.Kind == EdgeDefines {
			foundDefinesEdge++
		}
	}
	if !foundCallEdge {
		t.Error("expected a calls edge from CreateUser to validateName")
	}
	if foundDefinesEdge != 2 {
		t.Errorf("expected 2 defines edges from file to symbols, got %d", foundDefinesEdge)
	}
}

// TestBuilderResolvesAmbiguousNamesWithinFile is a regression test for a
// bug where resolveName's same-file check compared the full symbol ID
// (which includes the qualified name, not just the file path) against
// candidates, so it never matched and same-file calls were silently
// dropped whenever the called name was ambiguous across the repo (e.g. two
// files each define a function with the same simple name).
func TestBuilderResolvesAmbiguousNamesWithinFile(t *testing.T) {
	const fileA = `package a

func Handler() error {
	process()
	return nil
}

func process() {}
`
	const fileB = `package b

func process() {}
`
	aa := golang.New()
	faA, err := aa.Analyze("a/handler.go", []byte(fileA))
	if err != nil {
		t.Fatalf("analyze a failed: %v", err)
	}
	faB, err := aa.Analyze("b/other.go", []byte(fileB))
	if err != nil {
		t.Fatalf("analyze b failed: %v", err)
	}

	g := NewBuilder().Build([]*parser.FileAnalysis{faA, faB})

	handlerID := "symbol::a/handler.go::Handler"
	wantID := "symbol::a/handler.go::process"

	found := false
	for _, e := range g.Edges {
		if e.From == handlerID && e.Kind == EdgeCalls {
			if e.To != wantID {
				t.Errorf("expected Handler to call the same-file process() (%s), got %s", wantID, e.To)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected a calls edge from Handler to process(), even though process() is ambiguous repo-wide")
	}
}
