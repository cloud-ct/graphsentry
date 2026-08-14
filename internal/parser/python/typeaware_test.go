package python

import (
	"testing"

	"github.com/cloud-ct/graphsentry/internal/parser"
)

// TestSelfAttrCallHasReceiverType is a regression test for a false edge
// reported against a real Flask app: ChatService.chat_message showed a
// call to the unrelated ChatController.create, because the Python
// analyzer never tracked any receiver type information at all — every
// call, hinted or not, fell straight through to the bare-name heuristic.
// A call through self.<attr> where <attr> was constructed via
// `self.<attr> = ClassName(...)` must now carry ClassName as
// ReceiverType, so the graph builder can resolve it (or refuse to, if
// ClassName isn't the call's real definer) instead of guessing from every
// same-named method in the repo.
func TestSelfAttrCallHasReceiverType(t *testing.T) {
	const src = `class ChatService:
    def __init__(self):
        self.chat_controller = ChatController()

    def chat_message(self, a):
        self.chat_controller.create(a)
`
	a := New()
	fa, err := a.Analyze("chat_service.py", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	var method *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Qualified == "ChatService.chat_message" {
			method = &fa.Symbols[i]
		}
	}
	if method == nil {
		t.Fatal("expected to find ChatService.chat_message")
	}
	if len(method.Calls) != 1 || method.Calls[0].Name != "create" || method.Calls[0].ReceiverType != "ChatController" {
		t.Errorf("expected a single create() call with ReceiverType ChatController, got %+v", method.Calls)
	}
}

// TestLocalVarCallHasReceiverType mirrors TestSelfAttrCallHasReceiverType
// for a locally-constructed variable rather than a self attribute.
func TestLocalVarCallHasReceiverType(t *testing.T) {
	const src = `def handler():
    controller = ChatController()
    controller.create()
`
	a := New()
	fa, err := a.Analyze("handler.py", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	var fn *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Name == "handler" {
			fn = &fa.Symbols[i]
		}
	}
	if fn == nil {
		t.Fatal("expected to find handler function")
	}
	var call *parser.CallRef
	for i := range fn.Calls {
		if fn.Calls[i].Name == "create" {
			call = &fn.Calls[i]
		}
	}
	if call == nil {
		t.Fatal("expected a create() call")
	}
	if call.ReceiverType != "ChatController" {
		t.Errorf("expected ReceiverType ChatController, got %q", call.ReceiverType)
	}
}

// TestDeepAttributeChainCallIsDropped is a regression test for the exact
// real-world bug reported: `self.client = OpenAI()` followed by a call
// several attributes deep — `self.client.beta.threads.create()` — showed
// up as a call to an unrelated ChatController.create, because the
// analyzer's first pass at receiver-type tracking only handled a single
// level of attribute access (self.attr.method() or local.method()) and
// silently fell through to the unsafe bare-name heuristic for anything
// deeper. A chain more than one attribute past a tracked receiver has an
// intermediate type (here, whatever `self.client.beta` evaluates to) the
// analyzer has no way to know — the fix is to drop such calls entirely
// rather than guess, which this asserts directly: no CallRef for
// "create" should be produced at all.
func TestDeepAttributeChainCallIsDropped(t *testing.T) {
	const src = `class ChatService:
    def __init__(self):
        self.client = OpenAI()

    def chat_message(self):
        thread = self.client.beta.threads.create()
        return thread
`
	a := New()
	fa, err := a.Analyze("chat_service.py", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	var method *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Qualified == "ChatService.chat_message" {
			method = &fa.Symbols[i]
		}
	}
	if method == nil {
		t.Fatal("expected to find ChatService.chat_message")
	}
	for _, c := range method.Calls {
		if c.Name == "create" {
			t.Errorf("expected no CallRef for the deep-chain create() call, got %+v", c)
		}
	}
}

// TestSelfMethodCallHasClassReceiverType verifies that self.method() —
// calling another method of the same class — carries the class's own
// name as ReceiverType (mirroring the C# analyzer's this_expression
// handling), so it resolves precisely instead of via the bare-name
// heuristic.
func TestSelfMethodCallHasClassReceiverType(t *testing.T) {
	const src = `class Worker:
    def run(self):
        self.helper()

    def helper(self):
        pass
`
	a := New()
	fa, err := a.Analyze("worker.py", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	var run *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Qualified == "Worker.run" {
			run = &fa.Symbols[i]
		}
	}
	if run == nil {
		t.Fatal("expected to find Worker.run")
	}
	if len(run.Calls) != 1 || run.Calls[0].Name != "helper" || run.Calls[0].ReceiverType != "Worker" {
		t.Errorf("expected a single helper() call with ReceiverType Worker, got %+v", run.Calls)
	}
}

// TestModuleVarConstructorIsTracked verifies that a module-level
// `name = ClassName(...)` assignment is captured in FileAnalysis.ModuleVars
// — this is the per-file half of the cross-file singleton-service pattern
// (`chat_service = ChatService()` at the bottom of chat_service.py,
// imported and called elsewhere); the graph builder reconciles these
// across every analyzed file (see TestBuilderResolvesModuleLevelSingleton
// in internal/graph).
func TestModuleVarConstructorIsTracked(t *testing.T) {
	const src = `class ChatService:
    def create_assistant(self):
        pass

chat_service = ChatService()
`
	a := New()
	fa, err := a.Analyze("chat_service.py", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	found := false
	for _, mv := range fa.ModuleVars {
		if mv.Name == "chat_service" && mv.TypeName == "ChatService" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ModuleVars to include chat_service -> ChatService, got %+v", fa.ModuleVars)
	}
}

// TestModuleConstructorCallFallsBackToBareName verifies the
// `module_alias.ClassName(...)` pattern — constructing a class reached
// through an imported module — resolves via the plain bare-name
// heuristic (keyed on the PascalCase class name, not the receiver), since
// there's no receiver type to track here at all: the "receiver" is a
// module reference, not an object instance.
func TestModuleConstructorCallFallsBackToBareName(t *testing.T) {
	const src = `def build():
    request = create_assistant_request.CreateAssistantRequest(name="x")
    return request
`
	a := New()
	fa, err := a.Analyze("builder.py", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	var fn *parser.Symbol
	for i := range fa.Symbols {
		if fa.Symbols[i].Name == "build" {
			fn = &fa.Symbols[i]
		}
	}
	if fn == nil {
		t.Fatal("expected to find build function")
	}
	var call *parser.CallRef
	for i := range fn.Calls {
		if fn.Calls[i].Name == "CreateAssistantRequest" {
			call = &fn.Calls[i]
		}
	}
	if call == nil {
		t.Fatal("expected a CreateAssistantRequest call")
	}
	if call.ReceiverType != "" || call.ReceiverVar != "" {
		t.Errorf("expected no receiver type/var (bare-name fallback expected), got %+v", call)
	}
}

// TestModuleVarConstructedViaModuleAttribute is a regression test for the
// exact real-world pattern found in bankme-ai-assistant-service:
//
//	from openai_application.service import chat_service
//	chat_service = chat_service.ChatService()
//
// The name `chat_service` is first imported as a *module* (the file
// chat_service.py), then immediately shadowed at module scope by an
// instance of the class it defines — constructed by reaching through that
// same module reference (`chat_service.ChatService()`, an attribute-form
// call, not the bare `ClassName()` form collectModuleVarTypes originally
// only recognized). Without handling this form, the module-var registry
// never learned that `chat_service` was a ChatService instance at all,
// so calls through it kept falling through to ReceiverVar resolution
// with no match.
func TestModuleVarConstructedViaModuleAttribute(t *testing.T) {
	const src = `from openai_application.service import chat_service

class ChatService:
    def create_assistant(self):
        pass

chat_service = chat_service.ChatService()
`
	a := New()
	fa, err := a.Analyze("chat_controller.py", []byte(src))
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}
	found := false
	for _, mv := range fa.ModuleVars {
		if mv.Name == "chat_service" && mv.TypeName == "ChatService" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ModuleVars to include chat_service -> ChatService (via the module-attribute constructor form), got %+v", fa.ModuleVars)
	}
}
