// Package typescript implements parser.LanguageAnalyzer for TypeScript and
// JavaScript source files using tree-sitter.
package typescript

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/huandert/repolens/internal/parser"
)

// httpMethods used to recognize Express-style route handlers, e.g.
// `router.post("/users", handler)`.
var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true, "patch": true,
}

// Analyzer extracts functions, classes, imports and (heuristically) Express
// route endpoints from TypeScript/JavaScript files.
type Analyzer struct{}

// New returns a TypeScript/JavaScript LanguageAnalyzer.
func New() *Analyzer { return &Analyzer{} }

func (a *Analyzer) Language() string     { return "typescript" }
func (a *Analyzer) Extensions() []string { return []string{".ts", ".tsx", ".js", ".jsx"} }

func (a *Analyzer) Analyze(path string, content []byte) (*parser.FileAnalysis, error) {
	p := sitter.NewParser()
	p.SetLanguage(typescript.GetLanguage())
	tree, err := p.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	defer tree.Close()

	fa := &parser.FileAnalysis{Path: path, Language: "typescript"}
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
		case "import_statement":
			if src2 := n.ChildByFieldName("source"); src2 != nil {
				imp := strings.Trim(text(src2), `"'`)
				startLine, _ := line(n)
				fa.Imports = append(fa.Imports, parser.Import{Path: imp, Line: startLine})
			}
		case "function_declaration":
			if nameNode := n.ChildByFieldName("name"); nameNode != nil {
				name := text(nameNode)
				start, end := line(n)
				fa.Symbols = append(fa.Symbols, parser.Symbol{
					Kind: parser.KindFunction, Name: name, Qualified: name,
					StartLine: start, EndLine: end,
					Signature: signatureLine(text(n)),
					Calls:     extractCalls(n, src),
				})
			}
		case "class_declaration":
			nameNode := n.ChildByFieldName("name")
			if nameNode == nil {
				break
			}
			className := text(nameNode)
			start, end := line(n)
			var implements []string
			if heritage := findChildOfType(n, "class_heritage"); heritage != nil {
				implements = extractHeritageNames(heritage, src)
			}
			fa.Symbols = append(fa.Symbols, parser.Symbol{
				Kind: parser.KindClass, Name: className, Qualified: className,
				StartLine: start, EndLine: end,
				Signature:  signatureLine(text(n)),
				Implements: implements,
			})
			// Methods inside the class body, qualified as Class.method.
			if body := n.ChildByFieldName("body"); body != nil {
				for i := 0; i < int(body.ChildCount()); i++ {
					m := body.Child(i)
					if m.Type() != "method_definition" {
						continue
					}
					mNameNode := m.ChildByFieldName("name")
					if mNameNode == nil {
						continue
					}
					mName := text(mNameNode)
					mStart, mEnd := line(m)
					fa.Symbols = append(fa.Symbols, parser.Symbol{
						Kind: parser.KindMethod, Name: mName, Qualified: className + "." + mName,
						StartLine: mStart, EndLine: mEnd,
						Signature: signatureLine(text(m)),
						Calls:     extractCalls(m, src),
					})
				}
			}
		case "call_expression":
			if ep := detectEndpoint(n, src); ep != "" {
				start, end := line(n)
				fa.Symbols = append(fa.Symbols, parser.Symbol{
					Kind: parser.KindEndpoint, Name: ep, Qualified: ep,
					StartLine: start, EndLine: end,
					Signature: signatureLine(text(n)),
					Calls:     extractCalls(n, src),
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

func findChildOfType(n *sitter.Node, t string) *sitter.Node {
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c.Type() == t {
			return c
		}
	}
	return nil
}

func extractHeritageNames(n *sitter.Node, src []byte) []string {
	var names []string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n.Type() == "identifier" {
			names = append(names, n.Content(src))
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(n)
	return names
}

// detectEndpoint recognizes Express-style calls like
// `app.get("/users", handler)` or `router.post("/users/:id", handler)` and
// returns "GET /users" style endpoint names.
func detectEndpoint(n *sitter.Node, src []byte) string {
	fn := n.ChildByFieldName("function")
	if fn == nil || fn.Type() != "member_expression" {
		return ""
	}
	prop := fn.ChildByFieldName("property")
	if prop == nil {
		return ""
	}
	method := prop.Content(src)
	if !httpMethods[method] {
		return ""
	}
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}
	first := args.NamedChild(0)
	if first.Type() != "string" {
		return ""
	}
	route := strings.Trim(first.Content(src), `"'`)
	return strings.ToUpper(method) + " " + route
}

func signatureLine(full string) string {
	if idx := strings.Index(full, "{"); idx >= 0 {
		full = full[:idx]
	}
	return strings.TrimSpace(strings.Join(strings.Fields(full), " "))
}

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
					calls = append(calls, parser.CallRef{Name: name})
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
	case "member_expression":
		if prop := fn.ChildByFieldName("property"); prop != nil {
			return prop.Content(src)
		}
	}
	return ""
}
