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

// TestEndpointRouteOnSeparateAttribute is a regression test for a real bug
// found against bankme-ai-main: when a method's HTTP-verb attribute has no
// inline route argument and the route instead lives on a separate
// [Route(...)] attribute (`[HttpPost] [Route("foo")]`, as opposed to
// `[HttpPost("foo")]`), the analyzer used the verb attribute's own (empty)
// argument and silently ignored the separate Route attribute — so every
// verb-only handler in a controller produced the *same* qualified endpoint
// name (just the class-level route, with no method-specific suffix). Since
// a node's ID is derived from its qualified name, two such handlers
// collided onto one graph node, and the second silently overwrote the
// first — `flow`/`impact` on either handler then showed a mix of both.
func TestEndpointRouteOnSeparateAttribute(t *testing.T) {
	const src = `[Route("[controller]")]
public class WebhookController : ControllerBase
{
    [HttpPost]
    public async Task<IActionResult> Send(Request request)
    {
        return Ok();
    }

    [HttpPost]
    [Route("special")]
    public async Task<IActionResult> SpecialCase(Request request)
    {
        return Ok();
    }
}
`
	a := New()
	fa, err := a.Analyze("Controllers/WebhookController.cs", []byte(src))
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	endpoints := map[string]bool{}
	for _, sym := range fa.Symbols {
		if sym.Kind == parser.KindEndpoint {
			endpoints[sym.Name] = true
		}
	}
	if endpoints["POST Webhook/special"] != true {
		t.Errorf("expected endpoint 'POST Webhook/special' (route from the separate [Route] attribute), got %v", endpoints)
	}
	if endpoints["POST Webhook"] != true {
		t.Errorf("expected endpoint 'POST Webhook' for Send (no route suffix), got %v", endpoints)
	}
	if len(endpoints) != 2 {
		t.Errorf("expected 2 distinct endpoints (a route collision would collapse them to 1), got %d: %v", len(endpoints), endpoints)
	}
}

// TestDeclarationLineSkipsAttributes is a regression test for a CodeLens
// anchored several lines above the method it describes: a method_declaration
// node's own StartPoint is wherever its *first* attribute sits, not the
// method signature, so a handler with several stacked ASP.NET attributes
// (a common pattern — [HttpPost], [Authorize], [EndpointSummary], ...)
// placed its graph node (and therefore its CodeLens) up near the first
// attribute, reading as if it annotated that whole block rather than the
// method itself.
func TestDeclarationLineSkipsAttributes(t *testing.T) {
	const src = `public class Controller
{
    [HttpPost]
    [Authorize(Roles = "admin")]
    [EndpointSummary("does a thing")]
    public async Task<IActionResult> Send(Request request)
    {
        return Ok();
    }
}
`
	a := New()
	fa, err := a.Analyze("Controller.cs", []byte(src))
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	var method *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Kind == parser.KindMethod {
			method = &fa.Symbols[i]
		}
	}
	if method == nil {
		t.Fatal("expected to find the Send method")
	}
	// "public async Task<IActionResult> Send(Request request)" is on line
	// 6 (1-based) — three attribute lines above it.
	if method.StartLine != 6 {
		t.Errorf("expected StartLine 6 (the method signature, past the 3 leading attributes), got %d", method.StartLine)
	}
}
