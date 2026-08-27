package rules

import (
	"testing"

	"github.com/cloud-ct/graphsentry/internal/graph"
	"github.com/cloud-ct/graphsentry/internal/parser"
	"github.com/cloud-ct/graphsentry/internal/parser/csharp"
	"github.com/cloud-ct/graphsentry/internal/security"
)

// TestAspNetRulesEndToEnd runs the full pipeline — real tree-sitter parsing,
// then the real Builder, then both rules — against a fixture modeled
// directly on bankme-ai-main's actual controllers: a class-level
// [Authorize(...)], a custom [ApiKeyAuthorize(...)] backed by a
// TypeFilterAttribute + IAuthorizationFilter pair, an [AllowAnonymous]
// override, and one action left with no guard at all (the
// DiagnosticController pattern that motivated this feature).
func TestAspNetRulesEndToEnd(t *testing.T) {
	controller := `[ApiController]
[Route("[controller]")]
public class DiagnosticController : ControllerBase
{
    [HttpGet("admin-only")]
    [Authorize(Roles = "bankme-application-admin")]
    public async Task<IActionResult> AdminOnly() { return Ok(); }

    [HttpPost("from-tally")]
    [ApiKeyAuthorize("N8N")]
    public async Task<IActionResult> CreateFromTally() { return Ok(); }

    [HttpGet("public-report")]
    [AllowAnonymous]
    public async Task<IActionResult> PublicReport() { return Ok(); }

    [HttpGet("by-code")]
    public async Task<IActionResult> GetByCode() { return Ok(); }
}
`
	authFiles := `using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.Filters;

public class ApiKeyAuthorizeAttribute : TypeFilterAttribute
{
    public ApiKeyAuthorizeAttribute(string keyName) : base(typeof(ApiKeyAuthorizeFilter))
    {
        Arguments = [keyName];
    }
}

public class ApiKeyAuthorizeFilter : IAuthorizationFilter
{
    public void OnAuthorization(AuthorizationFilterContext context) {}
}
`
	a := csharp.New()
	controllerFA, err := a.Analyze("Controllers/DiagnosticController.cs", []byte(controller))
	if err != nil {
		t.Fatalf("Analyze(controller): %v", err)
	}
	authFA, err := a.Analyze("Auth/ApiKeyAuthorize.cs", []byte(authFiles))
	if err != nil {
		t.Fatalf("Analyze(auth): %v", err)
	}

	g := graph.NewBuilder().Build([]*parser.FileAnalysis{controllerFA, authFA})

	findings := security.Analyze(g, AspNetAuthorize{}, AspNetCustomFilter{})
	if len(findings) != 4 {
		t.Fatalf("expected 4 endpoint findings, got %d", len(findings))
	}

	byRoute := make(map[string]security.EndpointFinding, len(findings))
	for _, f := range findings {
		byRoute[f.Endpoint.Qualified] = f
	}

	cases := []struct {
		route      string
		wantStatus security.Status
	}{
		{"GET Diagnostic/admin-only", security.StatusProtected},
		{"POST Diagnostic/from-tally", security.StatusProtected}, // the custom filter, not [Authorize]
		{"GET Diagnostic/public-report", security.StatusPublic},
		{"GET Diagnostic/by-code", security.StatusUnprotected}, // the real finding
	}
	for _, c := range cases {
		f, ok := byRoute[c.route]
		if !ok {
			t.Errorf("%s: no finding (routes seen: %v)", c.route, routeKeys(byRoute))
			continue
		}
		if f.Status != c.wantStatus {
			t.Errorf("%s: got status %q, want %q (guards: %+v)", c.route, f.Status, c.wantStatus, f.Guards)
		}
	}

	tally := byRoute["POST Diagnostic/from-tally"]
	if len(tally.Guards) != 1 || tally.Guards[0].GuardName != "ApiKeyAuthorize(N8N)" {
		t.Errorf("from-tally: expected guard ApiKeyAuthorize(N8N), got %+v", tally.Guards)
	}
}

func routeKeys(m map[string]security.EndpointFinding) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
