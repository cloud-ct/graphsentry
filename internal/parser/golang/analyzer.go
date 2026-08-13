// Package golang implements parser.LanguageAnalyzer for Go source files
// using tree-sitter.
package golang

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"

	"github.com/huandert/repolens/internal/parser"
)

// Analyzer extracts functions, methods, types, and imports from Go files.
type Analyzer struct{}

// New returns a Go LanguageAnalyzer.
func New() *Analyzer { return &Analyzer{} }

func (a *Analyzer) Language() string     { return "go" }
func (a *Analyzer) Extensions() []string { return []string{".go"} }

// Analyze parses a Go source file and extracts top-level funcs, methods,
// type declarations, imports, and best-effort call edges (identifiers and
// selector expressions invoked within a function body).
func (a *Analyzer) Analyze(path string, content []byte) (*parser.FileAnalysis, error) {
	p := sitter.NewParser()
	p.SetLanguage(golang.GetLanguage())
	tree, err := p.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	defer tree.Close()

	fa := &parser.FileAnalysis{Path: path, Language: "go"}
	root := tree.RootNode()
	src := content

	line := func(n *sitter.Node) (int, int) {
		return int(n.StartPoint().Row) + 1, int(n.EndPoint().Row) + 1
	}
	text := func(n *sitter.Node) string { return n.Content(src) }

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Type() {
		case "import_spec":
			pathNode := n.ChildByFieldName("path")
			if pathNode != nil {
				imp := strings.Trim(text(pathNode), `"`)
				startLine, _ := line(n)
				fa.Imports = append(fa.Imports, parser.Import{Path: imp, Line: startLine})
			}
		case "function_declaration":
			nameNode := n.ChildByFieldName("name")
			if nameNode != nil {
				name := text(nameNode)
				start, end := line(n)
				sym := parser.Symbol{
					Kind:      parser.KindFunction,
					Name:      name,
					Qualified: name,
					StartLine: start,
					EndLine:   end,
					Signature: signatureLine(text(n)),
					Calls:     extractCalls(n, src),
				}
				fa.Symbols = append(fa.Symbols, sym)
			}
		case "method_declaration":
			nameNode := n.ChildByFieldName("name")
			recvNode := n.ChildByFieldName("receiver")
			if nameNode != nil {
				name := text(nameNode)
				recvType := receiverType(recvNode, src)
				qualified := name
				if recvType != "" {
					qualified = recvType + "." + name
				}
				start, end := line(n)
				sym := parser.Symbol{
					Kind:      parser.KindMethod,
					Name:      name,
					Qualified: qualified,
					StartLine: start,
					EndLine:   end,
					Signature: signatureLine(text(n)),
					Calls:     extractCalls(n, src),
				}
				fa.Symbols = append(fa.Symbols, sym)
			}
		case "type_spec":
			nameNode := n.ChildByFieldName("name")
			if nameNode != nil {
				name := text(nameNode)
				start, end := line(n)
				kind := parser.KindType
				var implements []string
				if iface := n.ChildByFieldName("type"); iface != nil && iface.Type() == "interface_type" {
					kind = parser.KindInterface
				}
				fa.Symbols = append(fa.Symbols, parser.Symbol{
					Kind:       kind,
					Name:       name,
					Qualified:  name,
					StartLine:  start,
					EndLine:    end,
					Signature:  signatureLine(text(n)),
					Implements: implements,
				})
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)

	return fa, nil
}

// receiverType extracts "Foo" from a receiver clause like "(f *Foo)" or "(f Foo)".
func receiverType(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	txt := n.Content(src)
	txt = strings.Trim(txt, "()")
	fields := strings.Fields(txt)
	if len(fields) == 0 {
		return ""
	}
	t := fields[len(fields)-1]
	return strings.TrimPrefix(t, "*")
}

// signatureLine returns just the first line of a declaration (up to the
// opening brace), used as a compact human-readable signature.
func signatureLine(full string) string {
	if idx := strings.Index(full, "{"); idx >= 0 {
		full = full[:idx]
	}
	return strings.TrimSpace(strings.Join(strings.Fields(full), " "))
}

// extractCalls walks a function/method body and collects the names of
// invoked functions/methods (identifier or selector before a call
// expression's arguments). This is a heuristic, not full type resolution:
// the graph builder resolves these names against known symbols in the repo.
func extractCalls(n *sitter.Node, src []byte) []parser.CallRef {
	seen := map[string]bool{}
	var calls []parser.CallRef
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "call_expression" {
			fn := n.ChildByFieldName("function")
			if fn != nil {
				name := callTarget(fn, src)
				if name != "" && !seen[name] {
					seen[name] = true
					calls = append(calls, parser.CallRef{Name: name, Line: int(n.StartPoint().Row) + 1})
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(n)
	return calls
}

func callTarget(fn *sitter.Node, src []byte) string {
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "selector_expression":
		field := fn.ChildByFieldName("field")
		if field != nil {
			return field.Content(src)
		}
	}
	return ""
}
