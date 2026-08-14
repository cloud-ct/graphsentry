package typescript

import (
	"testing"

	"github.com/cloud-ct/graphsentry/internal/parser"
)

const sample = `import { UserService } from "./user.service";
import express from "express";

const router = express.Router();

router.post("/users", (req, res) => {
	userService.createUser(req.body);
});

class UsersController {
	create(req, res) {
		this.userService.createUser(req.body);
	}
}
`

func TestAnalyze(t *testing.T) {
	a := New()
	fa, err := a.Analyze("controllers/users.ts", []byte(sample))
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(fa.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d: %+v", len(fa.Imports), fa.Imports)
	}

	var endpoint, class, method *parser.Symbol
	for i := range fa.Symbols {
		sym := &fa.Symbols[i]
		switch sym.Kind {
		case parser.KindEndpoint:
			endpoint = sym
		case parser.KindClass:
			class = sym
		case parser.KindMethod:
			method = sym
		}
	}

	if endpoint == nil {
		t.Fatal("expected to detect an endpoint")
	}
	if endpoint.Name != "POST /users" {
		t.Errorf("expected endpoint 'POST /users', got %s", endpoint.Name)
	}
	if class == nil || class.Name != "UsersController" {
		t.Fatalf("expected class UsersController, got %+v", class)
	}
	if method == nil || method.Qualified != "UsersController.create" {
		t.Fatalf("expected method UsersController.create, got %+v", method)
	}
}
