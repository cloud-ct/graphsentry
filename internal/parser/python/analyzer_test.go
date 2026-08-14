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
        # user_service is imported from another file, so its type isn't
        # locally inferable — the analyzer tags it with ReceiverVar
        # instead of ReceiverType, for the graph builder to resolve
        # cross-file against every analyzed file's module-level vars.
        user = user_service.create_user()
        service = UserService()
        service.notify()
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
	// create_user()'s receiver (user_service, imported from elsewhere) has
	// no locally-inferable type — it should carry ReceiverVar for the
	// graph builder to try resolving cross-file, not a ReceiverType, and
	// it must NOT have fallen back to the old bare-name heuristic.
	foundCreateUser := false
	for _, c := range method.Calls {
		if c.Name == "create_user" {
			foundCreateUser = true
			if c.ReceiverVar != "user_service" || c.ReceiverType != "" {
				t.Errorf("expected create_user() to carry ReceiverVar=user_service and no ReceiverType, got %+v", c)
			}
		}
	}
	if !foundCreateUser {
		t.Errorf("expected a create_user() call, got calls: %v", method.Calls)
	}
	// notify() on a locally-constructed UserService() resolves directly —
	// ReceiverType, not ReceiverVar.
	foundNotify := false
	for _, c := range method.Calls {
		if c.Name == "notify" && c.ReceiverType == "UserService" {
			foundNotify = true
		}
	}
	if !foundNotify {
		t.Errorf("expected create() to call notify() with ReceiverType UserService, got calls: %v", method.Calls)
	}
}
