package csharp

import (
	"testing"

	"github.com/cloud-ct/repolens/internal/parser"
)

// TestTypeHints is a regression test for real-world confusions reported
// against bankme-ai-main:
//  1. `new Foo()` should be tracked separately from method calls (Creates,
//     not Calls) so diagrams don't label "constructing a value" the same
//     as "invoking a method".
//  2. a call through a field/local typed as a BCL/vendor type (not a
//     symbol anywhere in the repo) must get a receiver type hint, so the
//     graph builder can refuse to match it against an unrelated same-named
//     method elsewhere in the repo — the false "SendAsync calls itself"
//     edge this was built to prevent.
func TestTypeHints(t *testing.T) {
	const src = `class Worker {
    private readonly ILogger _logger;

    public async Task Run() {
        var ws = new ClientWebSocket();
        await ws.SendAsync(data, token);
        var m = new MetricDelta(1, 2);
        _logger.Log("hi");
    }

    public async Task SendAsync(byte[] data) {
        await Run();
    }
}
`
	a := New()
	fa, err := a.Analyze("worker.cs", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}

	var run *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Name == "Run" {
			run = &fa.Symbols[i]
		}
	}
	if run == nil {
		t.Fatal("expected to find Run method")
	}

	// `new ClientWebSocket()` and `new MetricDelta(1, 2)` go to Creates,
	// not Calls.
	if len(run.Creates) != 2 {
		t.Errorf("expected 2 Creates entries, got %d: %v", len(run.Creates), run.Creates)
	}
	creates := map[string]bool{}
	for _, c := range run.Creates {
		creates[c.TypeName] = true
	}
	if !creates["ClientWebSocket"] || !creates["MetricDelta"] {
		t.Errorf("expected Creates to include ClientWebSocket and MetricDelta, got %v", run.Creates)
	}

	// ws.SendAsync(...) must carry ReceiverType "ClientWebSocket" — a type
	// the analyzer can see is NOT defined anywhere in this file, which is
	// exactly the signal the graph builder needs to refuse resolving it
	// against Worker's own unrelated SendAsync method.
	var wsCall *parser.CallRef
	for i := range run.Calls {
		if run.Calls[i].Name == "SendAsync" {
			wsCall = &run.Calls[i]
		}
	}
	if wsCall == nil {
		t.Fatal("expected a SendAsync call")
	}
	if wsCall.ReceiverType != "ClientWebSocket" {
		t.Errorf("expected SendAsync call to have ReceiverType ClientWebSocket, got %q", wsCall.ReceiverType)
	}

	// _logger.Log(...) — field typed ILogger — should carry ReceiverType
	// "ILogger" too.
	var logCall *parser.CallRef
	for i := range run.Calls {
		if run.Calls[i].Name == "Log" {
			logCall = &run.Calls[i]
		}
	}
	if logCall == nil {
		t.Fatal("expected a Log call")
	}
	if logCall.ReceiverType != "ILogger" {
		t.Errorf("expected Log call to have ReceiverType ILogger, got %q", logCall.ReceiverType)
	}
}

// TestInterfaceRoutedCall verifies that a call through an interface-typed
// field resolves to the interface's implementer via the graph, not just
// the analyzer's per-file output (see builder_test.go in internal/graph
// for the full end-to-end assertion). This test only checks the analyzer
// half: the call must carry the interface name as ReceiverType so the
// builder has something to route with.
func TestInterfaceRoutedCallHasReceiverType(t *testing.T) {
	const src = `class UsersController {
    private readonly IUserService _userService;

    public async Task Create() {
        await _userService.CreateUserAsync();
    }
}
`
	a := New()
	fa, err := a.Analyze("controller.cs", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	var create *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Name == "Create" {
			create = &fa.Symbols[i]
		}
	}
	if create == nil {
		t.Fatal("expected to find Create method")
	}
	if len(create.Calls) != 1 || create.Calls[0].Name != "CreateUserAsync" || create.Calls[0].ReceiverType != "IUserService" {
		t.Errorf("expected a single CreateUserAsync call with ReceiverType IUserService, got %+v", create.Calls)
	}
}

// TestPrimaryConstructorParametersAreTypeHints is a regression test for a
// real confusion found against bankme-ai-main: services there commonly use
// C# 12 primary constructors — `class AppService(IAppRepository
// appRepository, ...) : IAppService` — where the parameter is usable like
// a field throughout the class body. tree-sitter-c-sharp represents this
// parameter_list as a direct child of class_declaration, distinct from the
// declaration_list body that classFieldTypes scans, so without explicit
// handling these parameters got no type hint at all — falling back to the
// bare-name heuristic, which (since the interface's own abstract method
// declaration commonly lives in the same file as its implementation)
// wrongly resolved calls to the interface's stub instead of the real
// implementation elsewhere in the repo.
func TestPrimaryConstructorParametersAreTypeHints(t *testing.T) {
	const src = `public interface IAppRepository {
    Task<List<App>> GetAllAsync();
}

public class AppService(IAppRepository appRepository) : IAppService {
    public async Task<List<AppResponse>> GetAllAsync() {
        var apps = await appRepository.GetAllAsync();
        return null;
    }
}
`
	a := New()
	fa, err := a.Analyze("appservice.cs", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	var method *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Qualified == "AppService.GetAllAsync" {
			method = &fa.Symbols[i]
		}
	}
	if method == nil {
		t.Fatal("expected to find AppService.GetAllAsync method")
	}
	var call *parser.CallRef
	for i := range method.Calls {
		if method.Calls[i].Name == "GetAllAsync" {
			call = &method.Calls[i]
		}
	}
	if call == nil {
		t.Fatal("expected a GetAllAsync call")
	}
	if call.ReceiverType != "IAppRepository" {
		t.Errorf("expected the call through the primary-constructor parameter to carry ReceiverType IAppRepository, got %q", call.ReceiverType)
	}
}

// TestLocalFunctionCallsAreExcluded is a regression test for a false
// dependent edge found against bankme-ai-main: a class declared a local
// function named Send (nested inside another method), and bare calls to
// it resolved — via the bare-name heuristic, since local functions aren't
// tracked as symbols at all — to a completely unrelated class elsewhere in
// the repo that happened to be the only OTHER symbol named "Send",
// wrongly inflating that unrelated method's dependent count.
func TestLocalFunctionCallsAreExcluded(t *testing.T) {
	const src = `class Worker {
    public async Task Run() {
        Task Send(object payload) {
            return null;
        }
        await Send(new { });
    }
}
`
	a := New()
	fa, err := a.Analyze("worker.cs", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	var run *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Name == "Run" {
			run = &fa.Symbols[i]
		}
	}
	if run == nil {
		t.Fatal("expected to find Run method")
	}
	for _, c := range run.Calls {
		if c.Name == "Send" {
			t.Errorf("expected no CallRef for the local function Send, got %+v", c)
		}
	}
}
