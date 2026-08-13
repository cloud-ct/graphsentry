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
