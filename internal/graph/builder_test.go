package graph

import (
	"testing"

	"github.com/cloud-ct/graphsentry/internal/parser"
	"github.com/cloud-ct/graphsentry/internal/parser/csharp"
	"github.com/cloud-ct/graphsentry/internal/parser/golang"
	"github.com/cloud-ct/graphsentry/internal/parser/python"
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

// TestBuilderRoutesInterfaceCallToImplementer is an end-to-end regression
// test for the "controller calls a service through an injected interface"
// case reported against bankme-ai-main: the call wasn't mapped at all
// because bare-name resolution saw the method name as ambiguous (or
// unresolved) and silently dropped it. With a receiver type hint, the call
// should route through the Implements edge to the concrete class.
func TestBuilderRoutesInterfaceCallToImplementer(t *testing.T) {
	const controllerSrc = `class UsersController {
    private readonly IUserService _userService;

    public async Task Create() {
        await _userService.CreateUserAsync();
    }
}
`
	const interfaceSrc = `interface IUserService {
    Task CreateUserAsync();
}
`
	const serviceSrc = `class UserService : IUserService {
    public async Task CreateUserAsync() {
    }
}
`
	a := csharp.New()
	controllerFA, err := a.Analyze("controller.cs", []byte(controllerSrc))
	if err != nil {
		t.Fatalf("analyze controller failed: %v", err)
	}
	interfaceFA, err := a.Analyze("iuserservice.cs", []byte(interfaceSrc))
	if err != nil {
		t.Fatalf("analyze interface failed: %v", err)
	}
	serviceFA, err := a.Analyze("userservice.cs", []byte(serviceSrc))
	if err != nil {
		t.Fatalf("analyze service failed: %v", err)
	}

	g := NewBuilder().Build([]*parser.FileAnalysis{controllerFA, interfaceFA, serviceFA})

	createID := "symbol::controller.cs::UsersController.Create"
	implID := "symbol::userservice.cs::UserService.CreateUserAsync"

	found := false
	for _, e := range g.Edges {
		if e.From == createID && e.Kind == EdgeCalls {
			if e.To != implID {
				t.Errorf("expected Create to call the implementer %s, got %s", implID, e.To)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected a calls edge from UsersController.Create to UserService.CreateUserAsync, routed via the IUserService interface")
	}
}

// TestBuilderRefusesForeignReceiverType is a regression test for a false
// edge reported against bankme-ai-main: a call on a locally-`new`'d BCL
// type (ClientWebSocket.SendAsync) was wrongly resolved to the user's own
// unrelated SendAsync method because resolution only compared bare method
// names. Once the receiver's declared type is known and isn't a symbol
// anywhere in the repo, resolution must refuse rather than guess.
func TestBuilderRefusesForeignReceiverType(t *testing.T) {
	const src = `class Worker {
    public async Task Run() {
        var ws = new ClientWebSocket();
        await ws.SendAsync(data, token);
    }

    public async Task SendAsync(byte[] data) {
    }
}
`
	a := csharp.New()
	fa, err := a.Analyze("worker.cs", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}

	g := NewBuilder().Build([]*parser.FileAnalysis{fa})

	runID := "symbol::worker.cs::Worker.Run"
	wrongTargetID := "symbol::worker.cs::Worker.SendAsync"

	for _, e := range g.Edges {
		if e.From == runID && e.To == wrongTargetID && e.Kind == EdgeCalls {
			t.Errorf("expected no calls edge from Run to Worker's own SendAsync (ws is a ClientWebSocket, unrelated), but found one")
		}
	}
}

// TestBuilderPreservesSourceOrderAcrossCallsAndCreates is a regression
// test for calls and instantiations rendering out of order in flow
// diagrams: extraction collects them into two separate slices
// (parser.Symbol.Calls / .Creates), and edges used to be added "every
// call, then every instantiation" regardless of how they're actually
// interleaved in the source. mergeBySourceOrder fixes this by sorting on
// each entry's captured source line before edges are added — this checks
// that fix holds for a method that calls, instantiates, then calls again,
// which would come out as [call, call, instantiate] without it.
func TestBuilderPreservesSourceOrderAcrossCallsAndCreates(t *testing.T) {
	const src = `class Worker {
    public void Run() {
        First();
        var m = new Middle();
        Last();
    }

    public void First() {}
    public void Last() {}
}

class Middle {}
`
	a := csharp.New()
	fa, err := a.Analyze("worker.cs", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}

	g := NewBuilder().Build([]*parser.FileAnalysis{fa})

	runID := "symbol::worker.cs::Worker.Run"
	var gotOrder []string
	for _, e := range g.Out(runID) {
		gotOrder = append(gotOrder, string(e.Kind)+":"+g.Nodes[e.To].Name)
	}

	want := []string{"calls:First", "instantiates:Middle", "calls:Last"}
	if len(gotOrder) != len(want) {
		t.Fatalf("expected edges %v, got %v", want, gotOrder)
	}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Errorf("expected edge order %v, got %v", want, gotOrder)
			break
		}
	}
}

// TestBuilderResolvesPythonSelfAttrCallCorrectly is an end-to-end
// regression test for a false edge reported against a real Flask app:
// ChatService.chat_message showed a call to the unrelated
// ChatController.create. ChatController happens to define the repo's
// only method named "create", so the Python analyzer's old
// no-receiver-type-ever call extraction fell through to the bare-name
// heuristic's "exactly one candidate" shortcut and confidently — wrongly
// — resolved there, even though chat_message's real self.chat_controller
// is (in this test) a *different* class that also defines create(). The
// receiver-type hint from `self.chat_controller = ...(...)` must route
// the call to the correct implementer, not the merely name-unique one.
func TestBuilderResolvesPythonSelfAttrCallCorrectly(t *testing.T) {
	const controllerSrc = `class ChatController:
    def create(self):
        pass
`
	const serviceSrc = `class RealTarget:
    def create(self):
        pass

class ChatService:
    def __init__(self):
        self.target = RealTarget()

    def chat_message(self):
        self.target.create()
`
	a := python.New()
	controllerFA, err := a.Analyze("chat_controller.py", []byte(controllerSrc))
	if err != nil {
		t.Fatalf("analyze controller failed: %v", err)
	}
	serviceFA, err := a.Analyze("chat_service.py", []byte(serviceSrc))
	if err != nil {
		t.Fatalf("analyze service failed: %v", err)
	}

	g := NewBuilder().Build([]*parser.FileAnalysis{controllerFA, serviceFA})

	chatMessageID := "symbol::chat_service.py::ChatService.chat_message"
	wrongTargetID := "symbol::chat_controller.py::ChatController.create"
	rightTargetID := "symbol::chat_service.py::RealTarget.create"

	var gotTarget string
	for _, e := range g.Out(chatMessageID, EdgeCalls) {
		gotTarget = e.To
	}
	if gotTarget == wrongTargetID {
		t.Errorf("expected chat_message's create() call to resolve to RealTarget.create, resolved to the unrelated ChatController.create instead")
	}
	if gotTarget != rightTargetID {
		t.Errorf("expected chat_message to call %s, got %q", rightTargetID, gotTarget)
	}
}

// TestBuilderResolvesModuleLevelSingleton is an end-to-end regression test
// for the cross-file Python singleton-service pattern: chat_service.py
// constructs `chat_service = ChatService()` at module scope, and
// chat_controller.py (having imported that name) calls
// `chat_service.create_assistant()`. Neither file's analyzer pass can see
// the other side on its own — the graph builder has to reconcile
// parser.CallRef.ReceiverVar against every file's
// parser.FileAnalysis.ModuleVars once it's seen them all.
func TestBuilderResolvesModuleLevelSingleton(t *testing.T) {
	const controllerSrc = `def create():
    request = chat_service.create_assistant()
    return request
`
	const serviceSrc = `class ChatService:
    def create_assistant(self):
        pass

chat_service = ChatService()
`
	a := python.New()
	controllerFA, err := a.Analyze("chat_controller.py", []byte(controllerSrc))
	if err != nil {
		t.Fatalf("analyze controller failed: %v", err)
	}
	serviceFA, err := a.Analyze("chat_service.py", []byte(serviceSrc))
	if err != nil {
		t.Fatalf("analyze service failed: %v", err)
	}

	g := NewBuilder().Build([]*parser.FileAnalysis{controllerFA, serviceFA})

	createID := "symbol::chat_controller.py::create"
	wantTargetID := "symbol::chat_service.py::ChatService.create_assistant"

	var gotTarget string
	for _, e := range g.Out(createID, EdgeCalls) {
		gotTarget = e.To
	}
	if gotTarget != wantTargetID {
		t.Errorf("expected create() to call %s (via the chat_service module-level singleton), got %q", wantTargetID, gotTarget)
	}
}
