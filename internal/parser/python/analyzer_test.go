package python

import (
	"testing"

	"github.com/huandert/repolens/internal/parser"
)

const sample = `from flask import Blueprint
import logging
from application.service import user_service

bp = Blueprint("users", __name__)

class UsersController:

    @bp.route('/users', methods=["POST"])
    def create():
        user = user_service.create_user()
        return user

def helper(x):
    return x + 1
`

func TestAnalyze(t *testing.T) {
	a := New()
	fa, err := a.Analyze("controllers/users.py", []byte(sample))
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(fa.Imports) != 3 {
		t.Fatalf("expected 3 imports, got %d: %+v", len(fa.Imports), fa.Imports)
	}

	var class, method, fn, endpoint *parser.Symbol
	for i := range fa.Symbols {
		sym := &fa.Symbols[i]
		switch sym.Kind {
		case parser.KindClass:
			class = sym
		case parser.KindMethod:
			method = sym
		case parser.KindFunction:
			fn = sym
		case parser.KindEndpoint:
			endpoint = sym
		}
	}

	if class == nil || class.Name != "UsersController" {
		t.Fatalf("expected class UsersController, got %+v", class)
	}
	if method == nil || method.Qualified != "UsersController.create" {
		t.Fatalf("expected method UsersController.create, got %+v", method)
	}
	if fn == nil || fn.Name != "helper" {
		t.Fatalf("expected function helper, got %+v", fn)
	}
	if endpoint == nil {
		t.Fatal("expected to detect an endpoint")
	}
	if endpoint.Name != "POST /users" {
		t.Errorf("expected endpoint 'POST /users', got %s", endpoint.Name)
	}
	found := false
	for _, c := range method.Calls {
		if c == "create_user" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected create() to call create_user, got calls: %v", method.Calls)
	}
}
