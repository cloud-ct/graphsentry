package golang

import (
	"testing"

	"github.com/huandert/repolens/internal/parser"
)

const sample = `package service

import (
	"fmt"
	"myapp/repo"
)

type UserService struct {
	repo repo.UserRepository
}

func (s *UserService) CreateUser(name string) error {
	fmt.Println(name)
	s.validate(name)
	return s.repo.Save(name)
}

func (s *UserService) validate(name string) bool {
	return name != ""
}

type Greeter interface {
	Greet() string
}
`

func TestAnalyze(t *testing.T) {
	a := New()
	fa, err := a.Analyze("service/user.go", []byte(sample))
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(fa.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d: %+v", len(fa.Imports), fa.Imports)
	}

	var methods, types, ifaces int
	var createUser *parser.Symbol
	for i := range fa.Symbols {
		sym := &fa.Symbols[i]
		switch sym.Kind {
		case parser.KindMethod:
			methods++
			if sym.Name == "CreateUser" {
				createUser = sym
			}
		case parser.KindType:
			types++
		case parser.KindInterface:
			ifaces++
		}
	}

	if methods != 2 {
		t.Errorf("expected 2 methods, got %d", methods)
	}
	if types != 1 {
		t.Errorf("expected 1 type, got %d", types)
	}
	if ifaces != 1 {
		t.Errorf("expected 1 interface, got %d", ifaces)
	}
	if createUser == nil {
		t.Fatal("expected to find CreateUser method")
	}
	if createUser.Qualified != "UserService.CreateUser" {
		t.Errorf("expected qualified name UserService.CreateUser, got %s", createUser.Qualified)
	}
	found := map[string]bool{}
	for _, c := range createUser.Calls {
		found[c] = true
	}
	for _, want := range []string{"Println", "validate", "Save"} {
		if !found[want] {
			t.Errorf("expected CreateUser to call %s, got calls: %v", want, createUser.Calls)
		}
	}
}

func TestExtensionsAndLanguage(t *testing.T) {
	a := New()
	if a.Language() != "go" {
		t.Errorf("expected language 'go', got %s", a.Language())
	}
	exts := a.Extensions()
	if len(exts) != 1 || exts[0] != ".go" {
		t.Errorf("expected extensions [.go], got %v", exts)
	}
}
