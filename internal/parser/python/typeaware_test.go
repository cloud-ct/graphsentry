package python

import (
	"testing"

	"github.com/huandert/repolens/internal/parser"
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
