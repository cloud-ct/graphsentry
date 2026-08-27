package diagram

import (
	"strings"
	"testing"

	"github.com/cloud-ct/graphsentry/internal/graph"
)

// TestMermaidFlowEscapesRouteParams is a regression test for a broken
// Mermaid diagram whenever an endpoint's route has ASP.NET-style
// placeholders — e.g. "GET Diagnostic/{id}/validate/{otp}" — since Mermaid
// reads a bare "{"/"}" as its own decision-node syntax even inside a
// quoted label, producing a parser error client-side ("Syntax error in
// text") instead of a diagram.
func TestMermaidFlowEscapesRouteParams(t *testing.T) {
	endpoint := &graph.Node{ID: "ep", Kind: graph.NodeEndpoint, Name: `GET Diagnostic/{id}/validate/{otp}`}
	handler := &graph.Node{ID: "handler", Kind: graph.NodeMethod, Name: "GetByCode", Qualified: "DiagnosticController.GetByCode"}

	paths := [][]*graph.PathStep{
		{{Node: endpoint}, {Node: handler, Via: graph.EdgeCalls}},
	}

	out := MermaidFlow(paths)

	if strings.Contains(out, "{id}") || strings.Contains(out, "{otp}") {
		t.Errorf("expected route braces to be escaped, got unescaped braces in:\n%s", out)
	}
	if !strings.Contains(out, "#123;id#125;") || !strings.Contains(out, "#123;otp#125;") {
		t.Errorf("expected Mermaid entity-code escaping (#123;/#125;) for the route's braces, got:\n%s", out)
	}

	// Node IDs aren't quoted at all in Mermaid flowchart syntax (unlike
	// labels), so a "{"/"}" reaching one is unsafe regardless of label
	// escaping — this is the part of the bug label-escaping alone doesn't
	// cover.
	for _, line := range strings.Split(out, "\n") {
		id := strings.Fields(line)
		if len(id) == 0 {
			continue
		}
		if strings.ContainsAny(id[0], "{}") {
			t.Errorf("expected no raw braces in a Mermaid node ID, got line: %q", line)
		}
	}
}

// TestMermaidFlowEscapesQuotes covers the other case %q got wrong: Go's
// backslash-escaping of an embedded '"' produces literal "\"" in the
// output file, which Mermaid doesn't treat as an escaped quote — it just
// ends the label early. A name is unlikely to contain a literal quote in
// practice, but the escaping function should still get it right.
func TestMermaidFlowEscapesQuotes(t *testing.T) {
	n := &graph.Node{ID: "n", Kind: graph.NodeFunction, Name: `Say "hi"`}
	paths := [][]*graph.PathStep{{{Node: n}}}

	out := MermaidFlow(paths)

	if strings.Contains(out, `\"`) {
		t.Errorf(`expected no Go-style backslash-escaped quote in Mermaid output, got:%s`, out)
	}
	if !strings.Contains(out, "#quot;") {
		t.Errorf("expected the embedded quote to be escaped as #quot;, got:\n%s", out)
	}
}
