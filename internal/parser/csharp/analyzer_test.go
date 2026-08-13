package csharp

import (
	"testing"

	"github.com/huandert/repolens/internal/parser"
)

const sample = `using Microsoft.AspNetCore.Mvc;
using MyApp.Services;

namespace MyApp.Controllers
{
    [ApiController]
    [Route("api/[controller]")]
    public class UsersController : ControllerBase
    {
        private readonly IUserService _userService;

        [HttpPost]
        public async Task<IActionResult> Create(CreateUserRequest request)
        {
            var user = await _userService.CreateAsync(request);
            return Ok(user);
        }
    }

    public interface IUserService
    {
        Task<User> CreateAsync(CreateUserRequest request);
    }
}
`

func TestAnalyze(t *testing.T) {
	a := New()
	fa, err := a.Analyze("Controllers/UsersController.cs", []byte(sample))
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(fa.Imports) != 2 {
		t.Fatalf("expected 2 imports, got %d: %+v", len(fa.Imports), fa.Imports)
	}

	var class, iface, method, endpoint *parser.Symbol
	for i := range fa.Symbols {
		sym := &fa.Symbols[i]
		switch sym.Kind {
		case parser.KindClass:
			class = sym
		case parser.KindInterface:
			iface = sym
		case parser.KindMethod:
			if sym.Name == "Create" {
				method = sym
			}
		case parser.KindEndpoint:
			endpoint = sym
		}
	}

	if class == nil || class.Name != "UsersController" {
		t.Fatalf("expected class UsersController, got %+v", class)
	}
	if iface == nil || iface.Name != "IUserService" {
		t.Fatalf("expected interface IUserService, got %+v", iface)
	}
	if method == nil || method.Qualified != "UsersController.Create" {
		t.Fatalf("expected method UsersController.Create, got %+v", method)
	}
	if endpoint == nil {
		t.Fatal("expected to detect an endpoint")
	}
	if endpoint.Name != "POST api/Users" {
		t.Errorf("expected endpoint 'POST api/Users', got %s", endpoint.Name)
	}
}
